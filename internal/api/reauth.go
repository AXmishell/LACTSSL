package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
)

const (
	// reauthElevationTTL 是一次成功再认证后，同会话内免重复输入密码/TOTP 的时长。
	// 高风险账户操作仍使用 requireReauthFresh，始终要求现场验证。
	reauthElevationTTL = 5 * time.Minute
	// sessionReauthUntilKey 缓存本次请求对应的会话再认证有效期。
	sessionReauthUntilKey = "session_reauth_until"
)

// requireSessionReauth 是交互式再认证的统一入口，仅允许浏览器会话调用。
// API 密钥没有密码/TOTP，不能完成再认证；需要机器身份的接口应使用 scope
// 或 requireReauthElevated（scope 已授权时直接放行）。
func (s *Server) requireSessionReauth(c *gin.Context, user *model.User, password, totpCode string) bool {
	return s.requireReauth(c, user, password, totpCode)
}

// requireReauth 校验敏感操作前的再认证信息。
// 已处于再认证有效期内的会话直接通过；否则启用 TOTP 时必须同时提供
// 当前密码和动态验证码，未启用 TOTP 时校验当前密码。
func (s *Server) requireReauth(c *gin.Context, user *model.User, password, totpCode string) bool {
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return false
	}
	if apiKeyFromContext(c) != nil {
		failKey(c, http.StatusForbidden, "api.error.reauth_session_required")
		return false
	}
	if s.sessionReauthValid(c) {
		return true
	}
	return s.verifyReauth(c, user, password, totpCode)
}

// requireReauthFresh 始终要求现场验证，不接受会话级再认证有效期。
// 用于改密、改邮箱、TOTP、通行密钥等直接决定账户控制权的操作。
func (s *Server) requireReauthFresh(c *gin.Context, user *model.User, password, totpCode string) bool {
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return false
	}
	if apiKeyFromContext(c) != nil {
		failKey(c, http.StatusForbidden, "api.error.reauth_session_required")
		return false
	}
	return s.verifyReauth(c, user, password, totpCode)
}

// requireReauthElevated 用于已由 scope 授权的机器身份或已提权的浏览器会话。
// API 密钥直接通过（由调用方先做 requireAPIKeyScope）；浏览器会话必须有
// 有效的再认证有效期，否则返回 reauth_required 让前端弹出统一的再认证框。
func (s *Server) requireReauthElevated(c *gin.Context) bool {
	if s.sessionReauthValid(c) || apiKeyFromContext(c) != nil {
		return true
	}
	if getUser(c) == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return false
	}
	failReauthRequired(c)
	return false
}

// verifyReauth 校验凭据并在成功后写入会话级再认证有效期。
func (s *Server) verifyReauth(c *gin.Context, user *model.User, password, totpCode string) bool {
	// 串行化校验与提权落库：并发提交同一个验证码时，后一个请求在
	// 第一个请求完成提权后可以通过，而不是误报“验证码已被使用”。
	s.reauthMu.Lock()
	defer s.reauthMu.Unlock()

	key := "reauth:" + strconv.FormatInt(user.ID, 10)
	if user.TOTPEnabled {
		// 先校验密码，避免密码错误时消费掉一个有效的 TOTP 验证码。
		if !auth.VerifyPassword(user.PasswordHash, password) {
			return s.reauthFailure(c, key, "api.error.current_password_wrong")
		}
		switch auth.ValidateTOTPPurpose(user.TOTPSecret, totpCode, auth.TOTPPurposeReauth) {
		case auth.TOTPValid:
			// 继续下发提权有效期。
		case auth.TOTPReplayed:
			// 同一验证码可能来自并发/重复提交。如果当前会话已经由
			// 另一个请求提权，则直接视为成功。
			if s.sessionReauthValidInStore(c) {
				return true
			}
			failKey(c, http.StatusBadRequest, "api.error.totp_reused")
			return false
		default:
			return s.reauthFailure(c, key, "api.error.totp_invalid")
		}
	} else if !auth.VerifyPassword(user.PasswordHash, password) {
		return s.reauthFailure(c, key, "api.error.current_password_wrong")
	}

	s.limiter.Reset(key)
	s.grantReauth(c)
	return true
}

// sessionReauthValidInStore 从数据库重新读取当前会话的再认证有效期。
// 用于并发请求中后到者的补偿判断，避免只依赖本请求开始时的上下文快照。
func (s *Server) sessionReauthValidInStore(c *gin.Context) bool {
	id := currentSessionID(c)
	if id == "" {
		return false
	}
	session, err := s.Store.GetSession(id)
	if err != nil || session == nil || session.ReauthUntil == nil {
		return false
	}
	return time.Now().UTC().Before(*session.ReauthUntil)
}

// reauthFailure 记录失败并返回统一错误；成功路径不消耗失败额度。
func (s *Server) reauthFailure(c *gin.Context, key, messageKey string) bool {
	if allowed, wait := s.allow(ratelimit.Reauth, key); !allowed {
		rateLimited(c, wait)
		return false
	}
	failKey(c, http.StatusBadRequest, messageKey)
	return false
}

// grantReauth 在当前会话上记录再认证有效期。写库失败只记录日志，
// 不阻塞已经通过凭据校验的本次操作。
func (s *Server) grantReauth(c *gin.Context) time.Time {
	until := time.Now().UTC().Add(reauthElevationTTL)
	id := currentSessionID(c)
	if id == "" {
		return until
	}
	if err := s.Store.SetSessionReauthUntil(id, until); err != nil {
		s.logger().Warn("保存会话再认证有效期失败",
			"error", err.Error(),
			"request_id", c.GetString(requestIDKey),
		)
	}
	c.Set(sessionReauthUntilKey, until)
	return until
}

// sessionReauthValid 判断当前请求是否仍处于会话级再认证有效期内。
func (s *Server) sessionReauthValid(c *gin.Context) bool {
	value, ok := c.Get(sessionReauthUntilKey)
	if !ok {
		return false
	}
	until, ok := value.(time.Time)
	if !ok {
		return false
	}
	return time.Now().UTC().Before(until)
}

// sessionReauthUntil 返回当前会话的再认证有效期；不存在时返回零值。
func (s *Server) sessionReauthUntil(c *gin.Context) time.Time {
	value, ok := c.Get(sessionReauthUntilKey)
	if !ok {
		return time.Time{}
	}
	until, _ := value.(time.Time)
	return until
}

// failReauthRequired 返回需要再认证的 403，供前端弹出统一再认证框后重试。
func failReauthRequired(c *gin.Context) {
	c.Abort()
	payload := gin.H{
		"success":         false,
		"code":            "api.error.reauth_required",
		"message":         i18n.T(locale(c), "api.error.reauth_required", nil),
		"reauth_required": true,
	}
	if id := c.GetString(requestIDKey); id != "" {
		payload["request_id"] = id
	}
	c.JSON(http.StatusForbidden, payload)
}

// handleReauth 为浏览器会话建立短时再认证有效期。
// 前端在收到 reauth_required 后调用该接口，成功后自动重试原请求。
func (s *Server) handleReauth(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if apiKeyFromContext(c) != nil {
		failKey(c, http.StatusForbidden, "api.error.reauth_session_required")
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyReauth(c, user, body.Password, strings.TrimSpace(body.TOTP)) {
		return
	}
	ok(c, gin.H{"expires_at": s.sessionReauthUntil(c)})
}
