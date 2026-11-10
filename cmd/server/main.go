// LACTSSL 是一款面向个人使用的 SSL 证书自动化管理工具。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lactssl/internal/acme"
	"lactssl/internal/api"
	"lactssl/internal/auth"
	"lactssl/internal/backup"
	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
	"lactssl/internal/webui"
)

// cliLocale 从环境变量推断命令行语言。
func cliLocale() i18n.Locale {
	if value := os.Getenv("LACTSSL_LANG"); value != "" {
		return i18n.Normalize(value)
	}
	return i18n.ParseAcceptLanguage(os.Getenv("LANG"))
}

// cliText 翻译命令行文案。
func cliText(key string, pairs ...any) string { return i18n.Tf(cliLocale(), key, pairs...) }

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.load_config"), err)
	}
	// 上次恢复请求写入的备份在打开数据库前应用，避免运行中替换 SQLite 文件。
	if restored, applied, err := applyPendingRestore(cfg); err != nil {
		log.Printf("%s: %v", cliText("cli.error.restore_backup"), err)
	} else if applied {
		cfg = restored
		log.Println(cliText("cli.restore_applied"))
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}
	defer conn.Close()

	cipher, err := util.NewCipher(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_crypto"), err)
	}
	st := store.New(conn, cipher)
	st.ResetProcessingACMEChallenges()

	entry, err := ensureEntry(st, cfg.Entry)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_entry"), err)
	}
	ensureDefaults(st)
	migrateLegacyEABSettings(st)

	authManager := auth.New(cfg.JWTSecret)
	manager := service.New(st)
	if err := manager.MigrateDeployConfigSplit(); err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}

	server := api.New(st, authManager, manager, entry, webui.Assets(), cfg.TrustedProxies)
	server.ACMEBaseURL = cfg.ACME.BaseURL
	if server.ACMEBaseURL == "" && cfg.ACME.Addr != "" {
		scheme := "http"
		if cfg.ACME.TLSCert != "" {
			scheme = "https"
		}
		server.ACMEBaseURL = scheme + "://" + displayAddr(cfg.ACME.Addr)
	}
	server.Backup = backup.New(conn, cfg.DataDir, cfg.DBPath, cfg.File, api.Version)
	restart := make(chan struct{}, 1)
	server.Restart = func() {
		select {
		case restart <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	var mainHandler http.Handler = server.Router()
	var acmeHTTPServer *http.Server
	if cfg.ACME.Enabled {
		acmeHandler := acme.NewServer(st, cfg.ACME.BaseURL).Handler()
		if cfg.ACME.Addr == "" {
			// 未配置独立端口时，把 ACME 协议挂到主端口的 /acme 前缀。
			root := http.NewServeMux()
			root.Handle("/acme/", acmeHandler)
			root.Handle("/", mainHandler)
			mainHandler = root
		} else {
			acmeHTTPServer = &http.Server{
				Addr:              cfg.ACME.Addr,
				Handler:           acmeHandler,
				ReadHeaderTimeout: 15 * time.Second,
			}
			if cfg.ACME.TLSCert != "" {
				pool := x509.NewCertPool()
				if cfg.ACME.MTLSEnabled {
					data, err := os.ReadFile(cfg.ACME.MTLSClientCA)
					if err != nil {
						log.Fatalf("读取 ACME mTLS 客户端 CA 失败: %v", err)
					}
					if !pool.AppendCertsFromPEM(data) {
						log.Fatalf("ACME mTLS 客户端 CA 中没有有效证书")
					}
				}
				acmeHTTPServer.TLSConfig = &tls.Config{
					MinVersion: tls.VersionTLS12,
					ClientCAs:  pool,
					ClientAuth: clientAuthMode(cfg.ACME.MTLSEnabled),
				}
			}
			go func() {
				var err error
				if cfg.ACME.TLSCert != "" {
					err = acmeHTTPServer.ListenAndServeTLS(cfg.ACME.TLSCert, cfg.ACME.TLSKey)
				} else {
					err = acmeHTTPServer.ListenAndServe()
				}
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatalf("ACME HTTP 服务启动失败: %v", err)
				}
			}()
			log.Printf("ACME 服务监听于 %s，对外地址 %s", cfg.ACME.Addr, displayACMECfg(cfg))
		}
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mainHandler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		printStartup(cfg, entry, st)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("%s: %v", cliText("cli.error.http_start"), err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
		log.Println(cliText("cli.shutting_down"))
	case <-restart:
		log.Println(cliText("cli.restoring_backup"))
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	if acmeHTTPServer != nil {
		_ = acmeHTTPServer.Shutdown(shutdownCtx)
	}
	log.Println(i18n.T(cliLocale(), "log.system.exit", nil))
}

func clientAuthMode(required bool) tls.ClientAuthType {
	if required {
		return tls.RequireAndVerifyClientCert
	}
	return tls.NoClientCert
}

func displayACMECfg(cfg *config.Config) string {
	if cfg.ACME.BaseURL != "" {
		return cfg.ACME.BaseURL
	}
	if cfg.ACME.Addr != "" {
		return displayAddr(cfg.ACME.Addr)
	}
	return displayAddr(cfg.Addr)
}

// applyPendingRestore 在启动时应用界面上传的待恢复备份。
// 配置文件先替换并重新加载，使恢复后的数据库路径与密钥立即生效。
func applyPendingRestore(cfg *config.Config) (*config.Config, bool, error) {
	pending, err := backup.LoadPending(cfg.DataDir)
	if err != nil {
		return cfg, false, err
	}
	if pending == nil {
		return cfg, false, nil
	}
	current := cfg
	applied := false
	if pending.ConfigPath != "" {
		if err := backup.ApplyConfig(current.File, pending.ConfigPath); err != nil {
			return cfg, false, err
		}
		reloaded, err := config.Load()
		if err != nil {
			return cfg, false, err
		}
		current = reloaded
		applied = true
	}
	if pending.DatabasePath != "" {
		if err := backup.ApplyDatabase(current.DBPath, pending.DatabasePath); err != nil {
			return cfg, applied, err
		}
		applied = true
	}
	if err := pending.Cleanup(); err != nil {
		return current, applied, err
	}
	return current, applied, nil
}

// ensureEntry 读取或生成安全入口。
func ensureEntry(st *store.Store, override string) (string, error) {
	if override != "" {
		entry, err := util.ValidateEntry(override)
		if err != nil {
			return "", err
		}
		previous, _ := st.GetSetting("security.entry")
		if err := st.SetSetting("security.entry", entry); err != nil {
			return "", err
		}
		if previous != "" && previous != entry {
			st.AddLogKey("system", 0, "warning", "log.system.entry_overridden", map[string]any{"previous": previous, "entry": entry})
		}
		return entry, nil
	}
	if existing, _ := st.GetSetting("security.entry"); existing != "" {
		entry, err := util.ValidateEntry(existing)
		if err == nil {
			return entry, nil
		}
		// 旧入口无法作为路由段使用，重新生成以避免服务不可访问。
		st.AddLogKey("system", 0, "warning", "log.system.entry_invalid", map[string]any{"error": err.Error()})
	}
	entry, err := util.GenerateEntry()
	if err != nil {
		return "", err
	}
	if err := st.SetSetting("security.entry", entry); err != nil {
		return "", err
	}
	st.AddLogKey("system", 0, "info", "log.system.entry_generated", map[string]any{"entry": entry})
	return entry, nil
}

// ensureDefaults 补齐默认设置，不覆盖已有值。
func ensureDefaults(st *store.Store) {
	defaults := map[string]string{
		"acme.ca":         "letsencrypt",
		"acme.key_type":   "ecdsa",
		"acme.renew_days": "30",
		"smtp.port":       "465",
		"smtp.ssl":        "true",
		"notify.enabled":  "false",
	}
	for key, value := range defaults {
		if existing, _ := st.GetSetting(key); existing == "" {
			_ = st.SetSetting(key, value)
		}
	}
}

// migrateLegacyEABSettings 把旧版本全局 EAB 凭据迁移到默认 CA 的独立配置。
func migrateLegacyEABSettings(st *store.Store) {
	legacyKid, _ := st.GetSetting("acme.eab_kid")
	legacyHMAC, _ := st.GetSecret("acme.eab_hmac")
	if legacyKid == "" && legacyHMAC == "" {
		return
	}
	caValue, _ := st.GetSetting("acme.ca")
	if caValue == "" {
		caValue = "letsencrypt"
	}
	ca, found := service.CAInfo(caValue)
	if !found {
		return
	}
	if legacyKid != "" {
		if existing, _ := st.GetSetting(service.CAEABKidSettingKey(ca.Value)); existing == "" {
			_ = st.SetSetting(service.CAEABKidSettingKey(ca.Value), legacyKid)
		}
	}
	if legacyHMAC != "" {
		if existing, _ := st.GetSecret(service.CAEABHMACSettingKey(ca.Value)); existing == "" {
			_ = st.SetSecret(service.CAEABHMACSettingKey(ca.Value), legacyHMAC)
		}
	}
}

func printStartup(cfg *config.Config, entry string, st *store.Store) {
	addr := displayAddr(cfg.Addr)
	count, _ := st.CountUsers()
	fmt.Println("==============================================")
	fmt.Println("  " + cliText("cli.banner"))
	fmt.Println("  " + cliText("cli.version", "version", api.Version))
	fmt.Println("==============================================")
	fmt.Println("  " + cliText("cli.secure_entry", "url", fmt.Sprintf("http://%s/%s/", addr, entry)))
	fmt.Println("  " + cliText("cli.config_file", "path", cfg.File))
	fmt.Println("  " + cliText("cli.data_dir", "path", cfg.DataDir))
	fmt.Println("  " + webAssetsText())
	if count == 0 {
		fmt.Println("  " + cliText("cli.init_hint"))
	}
	fmt.Println("  " + i18n.T(cliLocale(), "cli.hint", nil))
	fmt.Println("==============================================")
}

// webAssetsText 说明前端资源来源，便于确认页面由本服务直接托管而非代理到 Vite。
func webAssetsText() string {
	source := webui.Source()
	switch source.Kind {
	case webui.SourceEmbedded:
		return cliText("cli.web_assets_embedded")
	case webui.SourceLocal:
		return cliText("cli.web_assets_local", "path", source.Path)
	default:
		return cliText("cli.web_assets_missing")
	}
}

// displayAddr 将通配监听地址转换为可直接访问的本机地址。
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
