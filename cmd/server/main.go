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
	"lactssl/internal/dnsplugin/bundled"
	"lactssl/internal/i18n"
	"lactssl/internal/logging"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/systemlog"
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

	// 日志应尽早在配置确定后初始化，使后续所有组件的标准输出与标准库 log
	// 都进入同一格式；恢复备份可能替换配置，因此放在恢复流程之后。
	closeLog, err := logging.Init(logging.Config{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		File:       cfg.Log.File,
		AlsoStdout: cfg.Log.AlsoStdout,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAgeDays: cfg.Log.MaxAgeDays,
		Compress:   cfg.Log.Compress,
	})
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_log"), err)
	}
	defer func() {
		if err := closeLog(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭日志文件失败: %v\n", err)
		}
	}()
	logger := logging.Component("main")
	logger.Info("进程启动",
		"version", api.Version,
		"pid", os.Getpid(),
		"addr", cfg.Addr,
		"data_dir", cfg.DataDir,
		"log_level", cfg.Log.Level,
		"log_file", cfg.Log.File,
	)

	// 发布版主程序内嵌 DNS helper；首次启动或内置版本变化时写入插件目录，
	// 这样 Release 包无需手动复制 helper 即可使用内置服务商。
	if cfg.Plugins.Enabled {
		if path, installed, err := bundled.Install(cfg.Plugins.Dir); err != nil {
			log.Printf("安装内置 DNS helper 失败: %v", err)
		} else if installed {
			log.Printf("已安装内置 DNS helper 到 %s", path)
		}
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
	// 系统日志落库使用独立异步接收器，避免 SQLite 写入阻塞请求。
	// 日志表在 db.Open 的迁移中已经创建，此时可以安全挂载接收器。
	st.SystemLogRetentionDays = cfg.Log.SystemRetentionDays
	st.SystemLogMaxRows = cfg.Log.SystemMaxRows
	if cfg.Log.SystemEnabled {
		systemSink := systemlog.New(st, systemlog.Options{
			MinLevel:      logging.Level(cfg.Log.SystemLevel),
			IncludeAccess: cfg.Log.SystemAccess,
		})
		logging.SetSink(systemSink.Write)
		defer systemSink.Close()
	} else {
		// 丢弃数据库尚未就绪时暂存的启动日志。
		logging.SetSink(nil)
	}
	if err := st.CleanupSystemLogs(); err != nil {
		log.Printf("清理系统日志失败: %v", err)
	}
	// 进程重启后强制所有登录失效：清空服务端会话记录，
	// 并配合下面的启动级 JWT 签名密钥，使旧令牌无法再通过校验。
	if err := st.DeleteAllSessions(); err != nil {
		log.Printf("清理历史登录会话失败: %v", err)
	}
	if err := st.MigrateTOTPSecrets(); err != nil {
		log.Printf("迁移 TOTP 密钥加密失败: %v", err)
	}
	if err := st.MigrateCertificateKeys(); err != nil {
		log.Printf("迁移证书私钥加密失败: %v", err)
	}
	st.ResetProcessingACMEChallenges()

	entry, err := ensureEntry(st, cfg.Entry)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_entry"), err)
	}
	ensureDefaults(st)
	migrateLegacyEABSettings(st)

	// 每次启动都生成新的 JWT 签名子密钥，旧令牌在重启后必然签名校验失败。
	authManager, err := auth.NewBootScoped(cfg.JWTSecret)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_auth"), err)
	}
	manager := service.New(st)
	if err := manager.MigrateDeployConfigSplit(); err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}

	server := api.New(st, authManager, manager, entry, webui.Assets(), cfg.TrustedProxies)
	server.AccessLog = cfg.Log.AccessLog
	server.Logger = logging.Component("http")
	server.SystemLogEnabled = cfg.Log.SystemEnabled
	server.PublicURL = cfg.PublicURL
	server.PluginsEnabled = cfg.Plugins.Enabled
	server.PluginDir = cfg.Plugins.Dir
	server.PluginMemoryLimitMB = cfg.Plugins.MemoryLimitMB
	server.PluginTimeout = cfg.Plugins.Timeout
	server.PluginMaxOutputKB = cfg.Plugins.MaxOutputKB
	server.DeployPluginsEnabled = cfg.DeployPlugins.Enabled
	server.DeployPluginDir = cfg.DeployPlugins.Dir
	server.DeployPluginMemoryLimitMB = cfg.DeployPlugins.MemoryLimitMB
	server.DeployPluginTimeout = cfg.DeployPlugins.Timeout
	server.DeployPluginMaxOutputKB = cfg.DeployPlugins.MaxOutputKB
	dnsPlugins := server.DiscoverPlugins()
	if dnsPlugins.Helpers > 0 || len(dnsPlugins.Errors) > 0 {
		log.Printf("外部 DNS 插件: %d 个 helper / %d 个服务商，%d 个与内置实现重复已忽略（目录 %s）", dnsPlugins.Helpers, dnsPlugins.Providers, len(dnsPlugins.Skipped), dnsPlugins.Dir)
		for _, message := range dnsPlugins.Errors {
			log.Printf("外部 DNS 插件警告: %s", message)
		}
	} else if cfg.Plugins.Enabled {
		// 主程序不再内置 DNS 服务商，没有 helper 时账户页的服务商列表为空。
		log.Printf("未发现 DNS 插件 helper（目录 %s）；请安装 lactssl-dns-helper-extra 后在插件页重扫", dnsPlugins.Dir)
	}
	if result := server.DiscoverDeployPlugins(); result.Helpers > 0 || len(result.Errors) > 0 || len(result.Skipped) > 0 {
		log.Printf("外部部署插件: %d 个 helper / %d 个部署目标，%d 个与内置实现重复已忽略（目录 %s）", result.Helpers, result.Deployers, len(result.Skipped), result.Dir)
		for _, message := range result.Errors {
			log.Printf("外部部署插件警告: %s", message)
		}
	}
	// 合并后的部署目标只有在 helper 已提供规范 ID 时才迁移，避免旧 helper
	// 安装环境下账号被改成不可用的新类型。
	if err := manager.MigrateDeployTargets(); err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}
	server.ACMEBaseURL = cfg.ACME.BaseURL
	if server.ACMEBaseURL == "" && cfg.ACME.Addr != "" {
		scheme := "http"
		if cfg.ACME.TLSCert != "" {
			scheme = "https"
		}
		server.ACMEBaseURL = scheme + "://" + displayAddr(cfg.ACME.Addr)
	}
	if cfg.ACME.Enabled && server.ACMEBaseURL == "" {
		log.Println("警告: 未配置 acme.base_url 或 security.public_url，ACME 资源 URL 将按请求 Host 推导；公网部署请配置固定地址")
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
		acmeServer := acme.NewServer(st, cfg.ACME.BaseURL)
		acmeServer.TrustedProxies = cfg.TrustedProxies
		acmeHandler := acmeServer.Handler()
		if cfg.ACME.Addr == "" {
			// 未配置独立端口时，把 ACME 协议挂到主端口的 /acme 前缀。
			// CRL/OCSP 位于 /pki/<ca>/...，必须一起挂载，否则证书中的吊销地址会 404。
			mainHandler = mountACMEHandler(mainHandler, acmeHandler)
		} else {
			acmeHTTPServer = &http.Server{
				Addr:              cfg.ACME.Addr,
				Handler:           acmeHandler,
				ReadHeaderTimeout: 15 * time.Second,
				ReadTimeout:       10 * time.Minute,
				WriteTimeout:      10 * time.Minute,
				IdleTimeout:       2 * time.Minute,
				MaxHeaderBytes:    1 << 20,
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
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		printStartup(cfg, entry, st)
		logger.Info("HTTP 服务监听", "addr", cfg.Addr, "acme_enabled", cfg.ACME.Enabled)
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

// mountACMEHandler 把 ACME 协议与 PKI（CRL/OCSP）一起挂载到主端口。
// 两者由同一个 Handler 提供，缺少 /pki/ 会让证书中的吊销地址不可达。
func mountACMEHandler(mainHandler, acmeHandler http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/acme/", acmeHandler)
	root.Handle("/pki/", acmeHandler)
	root.Handle("/", mainHandler)
	return root
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
		// 在替换现网配置前完整预检，避免坏配置写入后每次启动都失败。
		if err := config.ValidateFile(pending.ConfigPath); err != nil {
			_ = pending.Quarantine()
			return cfg, false, err
		}
		// 保留一份原配置，加载失败时可以回滚。
		backupFile := current.File + ".before-restore"
		original, readErr := os.ReadFile(current.File)
		if readErr == nil {
			_ = os.WriteFile(backupFile, original, 0o600)
		}
		if err := backup.ApplyConfig(current.File, pending.ConfigPath); err != nil {
			_ = pending.Quarantine()
			return cfg, false, err
		}
		reloaded, err := config.Load()
		if err != nil {
			if readErr == nil {
				_ = os.WriteFile(current.File, original, 0o600)
			}
			_ = os.Remove(backupFile)
			_ = pending.Quarantine()
			return cfg, false, err
		}
		_ = os.Remove(backupFile)
		current = reloaded
		applied = true
	}
	if pending.DatabasePath != "" {
		if err := backup.ApplyDatabase(current.DBPath, pending.DatabasePath); err != nil {
			_ = pending.Quarantine()
			return current, applied, err
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
			st.AddLogKey("system", 0, "warning", "log.system.entry_overridden", map[string]any{"previous": util.MaskSecret(previous), "entry": util.MaskSecret(entry)})
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
	// 不在持久化日志中记录入口明文，避免日志/备份泄露安全入口。
	st.AddLogKey("system", 0, "info", "log.system.entry_generated", map[string]any{"entry": util.MaskSecret(entry)})
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
	if count == 0 {
		fmt.Println("  " + cliText("cli.secure_entry", "url", fmt.Sprintf("http://%s/%s/", addr, entry)))
		fmt.Println("  " + cliText("cli.init_hint"))
	} else {
		// 初始化后不再把安全入口写入日志，避免容器日志或日志采集系统长期保存入口。
		fmt.Println("  " + cliText("cli.secure_entry_hidden"))
	}
	fmt.Println("  " + cliText("cli.config_file", "path", cfg.File))
	fmt.Println("  " + cliText("cli.data_dir", "path", cfg.DataDir))
	fmt.Println("  " + webAssetsText())
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
