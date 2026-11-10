// Package api 提供 HTTP 接口与前端静态资源服务。
package api

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/backup"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
)

// Server 持有接口层依赖。
type Server struct {
	Store          *store.Store
	Auth           *auth.Manager
	Manager        *service.Manager
	Backup         *backup.Manager
	WebFS          fs.FS
	TrustedProxies []string
	ACMEBaseURL    string
	// Restart 由 main 注入，用于在恢复备份后触发优雅退出，让进程管理器拉起新进程。
	Restart   func()
	entry     atomic.Value
	limiter   *rateLimiter
	startedAt time.Time
}

// New 创建 API 服务。
func New(st *store.Store, a *auth.Manager, mgr *service.Manager, entry string, webFS fs.FS, trustedProxies []string) *Server {
	s := &Server{
		Store:          st,
		Auth:           a,
		Manager:        mgr,
		WebFS:          webFS,
		TrustedProxies: trustedProxies,
		limiter:        newRateLimiter(defaultLimiterMaxKeys),
		startedAt:      time.Now(),
	}
	s.entry.Store(entry)
	return s
}

// Entry 返回当前安全入口。
func (s *Server) Entry() string {
	return s.entry.Load().(string)
}

// SetEntry 更新安全入口。
func (s *Server) SetEntry(entry string) {
	s.entry.Store(entry)
}

// Router 构建 Gin 路由。
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	// 仅信任显式配置的反向代理；为空时 ClientIP 取 RemoteAddr，避免 X-Forwarded-For 伪造。
	if err := r.SetTrustedProxies(s.TrustedProxies); err != nil {
		log.Printf("可信代理配置无效，已禁用代理头信任: %v", err)
		_ = r.SetTrustedProxies(nil)
	}
	r.Use(gin.Recovery(), s.localeMiddleware(), s.securityHeaders(), s.cors())

	api := r.Group("/:entry/api")
	api.Use(s.entryMiddleware())
	{
		api.GET("/setup/status", s.setupStatus)
		api.POST("/setup", s.handleSetup)
		api.POST("/setup/restore", s.handleSetupRestore)
		api.POST("/auth/login", s.handleLogin)
		api.POST("/auth/logout", s.handleLogout)
		api.POST("/auth/forgot", s.handleForgotPassword)
		api.POST("/auth/reset", s.handleResetPassword)
		api.GET("/auth/reset/validate", s.handleValidateReset)
	}

	protected := r.Group("/:entry/api")
	protected.Use(s.entryMiddleware(), s.authMiddleware())
	{
		protected.GET("/auth/me", s.handleMe)
		protected.POST("/auth/change-password", s.handleChangePassword)
		protected.POST("/auth/change-email", s.handleChangeEmail)
		protected.POST("/auth/totp/setup", s.handleTOTPSetup)
		protected.POST("/auth/totp/enable", s.handleTOTPEnable)
		protected.POST("/auth/totp/disable", s.handleTOTPDisable)

		protected.GET("/sessions", s.listSessions)
		protected.DELETE("/sessions/:id", s.revokeSession)
		protected.POST("/sessions/revoke-others", s.revokeOtherSessions)

		protected.GET("/meta", s.handleMeta)
		protected.GET("/status", s.handleStatus)
		protected.GET("/dashboard", s.handleDashboard)

		protected.GET("/ca-configs", s.caConfigs)
		protected.PUT("/ca-configs/:value", s.updateCAConfig)

		protected.GET("/private-cas", s.listPrivateCAs)
		protected.POST("/private-cas", s.createPrivateCA)
		protected.POST("/private-cas/import", s.importPrivateCA)
		protected.GET("/private-cas/:id/download", s.downloadPrivateCA)
		protected.DELETE("/private-cas/:id", s.deletePrivateCA)

		protected.GET("/acme-endpoints", s.listACMEEndpoints)
		protected.POST("/acme-endpoints", s.createACMEEndpoint)
		protected.PUT("/acme-endpoints/:id", s.updateACMEEndpoint)
		protected.DELETE("/acme-endpoints/:id", s.deleteACMEEndpoint)
		protected.GET("/acme-endpoints/:id/eab", s.listACMEEABCredentials)
		protected.POST("/acme-endpoints/:id/eab", s.createACMEEABCredential)
		protected.DELETE("/acme-eab/:id", s.deleteACMEEABCredential)
		protected.GET("/acme-accounts", s.listACMEAccounts)
		protected.GET("/acme-orders", s.listACMEOrders)
		protected.GET("/acme-orders/:id", s.getACMEOrder)
		protected.POST("/acme-diagnostics/cleanup", s.cleanupACMEDiagnostics)
		protected.POST("/acme-diagnostics/cleanup/preview", s.previewCleanupACMEDiagnostics)

		protected.GET("/dns-accounts", s.listDNSAccounts)
		protected.GET("/dns-accounts/:id", s.getDNSAccount)
		protected.POST("/dns-accounts", s.createDNSAccount)
		protected.PUT("/dns-accounts/:id", s.updateDNSAccount)
		protected.DELETE("/dns-accounts/:id", s.deleteDNSAccount)

		protected.GET("/deploy-accounts", s.listDeployAccounts)
		protected.GET("/deploy-accounts/:id", s.getDeployAccount)
		protected.POST("/deploy-accounts", s.createDeployAccount)
		protected.PUT("/deploy-accounts/:id", s.updateDeployAccount)
		protected.DELETE("/deploy-accounts/:id", s.deleteDeployAccount)

		protected.GET("/tasks", s.listTasks)
		protected.POST("/tasks", s.createTask)
		protected.PUT("/tasks/:id", s.updateTask)
		protected.DELETE("/tasks/:id", s.deleteTask)
		protected.POST("/tasks/:id/run", s.runTask)
		protected.POST("/tasks/batch/run", s.batchRunTasks)
		protected.POST("/tasks/batch/enabled", s.batchSetTasksEnabled)
		protected.GET("/tasks/:id/logs", s.taskLogs)

		protected.GET("/certificates", s.listCertificates)
		protected.POST("/certificates/cleanup", s.cleanupCertificates)
		protected.POST("/certificates/cleanup/preview", s.previewCleanupCertificates)
		protected.GET("/certificates/:id", s.getCertificate)
		protected.GET("/certificates/:id/analysis", s.analyzeCertificate)
		protected.GET("/certificates/:id/download", s.downloadCertificate)
		protected.POST("/certificates/:id/deploy", s.deployCertificate)
		protected.DELETE("/certificates/:id", s.deleteCertificate)

		protected.GET("/deployments", s.listDeployments)

		protected.GET("/monitors", s.listMonitors)
		protected.POST("/monitors", s.createMonitor)
		protected.PUT("/monitors/:id", s.updateMonitor)
		protected.DELETE("/monitors/:id", s.deleteMonitor)
		protected.POST("/monitors/:id/check", s.checkMonitor)

		protected.GET("/logs", s.listLogs)
		protected.GET("/settings", s.getSettings)
		protected.PUT("/settings", s.updateSettings)
		protected.GET("/settings/backup", s.backupInfo)
		protected.GET("/settings/backup/download", s.downloadBackup)
		protected.POST("/settings/backup/download", s.downloadBackup)
		protected.POST("/settings/backup/restore", s.restoreBackup)
		protected.POST("/settings/test-mail", s.testMail)
		protected.POST("/settings/test-gotify", s.testGotify)
		protected.POST("/settings/test-serverchan", s.testServerChan)
		protected.POST("/settings/test-channel", s.testChannel)
		protected.POST("/security/entry", s.changeEntry)
	}

	r.NoRoute(s.serveSPA)
	return r
}

// localeMiddleware 根据请求头协商语言，供所有失败消息与持久化消息渲染使用。
func (s *Server) localeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(i18n.ContextKey, i18n.FromRequest(c.Request))
		c.Next()
	}
}

// securityHeaders 添加基础安全响应头。
func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// 接口响应和页面包含登录态与安全入口，默认禁止缓存；静态资源会覆盖此值。
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

func (s *Server) entryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if subtle.ConstantTimeCompare([]byte(c.Param("entry")), []byte(s.Entry())) != 1 {
			// 入口错误不影响正常请求，仅对失败尝试做宽松限流，防止扫描器消耗资源。
			if allowed, wait := s.limiter.Allow("entry:"+clientRateKey(c), 60, time.Minute); !allowed {
				rateLimited(c, wait)
				return
			}
			failKey(c, http.StatusNotFound, "api.error.page_not_found")
			return
		}
		c.Next()
	}
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

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, fromHeader := s.sessionToken(c)
		if token == "" {
			failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
			return
		}
		// Cookie 会随跨站请求自动携带，因此状态变更请求必须带自定义头；
		// 显式使用 Authorization: Bearer 的 API 调用不存在 CSRF 问题。
		if !fromHeader && requiresCSRF(c.Request.Method) && c.GetHeader(csrfHeaderName) == "" {
			failKey(c, http.StatusForbidden, "api.error.unauthorized")
			return
		}
		claims, err := s.Auth.Parse(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": err.Error()})
			return
		}
		user, err := s.Store.GetUserByID(claims.UserID)
		if err != nil || user.TokenVersion != claims.Version {
			failKey(c, http.StatusUnauthorized, "api.error.session_expired")
			return
		}
		sessionID, err := s.ensureSession(c, claims, token)
		if errors.Is(err, errSessionInvalid) {
			failKey(c, http.StatusUnauthorized, "api.error.session_expired")
			return
		}
		if err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Set("user", user)
		c.Set("session_id", sessionID)
		c.Next()
	}
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
	c.JSON(status, gin.H{"success": false, "message": message})
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
	c.JSON(status, gin.H{
		"success": false,
		"code":    key,
		"message": i18n.T(locale(c), key, params),
		"params":  params,
	})
}

// failErr 优先使用可翻译错误，否则回退到错误原文。
func failErr(c *gin.Context, status int, err error) {
	if localized, ok := err.(*i18n.Error); ok && localized.Key != "" {
		failKey(c, status, localized.Key, pairsOf(localized.Params)...)
		return
	}
	fail(c, status, err.Error())
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
// 访问安全入口只做一次门禁：写入短期 Cookie 后把地址栏切到裸 /login。
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
	if p == "/"+entry+"/api" || strings.HasPrefix(p, "/"+entry+"/api/") {
		failKey(c, http.StatusNotFound, "api.error.api_not_found")
		return
	}
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		failKey(c, http.StatusNotFound, "api.error.api_not_found")
		return
	}

	// 访问安全入口：写入门禁 Cookie，再跳转到裸登录页。
	if p == "/"+entry || p == "/"+entry+"/" {
		s.setEntryCookie(c)
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// 兼容旧入口下的链接，例如 /<entry>/reset?token=...。
	if strings.HasPrefix(p, "/"+entry+"/") {
		s.setEntryCookie(c)
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

	// 登录、安装和密码重置页面：必须先在安全入口拿到门禁 Cookie。
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

	// 未登录且未通过安全入口：按原始路径直接返回 404，不做重定向，
	// 避免暴露登录页的存在，也避免 /login 在无门禁 Cookie 时二次 404。
	s.entryNotFound(c)
}

// entryNotFound 统一处理未通过入口门禁的请求，并计入失败限流。
func (s *Server) entryNotFound(c *gin.Context) {
	if allowed, wait := s.limiter.Allow("entry:"+clientRateKey(c), 60, time.Minute); !allowed {
		rateLimited(c, wait)
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
	// 把当前安全入口注入页面：登录页和已登录页面都通过它拼接口地址。
	entry := s.Entry()
	inject := fmt.Sprintf(
		`<script>window.__LACTSSL_ENTRY__=%q;window.__LACTSSL_API_BASE__=%q;</script>`,
		entry, "/"+entry+"/api",
	)
	if bytes.Contains(data, []byte("</head>")) {
		data = bytes.Replace(data, []byte("</head>"), []byte(inject+"</head>"), 1)
	}
	// 页面包含运行时注入的安全入口，且是否可访问与登录态相关，禁止缓存。
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) serveAsset(c *gin.Context, name string) {
	name = path.Clean(name)
	if strings.Contains(name, "..") {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := fs.ReadFile(s.WebFS, name)
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
