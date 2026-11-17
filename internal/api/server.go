// Package api 提供 HTTP 接口与前端静态资源服务。
package api

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
	"lactssl/internal/auth"
	"lactssl/internal/backup"
	"lactssl/internal/deployplugin"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/notifyplugin"
	"lactssl/internal/pluginrepo"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/upgrade"
)

// Server 持有接口层依赖。
type Server struct {
	Store   *store.Store
	Auth    *auth.Manager
	Manager *service.Manager
	Backup  *backup.Manager
	WebFS   fs.FS
	// Upgrade 是版本迁移与数据库/配置优化工具，由 main 注入。
	Upgrade *upgrade.Manager
	// Program 是程序二进制自升级工具，由 main 注入。
	Program *upgrade.ProgramUpdater
	// RestartProgram 由 main 注入，用于在程序升级后优雅退出并重新执行新二进制。
	RestartProgram func()
	// ConfigFile 是当前 config.yaml 路径，供初始化页面读写白名单配置项。
	ConfigFile string
	// DataDir 是数据目录，用于持久化插件图标等派生资源。
	DataDir        string
	PublicURL      string
	TrustedProxies []string
	ACMEBaseURL    string
	// PluginsEnabled 与插件目录/限制由 main 从配置注入。
	PluginsEnabled      bool
	PluginDir           string
	PluginMemoryLimitMB int
	PluginTimeout       time.Duration
	PluginMaxOutputKB   int
	// DeployPlugins* 是证书部署插件配置，同样由 main 注入。
	DeployPluginsEnabled      bool
	DeployPluginDir           string
	DeployPluginMemoryLimitMB int
	DeployPluginTimeout       time.Duration
	DeployPluginMaxOutputKB   int
	// NotifyPlugins* 是通知插件配置，同样由 main 注入。
	NotifyPluginsEnabled      bool
	NotifyPluginDir           string
	NotifyPluginMemoryLimitMB int
	NotifyPluginTimeout       time.Duration
	NotifyPluginMaxOutputKB   int
	// PluginRepo* 是插件仓库默认配置，运行期可被 settings 覆盖。
	PluginRepoEnabled       bool
	PluginRepoURL           string
	PluginRepoChannel       string
	PluginRepoAutoUpdate    bool
	PluginRepoPublicKey     string
	PluginRepoTimeout       time.Duration
	PluginRepoMaxDownloadMB int
	PluginRepoCheckInterval time.Duration
	// Restart 由 main 注入，用于在恢复备份后触发优雅退出，让进程管理器拉起新进程。
	Restart func()
	// AccessLog 控制是否记录 HTTP 访问日志；Logger 由 main 注入进程 logger。
	AccessLog bool
	Logger    *slog.Logger
	// SystemLogEnabled 表示是否仍在把进程日志写入数据库。
	SystemLogEnabled bool
	entry            atomic.Value
	// setupToken 是首次初始化使用的一次性令牌。为空表示当前部署未启用
	// （主要供测试直接构造 Server），生产进程由 main 在启动时设置。
	setupToken atomic.Value
	// setupMu 串行化初始化、恢复和初始化配置写入，避免匿名恢复与管理员
	// 首次创建账户之间的检查-写入竞态。
	setupMu   sync.Mutex
	limiter   *rateLimiter
	startedAt time.Time

	pluginMu     sync.Mutex
	pluginResult dnsplugin.DiscoverResult

	// pluginUploadMu 串行化插件上传/安装，避免并发写入同名文件或交错触发热加载。
	pluginUploadMu sync.Mutex

	// pluginRepoMu 保护仓库索引缓存。
	pluginRepoMu        sync.Mutex
	repoGroup           singleflight.Group
	pluginRepoGen       uint64
	pluginRepoIndex     pluginrepo.Index
	pluginRepoIndexRaw  []byte
	pluginRepoETag      string
	pluginRepoCheckedAt time.Time
	pluginRepoErr       string
	// pluginRepoVerified 表示当前索引是否已通过 Ed25519 签名校验。
	pluginRepoVerified bool

	deployPluginMu     sync.Mutex
	deployPluginResult deployplugin.DiscoverResult

	notifyPluginMu     sync.Mutex
	notifyPluginResult notifyplugin.DiscoverResult

	// passkeyCeremonies 暂存 WebAuthn 注册/登录的挑战数据。
	passkeyMu         sync.Mutex
	passkeyCeremonies map[string]passkeyCeremony
}

// New 创建 API 服务。
func New(st *store.Store, a *auth.Manager, mgr *service.Manager, entry string, webFS fs.FS, trustedProxies []string) *Server {
	s := &Server{
		Store:             st,
		Auth:              a,
		Manager:           mgr,
		WebFS:             webFS,
		TrustedProxies:    trustedProxies,
		limiter:           newRateLimiter(defaultLimiterMaxKeys),
		startedAt:         time.Now(),
		passkeyCeremonies: make(map[string]passkeyCeremony),
	}
	s.entry.Store(entry)
	return s
}

// Entry 返回当前门禁入口。
func (s *Server) Entry() string {
	return s.entry.Load().(string)
}

// SetEntry 更新门禁入口。
func (s *Server) SetEntry(entry string) {
	s.entry.Store(entry)
}

const setupTokenHeader = "X-LACTSSL-SETUP-TOKEN"

// SetSetupToken 设置首次初始化使用的一次性令牌。初始化成功后调用方应清空。
func (s *Server) SetSetupToken(token string) {
	s.setupToken.Store(token)
}

// SetupToken 返回当前初始化令牌，仅用于启动日志输出。
func (s *Server) SetupToken() string {
	value, _ := s.setupToken.Load().(string)
	return value
}

// requireSetupToken 校验初始化令牌。未配置令牌时保持旧测试/嵌入式调用可用；
// 生产入口在 main 中始终会在系统未初始化时设置随机令牌。
func (s *Server) requireSetupToken(c *gin.Context) bool {
	expected := s.SetupToken()
	if expected == "" {
		return true
	}
	provided := strings.TrimSpace(c.GetHeader(setupTokenHeader))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
		return true
	}
	failKey(c, http.StatusForbidden, "api.error.setup_token_invalid")
	return false
}

// Router 构建 Gin 路由。
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	// 仅信任显式配置的反向代理；为空时 ClientIP 取 RemoteAddr，避免 X-Forwarded-For 伪造。
	if err := r.SetTrustedProxies(s.TrustedProxies); err != nil {
		s.logger().Warn("可信代理配置无效，已禁用代理头信任", "error", err)
		_ = r.SetTrustedProxies(nil)
	}
	// 访问日志必须包在 recovery 外层，panic 被恢复并写入 500 后才能记录到正确状态码。
	r.Use(s.requestID(), s.accessLog(), s.recovery(), s.bodyLimit(), s.localeMiddleware(), s.securityHeaders(), s.cors())

	// 接口固定挂在裸 /api 下；旧版 /<entry>/api 保留为兼容别名。
	// 安全链按“来源校验 → 匿名限流 → CSRF → 默认认证”执行：
	// 未登记到匿名白名单的接口一律要求登录会话或 API 密钥。
	api := r.Group("/api")
	api.Use(s.originGuard(), s.apiRateLimit(), s.csrfGuard(), s.defaultAuth())
	s.registerAPI(api)

	legacyAPI := r.Group("/:entry/api")
	legacyAPI.Use(s.entryMiddleware(), s.originGuard(), s.apiRateLimit(), s.csrfGuard(), s.defaultAuth())
	s.registerAPI(legacyAPI)

	r.NoRoute(s.serveSPA)
	return r
}

// registerAPI 注册全部管理接口。调用方负责挂载认证与限流中间件，
// 因此同一组路由可以同时暴露在 /api 和旧版 /<entry>/api 下。
func (s *Server) registerAPI(g *gin.RouterGroup) {
	g.GET("/setup/status", s.setupStatus)
	g.GET("/setup/config", s.setupConfig)
	g.PUT("/setup/config", s.updateSetupConfig)
	g.POST("/setup/config/restart", s.restartSetupConfig)
	g.POST("/setup", s.handleSetup)
	g.POST("/setup/restore", s.handleSetupRestore)
	g.POST("/auth/login", s.handleLogin)
	g.POST("/auth/logout", s.handleLogout)
	g.POST("/auth/forgot", s.handleForgotPassword)
	g.POST("/auth/reset", s.handleResetPassword)
	g.GET("/auth/reset/validate", s.handleValidateReset)
	g.POST("/auth/passkey/login/begin", s.handlePasskeyLoginBegin)
	g.POST("/auth/passkey/login/finish", s.handlePasskeyLoginFinish)
	g.GET("/auth/me", s.handleMe)
	g.POST("/auth/change-password", s.handleChangePassword)
	g.POST("/auth/change-email", s.handleChangeEmail)
	g.POST("/auth/totp/setup", s.handleTOTPSetup)
	g.POST("/auth/totp/enable", s.handleTOTPEnable)
	g.POST("/auth/totp/disable", s.handleTOTPDisable)
	g.GET("/auth/passkeys", s.listPasskeys)
	g.POST("/auth/passkeys/register/begin", s.handlePasskeyRegisterBegin)
	g.POST("/auth/passkeys/register/finish", s.handlePasskeyRegisterFinish)
	g.POST("/auth/passkeys/:id/delete", s.handlePasskeyDelete)
	g.GET("/sessions", s.listSessions)
	g.DELETE("/sessions/:id", s.revokeSession)
	g.POST("/sessions/revoke-others", s.revokeOtherSessions)
	g.GET("/meta", s.handleMeta)
	g.GET("/status", s.handleStatus)
	g.GET("/plugins", s.listPlugins)
	g.GET("/plugins/icons", s.listPluginIcons)
	g.POST("/plugins/rescan", s.rescanPlugins)
	g.POST("/plugins/upload", s.uploadPlugin)
	g.DELETE("/plugins/files/:kind/:file", s.deletePluginFile)
	g.PUT("/plugins/files/:kind/:file", s.setPluginFileEnabled)
	g.GET("/plugins/repo", s.getPluginRepo)
	g.PUT("/plugins/repo", s.updatePluginRepo)
	g.POST("/plugins/repo/refresh", s.refreshPluginRepo)
	g.GET("/plugins/repo/plugins", s.listPluginRepoPlugins)
	g.POST("/plugins/repo/install", s.installPluginFromRepo)
	g.POST("/plugins/repo/update", s.updatePluginFromRepo)
	g.DELETE("/plugins/repo/plugins/:kind/:id", s.uninstallPluginFromRepo)
	g.GET("/plugins/repo/legacy", s.listLegacyPlugins)
	g.POST("/plugins/repo/migrate", s.migrateLegacyPlugins)
	g.PUT("/plugins/providers", s.setPluginProvidersEnabled)
	g.PUT("/plugins/providers/:id", s.setPluginProviderEnabled)
	g.PUT("/plugins/deployers", s.setDeployPluginDeployersEnabled)
	g.PUT("/plugins/deployers/:id", s.setDeployPluginDeployerEnabled)
	g.PUT("/plugins/notify-channels", s.setNotifyPluginChannelsEnabled)
	g.PUT("/plugins/notify-channels/:id", s.setNotifyPluginChannelEnabled)
	g.GET("/dashboard", s.handleDashboard)
	g.GET("/ca-configs", s.caConfigs)
	g.PUT("/ca-configs/:value", s.updateCAConfig)
	g.GET("/acme-cas", s.listACMECAs)
	g.POST("/acme-cas", s.createACMECA)
	g.PUT("/acme-cas/:id", s.updateACMECA)
	g.DELETE("/acme-cas/:id", s.deleteACMECA)
	g.GET("/private-cas", s.listPrivateCAs)
	g.POST("/private-cas", s.createPrivateCA)
	g.POST("/private-cas/import", s.importPrivateCA)
	g.GET("/private-cas/:id/download", s.downloadPrivateCA)
	g.DELETE("/private-cas/:id", s.deletePrivateCA)
	g.GET("/acme-endpoints", s.listACMEEndpoints)
	g.POST("/acme-endpoints", s.createACMEEndpoint)
	g.PUT("/acme-endpoints/:id", s.updateACMEEndpoint)
	g.DELETE("/acme-endpoints/:id", s.deleteACMEEndpoint)
	g.GET("/acme-endpoints/:id/eab", s.listACMEEABCredentials)
	g.POST("/acme-endpoints/:id/eab", s.createACMEEABCredential)
	g.DELETE("/acme-eab/:id", s.deleteACMEEABCredential)
	g.GET("/acme-accounts", s.listACMEAccounts)
	g.GET("/acme-orders", s.listACMEOrders)
	g.GET("/acme-orders/:id", s.getACMEOrder)
	g.POST("/acme-diagnostics/cleanup", s.cleanupACMEDiagnostics)
	g.POST("/acme-diagnostics/cleanup/preview", s.previewCleanupACMEDiagnostics)
	g.GET("/dns-accounts", s.listDNSAccounts)
	g.GET("/dns-accounts/:id", s.getDNSAccount)
	g.POST("/dns-accounts", s.createDNSAccount)
	g.PUT("/dns-accounts/:id", s.updateDNSAccount)
	g.DELETE("/dns-accounts/:id", s.deleteDNSAccount)
	g.GET("/deploy-accounts", s.listDeployAccounts)
	g.GET("/deploy-accounts/:id", s.getDeployAccount)
	g.POST("/deploy-accounts", s.createDeployAccount)
	g.PUT("/deploy-accounts/:id", s.updateDeployAccount)
	g.DELETE("/deploy-accounts/:id", s.deleteDeployAccount)
	g.GET("/tasks", s.listTasks)
	g.POST("/tasks", s.createTask)
	g.PUT("/tasks/:id", s.updateTask)
	g.DELETE("/tasks/:id", s.deleteTask)
	g.POST("/tasks/:id/run", s.runTask)
	g.POST("/tasks/batch/run", s.batchRunTasks)
	g.POST("/tasks/batch/enabled", s.batchSetTasksEnabled)
	g.GET("/tasks/:id/logs", s.taskLogs)
	g.GET("/certificates", s.listCertificates)
	g.POST("/certificates/cleanup", s.cleanupCertificates)
	g.POST("/certificates/cleanup/preview", s.previewCleanupCertificates)
	g.GET("/certificates/:id", s.getCertificate)
	g.GET("/certificates/:id/analysis", s.analyzeCertificate)
	g.GET("/certificates/:id/download", s.downloadCertificate)
	g.GET("/certificates/:id/pem", s.getCertificatePEM)
	g.POST("/certificates/:id/deploy", s.deployCertificate)
	g.DELETE("/certificates/:id", s.deleteCertificate)
	g.GET("/deployments", s.listDeployments)
	g.GET("/monitors", s.listMonitors)
	g.POST("/monitors", s.createMonitor)
	g.PUT("/monitors/:id", s.updateMonitor)
	g.DELETE("/monitors/:id", s.deleteMonitor)
	g.POST("/monitors/:id/check", s.checkMonitor)
	g.GET("/logs", s.listLogs)
	g.GET("/logs/export", s.exportLogs)
	g.GET("/system-logs", s.listSystemLogs)
	g.GET("/system-logs/export", s.exportSystemLogs)
	g.DELETE("/system-logs", s.clearSystemLogs)
	g.GET("/settings", s.getSettings)
	g.PUT("/settings", s.updateSettings)
	g.GET("/upgrade", s.getUpgradeStatus)
	g.POST("/upgrade/run", s.runUpgrade)
	g.POST("/upgrade/optimize", s.optimizeUpgrade)
	g.GET("/upgrade/program", s.getProgramStatus)
	g.POST("/upgrade/program/check", s.checkProgram)
	g.POST("/upgrade/program/apply", s.applyProgram)
	g.POST("/upgrade/program/restart", s.restartProgram)
	g.GET("/settings/backup", s.backupInfo)
	g.POST("/settings/backup/download", s.downloadBackup)
	g.POST("/settings/backup/restore", s.restoreBackup)
	g.POST("/settings/test-mail", s.testMail)
	g.POST("/settings/test-channel", s.testChannel)
	g.POST("/security/entry", s.changeEntry)
	g.GET("/api-keys", s.listAPIKeys)
	g.GET("/api-keys/scopes", s.apiKeyScopes)
	g.POST("/api-keys", s.createAPIKey)
	g.PUT("/api-keys/:id", s.updateAPIKey)
	g.DELETE("/api-keys/:id", s.deleteAPIKey)
	g.POST("/api-keys/:id/rotate", s.rotateAPIKey)
}

// localeMiddleware 根据请求头协商语言，供所有失败消息与持久化消息渲染使用。
func (s *Server) localeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(i18n.ContextKey, i18n.FromRequest(c.Request))
		c.Next()
	}
}

// maxJSONBodySize 限制普通接口的请求体大小，备份恢复接口单独放宽。
const maxJSONBodySize int64 = 2 << 20

// bodyLimit 防止超大请求体占用内存；备份恢复使用独立的大小限制。
func (s *Server) bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isLargeUploadPath(c.Request.URL.Path) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodySize)
		}
		c.Next()
	}
}

// isLargeUploadPath 判断是否为允许大文件上传的接口。
func isLargeUploadPath(path string) bool {
	return strings.HasSuffix(path, "/settings/backup/restore") ||
		strings.HasSuffix(path, "/setup/restore") ||
		strings.HasSuffix(path, "/plugins/upload")
}

const cspNonceKey = "lactssl.csp_nonce"

// cspNonce 生成每次请求唯一的 CSP nonce，用于放行注入的门禁入口脚本。
func cspNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退回空 nonce：内联脚本会被 CSP 拦截，但不会降级为允许任意内联脚本。
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}

// securityHeaders 添加基础安全响应头。
func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		nonce := cspNonce()
		c.Set(cspNonceKey, nonce)
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		if s.sessionCookieSecure(c) {
			c.Header("Strict-Transport-Security", "max-age=31536000")
		}
		// 接口响应和页面包含登录态与门禁入口，默认禁止缓存；静态资源会覆盖此值。
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// cors 不再反射任意 Origin。前后端同源部署（开发环境由 Vite 代理），
// 反射 Origin 并允许 credentials 会让 Cookie 会话暴露在跨站请求下。
func (s *Server) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// originGuard 对未认证的状态变更接口做来源校验，阻止跨站登录/初始化/重置请求。
// 不携带 Origin 的非浏览器客户端仍然允许，但浏览器跨站请求会被拒绝。
func (s *Server) originGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !s.sameOrigin(c) {
				failKey(c, http.StatusForbidden, "api.error.unauthorized")
				return
			}
		}
		c.Next()
	}
}

func (s *Server) sameOrigin(c *gin.Context) bool {
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin == "" {
		return !strings.EqualFold(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")), "cross-site")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	// 浏览器禁止脚本伪造 Sec-Fetch-Site；same-origin 可覆盖反向代理改写 Host 的场景。
	if strings.EqualFold(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")), "same-origin") {
		return true
	}
	// 开发模式下浏览器在 :5173，而 Vite 把接口代理到 :8080；仅当双方都在本机回环时放行。
	if isLoopbackRemoteAddr(c.Request.RemoteAddr) && isLoopbackHost(parsed.Host) {
		return true
	}
	allowed := map[string]bool{}
	addHost := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" {
			return
		}
		allowed[strings.ToLower(host)] = true
		allowed[normalizeOriginHost(host)] = true
	}
	addHost(c.Request.Host)
	if s.PublicURL != "" {
		if public, err := url.Parse(s.PublicURL); err == nil && public.Host != "" {
			addHost(public.Host)
		}
	}
	// 反向代理可能改写 Host，仅在请求来自可信代理时采信 X-Forwarded-Host。
	if s.isTrustedProxy(c.Request.RemoteAddr) {
		addHost(c.GetHeader("X-Forwarded-Host"))
	}
	return allowed[normalizeOriginHost(parsed.Host)]
}

// normalizeOriginHost 去掉默认端口，避免 Origin 与 Host 的写法差异导致同源误判。
func normalizeOriginHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ":80")
	host = strings.TrimSuffix(host, ":443")
	return host
}

// clientRateKey 返回用于限流的客户端标识，IPv6 归并到 /64，防止轮换地址绕过。
func clientRateKey(c *gin.Context) string {
	return normalizeRateIP(c.ClientIP())
}

func normalizeRateIP(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// rateLimited 返回统一的 429 响应，并通过 Retry-After 告知客户端重试时间。
func rateLimited(c *gin.Context, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	failKey(c, http.StatusTooManyRequests, "api.error.rate_limited")
}

// authenticate 校验会话 Cookie、Bearer 令牌或 API 密钥，成功后在上下文写入用户。
// 返回 false 表示已经写入错误响应并终止请求；CSRF 由 csrfGuard 统一处理。
func (s *Server) authenticate(c *gin.Context) bool {
	token, fromHeader := s.sessionToken(c)
	// API 密钥也可以放在独立请求头中，便于命令行与自动化工具调用。
	if keyToken := strings.TrimSpace(c.GetHeader(apiKeyHeader)); keyToken != "" {
		token, fromHeader = keyToken, true
	}
	if token == "" {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return false
	}
	// API 密钥使用独立的哈希校验与权限模型，不参与 JWT 与 CSRF 流程。
	if looksLikeAPIKey(token) {
		return s.authenticateAPIKey(c, token)
	}
	// 旧会话的 CSRF Cookie 缺失或过短时顺手补发，后续写请求即可强校验。
	if !fromHeader {
		if csrf, err := s.readCookie(c, csrfCookieName); err != nil || len(csrf) < auth.MinTokenLength {
			s.setCSRFCookie(c, token, time.Now().Add(entryCookieTTL))
		}
	}
	claims, err := s.Auth.Parse(token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": err.Error()})
		return false
	}
	user, err := s.Store.GetUserByID(claims.UserID)
	if err != nil || user.TokenVersion != claims.Version {
		failKey(c, http.StatusUnauthorized, "api.error.session_expired")
		return false
	}
	sessionID, session, err := s.ensureSession(c, claims, token)
	if errors.Is(err, errSessionInvalid) {
		failKey(c, http.StatusUnauthorized, "api.error.session_expired")
		return false
	}
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return false
	}
	c.Set("user", user)
	c.Set("session_id", sessionID)
	c.Set("session_strong_auth", session.StrongAuth)
	return true
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// locale 返回当前请求语言。
func locale(c *gin.Context) i18n.Locale { return i18n.FromContext(c) }

// managerLocale 返回系统设置中保存的语言，用于邮件等后台任务。
func (s *Server) managerLocale() i18n.Locale {
	value, _ := s.Store.GetSetting("language")
	return i18n.Normalize(value)
}

// localize 渲染一条持久化消息。
func localize(c *gin.Context, text string) string { return i18n.Render(locale(c), text) }

func fail(c *gin.Context, status int, message string) {
	// 终止后续处理，避免认证或入口校验失败后仍执行目标接口。
	c.Abort()
	payload := gin.H{"success": false, "message": message}
	if id := c.GetString(requestIDKey); id != "" {
		payload["request_id"] = id
	}
	c.JSON(status, payload)
}

// failKey 返回带语义 key 的可翻译错误。
func failKey(c *gin.Context, status int, key string, pairs ...any) {
	params := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		params[name] = pairs[i+1]
	}
	// 终止后续处理，避免认证或入口校验失败后仍执行目标接口。
	c.Abort()
	payload := gin.H{
		"success": false,
		"code":    key,
		"message": i18n.T(locale(c), key, params),
		"params":  params,
	}
	if id := c.GetString(requestIDKey); id != "" {
		payload["request_id"] = id
	}
	c.JSON(status, payload)
}

// failErr 优先使用可翻译错误，否则回退到错误原文。
// 非中文界面遇到尚未本地化的底层错误时返回通用提示并把原文写入进程日志，
// 避免英文界面混入中文错误；中文界面仍保留完整细节。
func failErr(c *gin.Context, status int, err error) {
	if localized, ok := err.(*i18n.Error); ok && localized.Key != "" {
		failKey(c, status, localized.Key, pairsOf(localized.Params)...)
		return
	}
	text := service.SanitizeErrorText(err.Error())
	if status >= http.StatusInternalServerError {
		slog.Default().Warn("服务端错误",
			"error", text,
			"request_id", c.GetString(requestIDKey),
			"path", c.FullPath(),
		)
		failKey(c, status, "api.error.operation_failed", "request_id", c.GetString(requestIDKey))
		return
	}
	if loc := locale(c); loc != i18n.ZhCN && containsCJK(text) {
		slog.Default().Warn("未本地化的错误",
			"error", text,
			"request_id", c.GetString(requestIDKey),
			"path", c.FullPath(),
		)
		failKey(c, status, "api.error.operation_failed", "request_id", c.GetString(requestIDKey))
		return
	}
	fail(c, status, text)
}

// containsCJK 判断文本是否包含中日韩统一表意文字。
func containsCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func pairsOf(params map[string]any) []any {
	pairs := make([]any, 0, len(params)*2)
	for key, value := range params {
		pairs = append(pairs, key, value)
	}
	return pairs
}

// localizeLogs 渲染日志列表中的结构化消息。
func localizeLogs(c *gin.Context, list []model.LogEntry) []model.LogEntry {
	for index := range list {
		list[index].Message = localize(c, list[index].Message)
	}
	return list
}

// localizeTasks 渲染任务列表中的结构化消息。
func localizeTasks(c *gin.Context, list []model.Task) []model.Task {
	for index := range list {
		list[index].Message = localize(c, list[index].Message)
	}
	return list
}

// serveSPA 返回前端单页应用。
// 访问门禁入口只做一次门禁：写入短期 Cookie 后把地址栏切到裸 /login。
// 登录页由门禁 Cookie 保护，控制台页面由会话 Cookie 保护，入口不再出现在地址栏中。
func (s *Server) serveSPA(c *gin.Context) {
	p := c.Request.URL.Path
	entry := s.Entry()

	// 带哈希名的前端资源不含敏感信息，始终公开。
	if strings.HasPrefix(p, "/assets/") {
		s.serveAsset(c, strings.TrimPrefix(p, "/"))
		return
	}
	if p == "/favicon.svg" || p == "/favicon.ico" || p == "/logo.svg" {
		s.serveAsset(c, strings.TrimPrefix(p, "/"))
		return
	}

	// 入口下和裸路径下的接口不存在时返回 JSON，避免被 SPA fallback 吞成 index.html。
	// 旧版 /<entry>/api 是兼容别名，未匹配到具体接口时同样返回 JSON 404。
	if p == "/"+entry+"/api" || strings.HasPrefix(p, "/"+entry+"/api/") {
		failKey(c, http.StatusNotFound, "api.error.api_not_found")
		return
	}
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		failKey(c, http.StatusNotFound, "api.error.api_not_found")
		return
	}

	// 访问门禁入口：写入门禁 Cookie，再跳转到裸登录页。
	if p == "/"+entry || p == "/"+entry+"/" {
		s.setEntryCookie(c)
		s.ensureDeviceCookie(c)
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// 兼容旧入口下的链接，例如 /<entry>/reset?token=...。
	if strings.HasPrefix(p, "/"+entry+"/") {
		s.setEntryCookie(c)
		s.ensureDeviceCookie(c)
		target := "/login"
		if rest := strings.TrimPrefix(p, "/"+entry); rest == "/login" || rest == "/install" || rest == "/reset" {
			target = rest
		}
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusFound, target)
		return
	}

	// 登录、安装和密码重置页面：必须先在门禁入口拿到门禁 Cookie。
	// 已登录用户也允许打开，避免门禁过期后无法进入登录页重新登录。
	if p == "/login" || p == "/install" || p == "/reset" {
		if !s.hasEntryCookie(c) && s.sessionUser(c) == nil {
			s.entryNotFound(c)
			return
		}
		s.serveIndex(c)
		return
	}

	// 控制台页面必须已登录。
	if s.sessionUser(c) != nil {
		s.serveIndex(c)
		return
	}

	// 未登录且未通过门禁入口：按原始路径直接返回 404，不做重定向，
	// 避免暴露登录页的存在，也避免 /login 在无门禁 Cookie 时二次 404。
	s.entryNotFound(c)
}

// entryNotFound 统一处理未通过入口门禁的请求，并计入失败限流。
// 没有门禁 Cookie 的请求只返回精简版 404；已通过入口但未登录的请求
// 才展示控制台样式的 404 页面（含请求路径与重新进入的提示）。
func (s *Server) entryNotFound(c *gin.Context) {
	if allowed, wait := s.limiter.Allow("entry:"+clientRateKey(c), 60, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	if !s.hasEntryCookie(c) {
		s.notFoundPlain(c)
		return
	}
	s.notFoundPage(c)
}

func (s *Server) serveIndex(c *gin.Context) {
	data, err := fs.ReadFile(s.WebFS, "index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(locale(c), "api.error.web_not_built", nil))
		return
	}
	// 每次返回页面都刷新门禁 Cookie 的有效期，避免使用过程中过期。
	s.setEntryCookie(c)
	// 首次访问页面时补发设备标识，后续保持稳定。
	s.ensureDeviceCookie(c)
	// 接口固定挂在裸 /api；页面只注入接口前缀，入口本身不再暴露给脚本。
	nonceValue, _ := c.Get(cspNonceKey)
	nonce, _ := nonceValue.(string)
	inject := fmt.Sprintf(
		`<script nonce=%q>window.__LACTSSL_API_BASE__=%q;</script>`,
		nonce, "/api",
	)
	if bytes.Contains(data, []byte("</head>")) {
		data = bytes.Replace(data, []byte("</head>"), []byte(inject+"</head>"), 1)
	}
	// 页面包含运行时注入的门禁入口，且是否可访问与登录态相关，禁止缓存。
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) serveAsset(c *gin.Context, name string) {
	// 只允许规范化的 assets/ 路径和固定的公共图标，避免 /assets/../index.html
	// 这类路径被 path.Clean 归一化后绕过页面门禁。
	clean := path.Clean(name)
	if clean != name || !fs.ValidPath(clean) || clean == "." {
		c.Status(http.StatusNotFound)
		return
	}
	if !strings.HasPrefix(clean, "assets/") && clean != "favicon.svg" && clean != "favicon.ico" && clean != "logo.svg" {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := fs.ReadFile(s.WebFS, clean)
	name = clean
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasPrefix(name, "assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	c.Data(http.StatusOK, contentType, data)
}

// rateLimiter 是基于令牌桶的内存限流器，按 key 维护少量状态。
type rateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]*rateBucket
	maxKeys     int
	lastSweep   time.Time
	sweepPeriod time.Duration
}

type rateBucket struct {
	tokens float64
	last   time.Time
	window time.Duration
}

const (
	defaultLimiterMaxKeys = 10000
	defaultLimiterSweep   = time.Minute
)

func newRateLimiter(maxKeys int) *rateLimiter {
	if maxKeys <= 0 {
		maxKeys = defaultLimiterMaxKeys
	}
	return &rateLimiter{
		buckets:     make(map[string]*rateBucket),
		maxKeys:     maxKeys,
		lastSweep:   time.Now(),
		sweepPeriod: defaultLimiterSweep,
	}
}

// Allow 尝试从 key 对应的令牌桶中取一个令牌。
// max 为窗口内允许的次数，window 为窗口长度；返回 false 时 wait 为建议的重试等待时间。
func (l *rateLimiter) Allow(key string, max int, window time.Duration) (bool, time.Duration) {
	if max <= 0 || window <= 0 {
		return true, 0
	}
	now := time.Now()
	rate := float64(max) / window.Seconds()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.buckets == nil {
		l.buckets = make(map[string]*rateBucket)
	}
	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.evictLocked(now)
		}
		b = &rateBucket{tokens: float64(max), last: now, window: window}
		l.buckets[key] = b
	} else {
		b.tokens = math.Min(float64(max), b.tokens+now.Sub(b.last).Seconds()*rate)
		b.last = now
		b.window = window
	}

	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait
	}
	b.tokens--
	return true, 0
}

// Reset 清除某个 key 的限流状态，通常用于登录成功后恢复配额。
func (l *rateLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// sweepLocked 定期删除长时间空闲的 key，避免 map 无限增长。
func (l *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.sweepPeriod {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if now.Sub(b.last) > 2*b.window {
			delete(l.buckets, key)
		}
	}
}

// evictLocked 在达到容量上限时清理过期项；仍然超限则删除最旧的一项。
func (l *rateLimiter) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, b := range l.buckets {
		if now.Sub(b.last) > 2*b.window {
			delete(l.buckets, key)
			continue
		}
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = key, b.last
		}
	}
	if len(l.buckets) >= l.maxKeys && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
