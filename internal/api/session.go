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
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// JWT 仍作为会话令牌，但浏览器通过 HttpOnly Cookie 携带。
// 这样服务端才能在 HTML 导航阶段校验登录状态，而不只依赖前端路由守卫。
const (
	sessionCookieName = "lactssl_session"
	entryCookieName   = "lactssl_entry"
	csrfCookieName    = "lactssl_csrf"
	// hostCookiePrefix 在 HTTPS 下启用，防止共享父域下的同名 Cookie 覆盖。
	hostCookiePrefix = "__Host-"
	csrfHeaderName   = "X-LACTSSL-CSRF"
	entryGatePurpose = "entry-gate"
	csrfTokenPurpose = "csrf"
	entryCookieTTL   = 24 * time.Hour
	// reauthTTL 是高风险操作重新认证的有效期。
	reauthTTL = 5 * time.Minute
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
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.cookieName(sessionCookieName, s.sessionCookieSecure(c)),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.sessionCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
		Expires:  expire,
		MaxAge:   int(time.Until(expire).Seconds()),
	})
}

// clearSessionCookie 清除会话 Cookie（同时清理带与不带 __Host- 前缀的名称）。
func (s *Server) clearSessionCookie(c *gin.Context) {
	for _, name := range []string{sessionCookieName, hostCookiePrefix + sessionCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{
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
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.cookieName(csrfCookieName, s.sessionCookieSecure(c)),
		Value:    s.Auth.SignValue(csrfTokenPurpose, token),
		Path:     "/",
		HttpOnly: false,
		Secure:   s.sessionCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
		Expires:  expire,
		MaxAge:   int(time.Until(expire).Seconds()),
	})
}

// clearCSRFCookie 清除 CSRF Cookie（同时清理带与不带 __Host- 前缀的名称）。
func (s *Server) clearCSRFCookie(c *gin.Context) {
	for _, name := range []string{csrfCookieName, hostCookiePrefix + csrfCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{
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
func (s *Server) validCSRF(c *gin.Context) bool {
	header := c.GetHeader(csrfHeaderName)
	if header == "" {
		return false
	}
	cookie, err := s.readCookie(c, csrfCookieName)
	if err != nil || cookie == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) == 1
}

// setEntryCookie 写入一次性门禁 Cookie。只有先访问过安全入口，才能打开裸登录页。
// 签名绑定当前入口，入口变更后旧 Cookie 自动失效。
func (s *Server) setEntryCookie(c *gin.Context) {
	expires := time.Now().Add(entryCookieTTL)
	http.SetCookie(c.Writer, &http.Cookie{
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

// markReauth 记录当前会话在最近通过账户密码重新认证。
func (s *Server) markReauth(sessionID string) {
	if sessionID == "" {
		return
	}
	now := time.Now()
	s.reauthMu.Lock()
	defer s.reauthMu.Unlock()
	if s.reauth == nil {
		s.reauth = make(map[string]time.Time)
	}
	// 顺手清理过期条目，避免 map 随会话增长。
	for id, expires := range s.reauth {
		if !now.Before(expires) {
			delete(s.reauth, id)
		}
	}
	s.reauth[sessionID] = now.Add(reauthTTL)
}

// hasReauth 判断当前会话是否仍在重新认证有效期内。
func (s *Server) hasReauth(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	s.reauthMu.Lock()
	defer s.reauthMu.Unlock()
	expires, ok := s.reauth[sessionID]
	if !ok {
		return false
	}
	if !time.Now().Before(expires) {
		delete(s.reauth, sessionID)
		return false
	}
	return true
}

// requireReauth 是旧版重新认证窗口的兼容辅助函数。
// 当前敏感操作已改用二次验证 token，新的业务接口不再依赖该窗口。
func (s *Server) requireReauth(c *gin.Context) bool {
	if s.hasReauth(currentSessionID(c)) {
		return true
	}
	c.Abort()
	c.JSON(http.StatusForbidden, gin.H{
		"success":         false,
		"code":            "api.error.reauth_required",
		"message":         i18n.T(locale(c), "api.error.reauth_required", nil),
		"reauth_required": true,
	})
	return false
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
