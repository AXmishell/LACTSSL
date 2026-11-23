// LACTSSL 是一款面向个人使用的 SSL 证书自动化管理工具。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lactssl/internal/acme"
	"lactssl/internal/api"
	"lactssl/internal/auth"
	"lactssl/internal/backup"
	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
	"lactssl/internal/i18n"
	"lactssl/internal/logarchive"
	"lactssl/internal/logging"
	"lactssl/internal/netutil"
	deployplugin "lactssl/internal/plugin/deploy"
	dnsplugin "lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/exec"
	notifyplugin "lactssl/internal/plugin/notify"
	"lactssl/internal/plugin/sandbox"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/truststore"
	"lactssl/internal/upgrade"
	"lactssl/internal/util"
	"lactssl/internal/webui"
)

// cliLocale 从系统语言推断命令行语言。
func cliLocale() i18n.Locale {
	return i18n.ParseAcceptLanguage(os.Getenv("LANG"))
}

// cliText 翻译命令行文案。
func cliText(key string, pairs ...any) string { return i18n.Tf(cliLocale(), key, pairs...) }

func main() {
	// 上传插件沙箱包装器：由 pluginexec 以隐藏子命令方式启动，
	// 应用 Landlock/rlimit 后 exec 插件，不进入正常服务启动流程。
	if len(os.Args) > 1 && os.Args[1] == "__plugin_sandbox" {
		// 父进程负责降权；pluginexec 在宿主仍为 root 时会拒绝启动本包装器，
		// 因此这里不再依赖子进程环境变量做第二次降权（环境已被最小化过滤）。
		if err := sandbox.RunCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(126)
		}
		return
	}

	configPath := flag.String("config", config.DefaultPath, "config.yaml 文件路径")
	flag.Parse()

	// 上传插件 strict 沙箱通过同一二进制的隐藏子命令执行。
	if executable, err := os.Executable(); err == nil {
		pluginexec.SandboxLauncher = executable
	}

	// 容器入口以 root 修正挂载目录归属后，通过环境变量要求主程序降权。
	// 普通二进制启动（非 root 或未设置变量）不会改变运行用户。
	if err := dropPrivileges(); err != nil {
		log.Fatalf("切换运行用户失败: %v", err)
	}

	cfg, err := config.LoadFrom(*configPath)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.load_config"), err)
	}
	// 上次恢复请求写入的备份在打开数据库前应用，避免运行中替换 SQLite 文件。
	restoreApplied := false
	if restored, applied, err := applyPendingRestore(cfg); err != nil {
		log.Printf("%s: %v", cliText("cli.error.restore_backup"), err)
	} else if applied {
		cfg = restored
		restoreApplied = true
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
		"database_driver", cfg.DBDriver,
		"log_level", cfg.Log.Level,
		"log_file", cfg.Log.File,
	)
	if runningAsRoot() {
		logger.Warn("服务以 root 运行，上传插件将被拒绝执行；请使用非 root 用户运行，或设置 LACTSSL_RUN_UID/LACTSSL_RUN_GID 让主程序降权")
	}

	cipher, err := util.NewCipher(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_crypto"), err)
	}
	conversionState, _ := dbconvert.LoadState(cfg.DataDir)
	conn, err := openConfiguredDB(cfg)
	if err != nil && conversionState != nil && conversionState.Phase == dbconvert.PhaseSwitching {
		if restored, rollbackErr := rollbackConversion(cfg, conversionState, err); rollbackErr == nil {
			cfg = restored
			conn, err = openConfiguredDB(cfg)
		}
	}
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}
	cfg, conn, err = finalizeConversion(cfg, conn, conversionState, cipher)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}
	defer conn.Close()
	if restoreApplied {
		if err := dbconvert.VerifyStarted(context.Background(), conn, cipher); err != nil {
			log.Fatalf("恢复备份校验失败: %v", err)
		}
	}

	st := store.New(conn, cipher)
	// 系统日志落库使用独立异步接收器，避免 SQLite 写入阻塞请求。
	// 日志表在 db.Open 的迁移中已经创建，此时可以安全挂载接收器。
	st.SystemLogRetentionDays = cfg.Log.SystemRetentionDays
	st.SystemLogMaxRows = cfg.Log.SystemMaxRows
	var logArchiveService *logarchive.Service
	if cfg.Log.Archive.Enabled {
		archiver := logarchive.New(st, logarchive.Options{
			RootDir:       cfg.Log.Archive.Dir,
			Compress:      cfg.Log.Archive.Compress,
			RetentionDays: cfg.Log.Archive.RetentionDays,
			MaxTotalMB:    cfg.Log.Archive.MaxTotalMB,
			QuotaPolicy:   cfg.Log.Archive.QuotaPolicy,
			AppVersion:    api.Version,
		})
		st.Archiver = archiver
		logArchiveService = archiver
		if err := archiver.Load(); err != nil {
			logger.Warn("扫描日志归档目录失败", "dir", archiver.Root(), "error", err)
		}
		logger.Info("日志归档已启用", "dir", archiver.Root())
	} else {
		logger.Warn("日志归档已关闭：清空日志将直接删除且不可恢复")
	}
	if cfg.Log.SystemEnabled {
		systemSink := logging.NewDBSink(st, logging.DBSinkOptions{
			MinLevel:      logging.Level(cfg.Log.SystemLevel),
			IncludeAccess: cfg.Log.SystemAccess,
		})
		logging.SetSink(systemSink.Write)
		defer systemSink.Close()
	} else {
		// 丢弃数据库尚未就绪时暂存的启动日志。
		logging.SetSink(nil)
	}

	manager := service.New(st)
	// 把系统根证书、导入的内部 CA 与私有 CA 合并成一个信任链文件。
	// 插件 helper、部署命令与通知渠道进程通过 SSL_CERT_FILE 读取它，
	// 从而在访问内网 HTTPS 目标时仍能完成证书校验。
	if bundlePath, err := manager.SyncTrustBundle(cfg.DataDir); err != nil {
		logger.Warn("生成 TLS 信任链失败", "error", err)
	} else if truststore.HasSystemBundle() && bundlePath != "" {
		if err := truststore.SetEnv(bundlePath); err != nil {
			logger.Warn("设置 TLS 信任链环境变量失败", "error", err)
		}
	}
	// 本地部署禁止写入数据目录、配置目录与程序目录，避免部署参数覆盖
	// 数据库、config.yaml 或正在运行的二进制。
	manager.ProtectedPaths = []string{cfg.DataDir, filepath.Dir(cfg.File)}
	if exePath, exeErr := os.Executable(); exeErr == nil {
		manager.ProtectedPaths = append(manager.ProtectedPaths, filepath.Dir(exePath))
	}
	// 升级迁移集中到 internal/upgrade：启动时自动执行历史兼容步骤，
	// 也可以通过 /upgrade 页面手动预检与优化。
	upgrader, err := upgrade.New(upgrade.Options{
		DB:                 conn,
		Store:              st,
		Manager:            manager,
		ConfigPath:         cfg.File,
		DataDir:            cfg.DataDir,
		DBPath:             cfg.DBPath,
		LegacySecretFile:   cfg.LegacySecretFile,
		AppVersion:         api.Version,
		Auto:               cfg.Upgrade.Auto,
		AutoOptimize:       cfg.Upgrade.AutoOptimize,
		Vacuum:             cfg.Upgrade.Vacuum,
		SessionPersistence: cfg.SessionPersistence,
		Logger:             logger,
	})
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_db"), err)
	}
	if err := upgrader.AutoMigrate(context.Background()); err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.upgrade"), err)
	}
	// 迁移完成后再清理，确保刚复制到统一 logs 表的旧系统日志也遵守保留策略。
	if err := st.CleanupSystemLogs(); err != nil {
		log.Printf("清理系统日志失败: %v", err)
	}
	// 程序自升级在 /upgrade 页面提供检查、下载、校验与替换；是否允许由配置控制。
	programUpdater, err := upgrade.NewProgramUpdater(upgrade.ProgramOptions{
		CurrentVersion: api.Version,
		Enabled:        cfg.Upgrade.Program.Enabled,
		Repo:           cfg.Upgrade.Program.Repo,
		Channel:        cfg.Upgrade.Program.Channel,
		DataDir:        cfg.DataDir,
		CheckInterval:  cfg.Upgrade.Program.CheckInterval,
		MaxDownloadMB:  cfg.Upgrade.Program.MaxDownloadMB,
		PublicKey:      cfg.Upgrade.Program.PublicKey,
		AllowUnsigned:  cfg.Upgrade.Program.AllowUnsigned,
		Token:          cfg.Upgrade.Program.Token,
		Store:          st,
		Logger:         logger,
	})
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.upgrade"), err)
	}
	if cfg.Upgrade.Program.Enabled && strings.TrimSpace(cfg.Upgrade.Program.PublicKey) == "" {
		if cfg.Upgrade.Program.AllowUnsigned {
			logger.Warn("程序自升级未配置 upgrade.program.public_key，但已显式允许未签名升级；生产环境不建议")
		} else {
			logger.Warn("程序自升级未配置 upgrade.program.public_key：官方构建通常已内置发布公钥；自编译版本将拒绝在线升级，请配置 Ed25519 发布公钥，或在受控环境显式设置 allow_unsigned")
		}
	}

	// 默认开启会话保持：进程重启后保留服务端会话，并使用固定 jwt_secret 签发/校验 JWT。
	// 显式设置 security.session_persistence: false 时清空会话，并改用启动级随机子密钥，
	// 使所有旧令牌在重启后立即失效。
	if cfg.SessionPersistence {
		logger.Info("会话保持已启用：进程重启后保留登录会话")
	} else if err := st.DeleteAllSessions(); err != nil {
		log.Printf("清理历史登录会话失败: %v", err)
	}

	entry, err := ensureEntry(st, cfg.Entry)
	if err != nil {
		log.Fatalf("%s: %v", cliText("cli.error.init_entry"), err)
	}
	ensureDefaults(st)
	// 配置保存后写入的“待重启”标记只对当前进程有效；启动成功即视为已生效。
	_ = st.DeleteSetting("config.pending_restart")
	// 恢复上次进程退出时尚未完成的安全提醒投递，避免队列一直等到下一条
	// 新事件写入后才被 worker 处理。
	manager.StartSecurityWorker()

	// 会话保持开启时使用固定 jwt_secret 派生的子密钥，重启后旧令牌仍可通过校验；
	// 显式关闭时每次启动生成新的签名子密钥，旧令牌在重启后必然失效。
	var authManager *auth.Manager
	if cfg.SessionPersistence {
		authManager = auth.New(cfg.JWTSecret)
	} else {
		authManager, err = auth.NewBootScoped(cfg.JWTSecret)
		if err != nil {
			log.Fatalf("%s: %v", cliText("cli.error.init_auth"), err)
		}
	}

	server := api.New(st, authManager, manager, entry, webui.Assets(), cfg.TrustedProxies, cfg.RateLimit)
	if os.Getenv("LACTSSL_DEV_ORIGIN") == "1" {
		server.AllowLoopbackOrigin = true
	}
	server.Upgrade = upgrader
	server.LogArchives = logArchiveService
	server.Program = programUpdater
	converter := dbconvert.NewManager(conn, db.Dialect(cfg.DBDriver), cfg.DBPath, cfg.DBDSN, cfg.DataDir, cfg.File, cipher, api.Version)
	converter.OnMaintenance = func(active bool) { server.SetMaintenance(active) }
	converter.OnSwitching = func() { server.Restart() }
	server.DBConvert = converter
	server.ConfigFile = cfg.File
	server.DataDir = cfg.DataDir
	server.AccessLog = cfg.Log.AccessLog
	server.Logger = logging.Component("http")
	server.SystemLogEnabled = cfg.Log.SystemEnabled
	server.PublicURL = cfg.PublicURL
	server.ClientIPMode = cfg.ClientIPMode
	// 官方插件源码已合并进主仓库，默认注册内置实现；
	// 仍可通过类别开关与条目白名单按需启用。
	server.BuiltinPluginsEnabled = true
	server.PluginsEnabled = cfg.Plugins.Enabled
	server.PluginDir = cfg.Plugins.Dir
	server.PluginSandbox = cfg.Plugins.Sandbox
	server.PluginMemoryLimitMB = cfg.Plugins.MemoryLimitMB
	server.PluginTimeout = cfg.Plugins.Timeout
	server.PluginMaxOutputKB = cfg.Plugins.MaxOutputKB
	server.DeployPluginsEnabled = cfg.DeployPlugins.Enabled
	server.DeployPluginDir = cfg.DeployPlugins.Dir
	server.DeployPluginSandbox = cfg.DeployPlugins.Sandbox
	server.DeployPluginMemoryLimitMB = cfg.DeployPlugins.MemoryLimitMB
	server.DeployPluginTimeout = cfg.DeployPlugins.Timeout
	server.DeployPluginMaxOutputKB = cfg.DeployPlugins.MaxOutputKB
	server.NotifyPluginsEnabled = cfg.NotifyPlugins.Enabled
	server.NotifyPluginDir = cfg.NotifyPlugins.Dir
	server.NotifyPluginSandbox = cfg.NotifyPlugins.Sandbox
	server.NotifyPluginMemoryLimitMB = cfg.NotifyPlugins.MemoryLimitMB
	server.NotifyPluginTimeout = cfg.NotifyPlugins.Timeout
	server.NotifyPluginMaxOutputKB = cfg.NotifyPlugins.MaxOutputKB
	// 首次初始化需要控制台输出的一次性令牌，避免仅凭门禁入口就能匿名
	// 创建管理员或恢复数据库。初始化成功后令牌会被清空。
	setupToken := ""
	count, countErr := st.CountUsers()
	if countErr != nil {
		log.Fatalf("检查初始化状态失败: %v", countErr)
	}
	if count == 0 {
		token, tokenErr := util.RandomHex(24)
		if tokenErr != nil {
			log.Fatalf("生成初始化令牌失败: %v", tokenErr)
		}
		setupToken = token
		server.SetSetupToken(token)
	}
	dnsPlugins := server.DiscoverPlugins()
	log.Printf("DNS 插件: 内置 %d 个服务商 / 上传 %d 个 helper，启用类别=%t（上传目录 %s）", len(dnsplugin.BuiltinProviders()), dnsPlugins.Helpers, cfg.Plugins.Enabled, dnsPlugins.Dir)
	for _, message := range dnsPlugins.Errors {
		log.Printf("上传 DNS 插件警告: %s", message)
	}
	deployPlugins := server.DiscoverDeployPlugins()
	log.Printf("部署插件: 内置 %d 个目标 / 上传 %d 个 helper，启用类别=%t（上传目录 %s）", len(deployplugin.BuiltinDeployers()), deployPlugins.Helpers, cfg.DeployPlugins.Enabled, deployPlugins.Dir)
	for _, message := range deployPlugins.Errors {
		log.Printf("上传部署插件警告: %s", message)
	}
	notifyPlugins := server.DiscoverNotifyPlugins()
	log.Printf("通知插件: 内置 %d 个渠道 / 上传 %d 个 helper，启用类别=%t（上传目录 %s）", len(notifyplugin.BuiltinChannels()), notifyPlugins.Helpers, cfg.NotifyPlugins.Enabled, notifyPlugins.Dir)
	for _, message := range notifyPlugins.Errors {
		log.Printf("上传通知插件警告: %s", message)
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
	// 内置 ACME 挂载在主端口时，优先复用对外地址，避免证书里的 CRL/OCSP
	// 与资源 URL 依赖请求 Host。
	if server.ACMEBaseURL == "" && cfg.PublicURL != "" {
		server.ACMEBaseURL = cfg.PublicURL
	}
	if cfg.ACME.Enabled && server.ACMEBaseURL == "" {
		log.Println("警告: 未配置 acme.base_url 或 security.public_url，ACME 资源 URL 与证书内 CRL/OCSP 地址将按请求 Host 推导；公网部署请配置固定地址")
	}
	server.Backup = backup.New(conn, cfg.DataDir, cfg.DBPath, cfg.File, api.Version)
	// 重启原因区分备份恢复与程序升级：前者交给进程管理器拉起，
	// 后者在优雅退出后直接 exec 新二进制，避免裸机部署升级后服务停摆。
	restart := make(chan string, 1)
	server.Restart = func() {
		reason := "restore"
		if state, _ := dbconvert.LoadState(cfg.DataDir); state != nil && state.Phase == dbconvert.PhaseSwitching {
			reason = "convert"
		}
		select {
		case restart <- reason:
		default:
		}
	}
	server.RestartProgram = func() {
		select {
		case restart <- "program":
		default:
		}
	}
	server.RestartConfig = func() {
		select {
		case restart <- "config":
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	var mainHandler http.Handler = server.Router()
	var acmeHTTPServer *http.Server
	trustedProxies, err := netutil.CompileTrusted(cfg.TrustedProxies)
	if err != nil {
		log.Fatalf("可信代理配置无效: %v", err)
	}
	serve := func(srv *http.Server, addr string, tlsConfig *tls.Config, certFile, keyFile string) error {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		if cfg.ProxyProtocol {
			listener = netutil.NewProxyProtoListener(listener, trustedProxies)
		}
		if tlsConfig != nil && certFile != "" {
			srv.TLSConfig = tlsConfig
			return srv.ServeTLS(listener, certFile, keyFile)
		}
		return srv.Serve(listener)
	}

	if cfg.ACME.Enabled {
		acmeServer := acme.NewServer(st, cfg.ACME.BaseURL, cfg.RateLimit)
		acmeServer.TrustedProxies = cfg.TrustedProxies
		acmeServer.ClientIPMode = cfg.ClientIPMode
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
				if err := serve(acmeHTTPServer, cfg.ACME.Addr, acmeHTTPServer.TLSConfig, cfg.ACME.TLSCert, cfg.ACME.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	var mainTLSConfig *tls.Config
	if cfg.ServerTLS.Cert != "" {
		mainTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.ServerTLS.ProxyClientCA != "" {
			pool := x509.NewCertPool()
			data, err := os.ReadFile(cfg.ServerTLS.ProxyClientCA)
			if err != nil {
				log.Fatalf("读取 mTLS 代理客户端 CA 失败: %v", err)
			}
			if !pool.AppendCertsFromPEM(data) {
				log.Fatalf("mTLS 代理客户端 CA 中没有有效证书")
			}
			mainTLSConfig.ClientCAs = pool
			mainTLSConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}

	go func() {
		printStartup(cfg, entry, st, setupToken)
		logger.Info("HTTP 服务监听",
			"addr", cfg.Addr,
			"acme_enabled", cfg.ACME.Enabled,
			"tls", cfg.ServerTLS.Cert != "",
			"proxy_protocol", cfg.ProxyProtocol,
		)
		if err := serve(httpServer, cfg.Addr, mainTLSConfig, cfg.ServerTLS.Cert, cfg.ServerTLS.Key); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("%s: %v", cliText("cli.error.http_start"), err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	restartReason := ""
	select {
	case <-quit:
		log.Println(cliText("cli.shutting_down"))
	case restartReason = <-restart:
		switch restartReason {
		case "program":
			log.Println("程序升级完成，正在重启服务")
		case "convert":
			log.Println("数据库切换完成，正在重启服务")
		case "config":
			log.Println("配置文件已更新，正在重启服务")
		default:
			log.Println(cliText("cli.restoring_backup"))
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	if acmeHTTPServer != nil {
		_ = acmeHTTPServer.Shutdown(shutdownCtx)
	}
	// 数据库切换成功后源库会保持冻结，避免切换完成后还有写入。
	// 退出前必须解冻：defer 中的系统日志 sink 会把队列刷新到源库，
	// 冻结状态下写入会永久阻塞，进程无法退出，Docker 等进程管理器
	// 也就不会按 restart policy 拉起新进程。
	conn.Unfreeze()
	if restartReason == "program" || restartReason == "config" {
		// exec 会跳过 defer，先显式关闭数据库，确保 WAL 正常收尾。
		_ = conn.Close()
		if err := reexecProcess(programUpdater.ExecutablePath()); err != nil {
			log.Printf("自动重启失败，请手动重启服务: %v", err)
		}
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
	// 原配置保留到数据库也成功替换后再删除；任一步失败都能回滚到旧配置。
	var originalConfig []byte
	originalRead := false
	backupFile := ""
	if pending.ConfigPath != "" {
		// 在替换现网配置前完整预检，避免坏配置写入后每次启动都失败。
		if err := config.ValidateFile(pending.ConfigPath); err != nil {
			_ = pending.Quarantine()
			return cfg, false, err
		}
		backupFile = current.File + ".before-restore"
		original, readErr := os.ReadFile(current.File)
		originalConfig, originalRead = original, readErr == nil
		if readErr == nil {
			_ = os.WriteFile(backupFile, original, 0o600)
		}
		if err := backup.ApplyConfig(current.File, pending.ConfigPath); err != nil {
			if readErr == nil {
				_ = os.WriteFile(current.File, original, 0o600)
			}
			_ = os.Remove(backupFile)
			_ = pending.Quarantine()
			return cfg, false, err
		}
		reloaded, err := config.LoadFrom(current.File)
		if err != nil {
			if readErr == nil {
				_ = os.WriteFile(current.File, original, 0o600)
			}
			_ = os.Remove(backupFile)
			_ = pending.Quarantine()
			return cfg, false, err
		}
		current = reloaded
		applied = true
	}
	if pending.DatabasePath != "" {
		var applyErr error
		switch pending.Driver {
		case "postgres":
			applyErr = backup.ApplyPostgresDatabase(current.DBDSN, pending.DatabasePath)
		case "mysql":
			applyErr = backup.ApplyMySQLDatabase(current.DBDSN, pending.DatabasePath)
		default:
			applyErr = backup.ApplyDatabase(current.DBPath, pending.DatabasePath)
		}
		if err := applyErr; err != nil {
			// 配置已替换但数据库失败：必须回滚配置，否则下次启动会用新 salt 打开旧库。
			if originalRead {
				_ = os.WriteFile(cfg.File, originalConfig, 0o600)
			}
			if backupFile != "" {
				_ = os.Remove(backupFile)
			}
			_ = pending.Quarantine()
			return cfg, false, fmt.Errorf("恢复数据库失败，已回滚配置: %w", err)
		}
		applied = true
	}
	if backupFile != "" {
		_ = os.Remove(backupFile)
	}
	if err := pending.Cleanup(); err != nil {
		return current, applied, err
	}
	return current, applied, nil
}

// ensureEntry 读取或生成门禁入口。
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
	// 不在持久化日志中记录入口明文，避免日志/备份泄露门禁入口。
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

// migrateLegacyEABSettings 已迁移到 internal/upgrade，启动时由升级工具统一执行。

func printStartup(cfg *config.Config, entry string, st *store.Store, setupToken string) {
	addr := displayAddr(cfg.Addr)
	count, _ := st.CountUsers()
	fmt.Println("==============================================")
	fmt.Println("  " + cliText("cli.banner"))
	fmt.Println("  " + cliText("cli.version", "version", api.Version))
	fmt.Println("==============================================")
	if count == 0 {
		scheme := "http"
		if cfg.ServerTLS.Cert != "" {
			scheme = "https"
		}
		fmt.Println("  " + cliText("cli.secure_entry", "url", fmt.Sprintf("%s://%s/%s/", scheme, addr, entry)))
		if setupToken != "" {
			fmt.Println("  " + cliText("cli.setup_token", "token", setupToken))
		}
		fmt.Println("  " + cliText("cli.init_hint"))
	} else {
		// 初始化后不再把门禁入口写入日志，避免容器日志或日志采集系统长期保存入口。
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
