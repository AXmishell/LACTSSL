package api

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// JWT 仍作为会话令牌，但浏览器通过 HttpOnly Cookie 携带。
// 这样服务端才能在 HTML 导航阶段校验登录状态，而不只依赖前端路由守卫。
const (
	sessionCookieName = "lactssl_session"
	entryCookieName   = "lactssl_entry"
	csrfCookieName    = "lactssl_csrf"
	deviceCookieName  = "lactssl_device"
	// hostCookiePrefix 在 HTTPS 下启用，防止共享父域下的同名 Cookie 覆盖。
	hostCookiePrefix = "__Host-"
	csrfHeaderName   = "X-LACTSSL-CSRF"
	entryGatePurpose = "entry-gate"
	csrfTokenPurpose = "csrf"
	entryCookieTTL   = 24 * time.Hour
	// csrfTokenLength 是双提交 CSRF 令牌的字符数（128 位十六进制）。
	csrfTokenLength = 32
	// deviceCookieValueLength 是设备标识的字符数（16 字节随机数的十六进制表示）。
	deviceCookieValueLength = 32
	deviceCookieTTL         = 365 * 24 * time.Hour
)

// sessionCookieSecure 在 TLS 或可信反代声明 HTTPS 时给 Cookie 加 Secure。
func (s *Server) sessionCookieSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return s.isTrustedProxy(c.Request.RemoteAddr) && strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// isTrustedProxy 判断请求是否来自配置的可信反向代理，只有可信代理的转发头才会被采信。
func (s *Server) isTrustedProxy(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	for _, entry := range s.TrustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if proxy := net.ParseIP(entry); proxy != nil && proxy.Equal(ip) {
			return true
		}
	}
	return false
}

// cookieName 在 HTTPS 下给 Cookie 加 __Host- 前缀；HTTP 环境保持原名，
// 避免浏览器因缺少 Secure 属性拒绝 __Host- Cookie。
func (s *Server) cookieName(base string, secure bool) string {
	if secure {
		return hostCookiePrefix + base
	}
	return base
}

// readCookie 优先读取 __Host- 前缀 Cookie，兼容明文 HTTP 下的旧名称。
func (s *Server) readCookie(c *gin.Context, base string) (string, error) {
	if value, err := c.Cookie(hostCookiePrefix + base); err == nil && value != "" {
		return value, nil
	}
	return c.Cookie(base)
}

// setSessionCookie 写入会话 Cookie。Path 设为根路径，保证裸页面和入口下的接口都能携带。
func (s *Server) setSessionCookie(c *gin.Context, token string, expire time.Time) {
	secure := s.sessionCookieSecure(c)
	if secure {
		// 升级或切换到 HTTPS 后，浏览器可能同时保留明文时代的旧名称 Cookie。
		// 服务端优先读取 __Host-，这里同步删除旧名称，避免两份会话并存。
		s.expireLegacyCookie(c, sessionCookieName, true)
	}
	http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
		Name:     s.cookieName(sessionCookieName, secure),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expire,
		MaxAge:   int(time.Until(expire).Seconds()),
	})
}

// expireLegacyCookie 删除不带前缀的同名旧 Cookie。
// 只在写入 __Host- Cookie 时调用：旧名称不再有存在价值，留着会让前端
// 的 document.cookie 顺序不可控。
func (s *Server) expireLegacyCookie(c *gin.Context, base string, httpOnly bool) {
	http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
		Name:     base,
		Value:    "",
		Path:     "/",
		HttpOnly: httpOnly,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// dropShadowedLegacyCookie 在 __Host- Cookie 已存在时删除并存的旧名称 Cookie。
// 浏览器不会因为新旧名称并存就丢弃旧值，而 document.cookie 的顺序不保证，
// 旧 CSRF 值可能被前端优先读到，造成登录状态下所有写操作提示“请先登录”。
func (s *Server) dropShadowedLegacyCookie(c *gin.Context, base string, httpOnly bool) {
	if _, err := c.Cookie(hostCookiePrefix + base); err != nil {
		return
	}
	if _, err := c.Cookie(base); err != nil {
		return
	}
	s.expireLegacyCookie(c, base, httpOnly)
}

// clearSessionCookie 清除会话 Cookie（同时清理带与不带 __Host- 前缀的名称）。
func (s *Server) clearSessionCookie(c *gin.Context) {
	for _, name := range []string{sessionCookieName, hostCookiePrefix + sessionCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   s.sessionCookieSecure(c),
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

// setCSRFCookie 下发双提交 CSRF Cookie。值由会话令牌派生，前端读取后放入请求头。
func (s *Server) setCSRFCookie(c *gin.Context, token string, expire time.Time) {
	secure := s.sessionCookieSecure(c)
	if secure {
		// 新值写入 __Host- 后，旧名称的 CSRF Cookie 必须删除；否则前端可能读到旧值。
		s.expireLegacyCookie(c, csrfCookieName, false)
	}
	http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
		Name:     s.cookieName(csrfCookieName, secure),
		Value:    s.Auth.SignValueLong(csrfTokenPurpose, token, csrfTokenLength),
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expire,
		MaxAge:   int(time.Until(expire).Seconds()),
	})
}

// ensureDeviceCookie 在首次访问或登录时下发设备标识；已存在时保持不变，
// 使同一浏览器在多次登录之间仍能保持稳定的设备标识。
func (s *Server) ensureDeviceCookie(c *gin.Context) {
	if value, err := s.readCookie(c, deviceCookieName); err == nil && len(value) == deviceCookieValueLength {
		return
	}
	value, err := util.RandomHex(deviceCookieValueLength / 2)
	if err != nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
		Name:     s.cookieName(deviceCookieName, s.sessionCookieSecure(c)),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.sessionCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(deviceCookieTTL),
		MaxAge:   int(deviceCookieTTL.Seconds()),
	})
}

// clearCSRFCookie 清除 CSRF Cookie（同时清理带与不带 __Host- 前缀的名称）。
func (s *Server) clearCSRFCookie(c *gin.Context) {
	for _, name := range []string{csrfCookieName, hostCookiePrefix + csrfCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
			Name:     name,
			Value:    "",
			Path:     "/",
			Secure:   s.sessionCookieSecure(c),
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

// validCSRF 严格校验双提交 CSRF 令牌；Cookie 缺失时不再退化为“只看请求头”。
// __Host- 与非前缀 Cookie 可能因部署方式切换而同时存在，请求头只要与其中
// 任意一个真实令牌一致即可，避免旧页面或旧 Cookie 让写操作误报“请先登录”。
func (s *Server) validCSRF(c *gin.Context) bool {
	header := c.GetHeader(csrfHeaderName)
	if header == "" {
		return false
	}
	matched := 0
	// 不提前返回，始终比较两种名称，避免通过响应耗时判断命中的 Cookie。
	for _, name := range []string{hostCookiePrefix + csrfCookieName, csrfCookieName} {
		cookie, err := c.Cookie(name)
		if err != nil || cookie == "" {
			continue
		}
		matched |= subtle.ConstantTimeCompare([]byte(header), []byte(cookie))
	}
	return matched == 1
}

// setEntryCookie 写入一次性门禁 Cookie。只有先访问过门禁入口，才能打开裸登录页。
// 签名绑定当前入口，入口变更后旧 Cookie 自动失效。
func (s *Server) setEntryCookie(c *gin.Context) {
	expires := time.Now().Add(entryCookieTTL)
	http.SetCookie(c.Writer, &http.Cookie{ // #nosec G124 -- Secure 由 sessionCookieSecure 按 TLS/可信代理动态设置，HttpOnly 与 SameSite 已固定
		Name:     s.cookieName(entryCookieName, s.sessionCookieSecure(c)),
		Value:    s.Auth.SignExpiringValue(entryGatePurpose, s.Entry(), expires),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.sessionCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(entryCookieTTL.Seconds()),
	})
}

// hasEntryCookie 校验请求是否携带有效的入口门禁 Cookie（含服务端过期时间）。
func (s *Server) hasEntryCookie(c *gin.Context) bool {
	value, err := s.readCookie(c, entryCookieName)
	if err != nil || value == "" {
		return false
	}
	return s.Auth.VerifyExpiringValue(entryGatePurpose, s.Entry(), value)
}

// sessionIDForToken 返回令牌对应的服务端会话 ID。
// 旧版令牌没有 jti，用令牌摘要作为稳定 ID，避免升级后立即掉线。
func sessionIDForToken(claims *auth.Claims, token string) string {
	if claims != nil && strings.TrimSpace(claims.ID) != "" {
		return claims.ID
	}
	return "legacy-" + util.HashToken(token)[:32]
}

// errSessionInvalid 表示会话已撤销、过期或不属于当前用户。
var errSessionInvalid = errors.New("session invalid")

// ensureSession 校验令牌对应的服务端会话，必要时补登记旧版令牌。
func (s *Server) ensureSession(c *gin.Context, claims *auth.Claims, token string) (string, *model.Session, error) {
	id := sessionIDForToken(claims, token)
	now := time.Now().UTC()
	session, err := s.Store.GetSession(id)
	if errors.Is(err, sql.ErrNoRows) {
		// 只有旧版没有 jti 的令牌才允许补登记，避免升级后立即掉线。
		// 新令牌缺失服务端记录说明会话已被撤销或随重启清理，
		// 必须拒绝，不能凭一个仍然有效的 JWT 自动复活会话。
		if claims.ID != "" {
			return "", nil, errSessionInvalid
		}
		if claims.ExpiresAt == nil {
			return "", nil, errSessionInvalid
		}
		created := &model.Session{
			ID:         id,
			UserID:     claims.UserID,
			IP:         c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			CreatedAt:  now,
			LastSeenAt: now,
			ExpiresAt:  claims.ExpiresAt.Time.UTC(),
		}
		if err := s.Store.CreateSession(created); err != nil {
			return "", nil, err
		}
		return id, created, nil
	}
	if err != nil {
		return "", nil, err
	}
	if session.UserID != claims.UserID || session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
		return "", nil, errSessionInvalid
	}
	_ = s.Store.TouchSession(id, now, now.Add(-time.Minute))
	return id, session, nil
}

// sessionToken 依次从 Authorization 头和会话 Cookie 取令牌。
// fromHeader 为 true 表示调用方显式携带 Bearer 令牌，不受 CSRF 校验约束。
func (s *Server) sessionToken(c *gin.Context) (token string, fromHeader bool) {
	if header := c.GetHeader("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), true
	}
	if value, err := s.readCookie(c, sessionCookieName); err == nil {
		return value, false
	}
	return "", false
}

// sessionUser 解析当前请求的登录用户，失败返回 nil。仅用于页面级鉴权。
func (s *Server) sessionUser(c *gin.Context) *model.User {
	token, _ := s.sessionToken(c)
	if token == "" {
		return nil
	}
	claims, err := s.Auth.Parse(token)
	if err != nil {
		return nil
	}
	user, err := s.Store.GetUserByID(claims.UserID)
	if err != nil || user.TokenVersion != claims.Version {
		return nil
	}
	// 页面级鉴权同样需要尊重服务端会话撤销，避免已撤销令牌仍能加载控制台外壳。
	session, err := s.Store.GetSession(sessionIDForToken(claims, token))
	if err != nil || session.UserID != user.ID || session.RevokedAt != nil || !time.Now().UTC().Before(session.ExpiresAt) {
		return nil
	}
	return user
}

// requiresCSRF 判断该请求方法是否会改变服务端状态。
func requiresCSRF(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
