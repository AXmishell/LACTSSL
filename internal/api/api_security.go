package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/ratelimit"
)

// publicAPIRoutes 是允许匿名访问的接口。新增接口默认需要认证，
// 只有显式登记到这里的接口才会跳过会话/API 密钥校验。
// 这些入口依赖 originGuard（登录、初始化、找回密码）或一次性初始化令牌。
var publicAPIRoutes = map[string]bool{
	"GET /api/setup/status":               true,
	"GET /api/setup/config":               true,
	"PUT /api/setup/config":               true,
	"POST /api/setup/config/restart":      true,
	"POST /api/setup":                     true,
	"POST /api/setup/restore":             true,
	"POST /api/auth/login":                true,
	"POST /api/auth/logout":               true,
	"POST /api/auth/forgot":               true,
	"POST /api/auth/reset":                true,
	"GET /api/auth/reset/validate":        true,
	"GET /api/auth/passkey/available":     true,
	"POST /api/auth/passkey/login/begin":  true,
	"POST /api/auth/passkey/login/finish": true,
}

// normalizeAPIPath 把旧版 /<entry>/api 路由模板统一成 /api，
// 使匿名白名单与 API 密钥权限表在两条路径下共用同一份定义。
func normalizeAPIPath(route string) string {
	return strings.Replace(route, "/:entry/api", "/api", 1)
}

// apiRouteKey 返回用于查表的「方法 + 路由模板」。
func apiRouteKey(c *gin.Context) string {
	return c.Request.Method + " " + normalizeAPIPath(c.FullPath())
}

func isPublicAPIRoute(c *gin.Context) bool {
	return publicAPIRoutes[apiRouteKey(c)]
}

// defaultAuth 是 /api 的默认认证层：未登记为匿名接口的路由一律要求
// 登录会话或 API 密钥，新增接口不会因为忘记挂中间件而裸奔。
func (s *Server) defaultAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicAPIRoute(c) {
			c.Next()
			return
		}
		if !s.authenticate(c) {
			return
		}
		if !s.authorizeAPIKey(c) {
			return
		}
		c.Next()
	}
}

// csrfGuard 对非匿名接口的状态变更请求默认校验双提交 CSRF。
// 匿名入口（登录、初始化、找回密码等）由 originGuard 防御登录 CSRF；
// 退出登录在处理器内部单独校验。
func (s *Server) csrfGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requiresCSRF(c.Request.Method) || isPublicAPIRoute(c) {
			c.Next()
			return
		}
		// API 密钥与显式 Bearer 调用不依赖 Cookie，不存在 CSRF 问题。
		if _, fromHeader := s.sessionToken(c); fromHeader {
			c.Next()
			return
		}
		if strings.TrimSpace(c.GetHeader(apiKeyHeader)) != "" {
			c.Next()
			return
		}
		token, _ := s.sessionToken(c)
		if token == "" {
			// 无凭据请求交给 defaultAuth 统一返回 401，避免把认证失败伪装成 CSRF 错误。
			c.Next()
			return
		}
		if !s.validCSRF(c) {
			s.logger().Warn("csrf validation failed",
				"request_id", c.GetString(requestIDKey),
				"path", s.logPath(c),
				"header_present", strings.TrimSpace(c.GetHeader(csrfHeaderName)) != "",
				"legacy_cookie", cookiePresent(c, csrfCookieName),
				"host_cookie", cookiePresent(c, hostCookiePrefix+csrfCookieName),
				"sec_fetch_site", c.GetHeader("Sec-Fetch-Site"),
				"origin", c.GetHeader("Origin"),
				"host", c.Request.Host,
			)
			failKey(c, http.StatusForbidden, "api.error.unauthorized")
			return
		}
		c.Next()
	}
}

// apiRateLimit 是 /api 的默认限流层：所有请求先过按 IP 的总量配额，
// 完全未携带凭据的请求再叠加更严格的匿名配额。安全入口不再出现在接口路径中，
// 裸 /api 会直接面对扫描器；登录接口在处理器内还有更严格的 IP 与账户维度限流。
func (s *Server) apiRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowed, wait := s.allow(ratelimit.APIIP, "api-ip:"+s.clientRateKey(c)); !allowed {
			rateLimited(c, wait)
			return
		}
		if token, _ := s.sessionToken(c); token == "" && strings.TrimSpace(c.GetHeader(apiKeyHeader)) == "" {
			if allowed, wait := s.allow(ratelimit.APIAnonymous, "anon-api:"+s.clientRateKey(c)); !allowed {
				rateLimited(c, wait)
				return
			}
		}
		c.Next()
	}
}

// cookiePresent 判断请求是否携带指定名称的 Cookie，仅用于诊断日志，不输出值。
func cookiePresent(c *gin.Context, name string) bool {
	value, err := c.Cookie(name)
	return err == nil && value != ""
}

// entryMiddleware 仅用于旧版 /<entry>/api 兼容路径：入口错误时拒绝执行，
// 并对失败尝试做宽松限流，防止扫描器通过旧路径暴力枚举入口。
func (s *Server) entryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if subtle.ConstantTimeCompare([]byte(c.Param("entry")), []byte(s.Entry())) == 1 {
			c.Next()
			return
		}
		if allowed, wait := s.allow(ratelimit.Entry, "entry:"+s.clientRateKey(c)); !allowed {
			rateLimited(c, wait)
			return
		}
		failKey(c, http.StatusNotFound, "api.error.page_not_found")
	}
}
