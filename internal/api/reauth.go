package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
)

// requireReauth 校验敏感操作前的再认证信息。
// 启用 TOTP 时必须提供动态验证码，否则校验当前密码。
func (s *Server) requireReauth(c *gin.Context, user *model.User, password, totpCode string) bool {
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return false
	}
	ok := false
	if user.TOTPEnabled {
		// 高风险操作要求密码 + TOTP 双因素，避免仅凭一个验证码完成敏感变更。
		ok = auth.VerifyPassword(user.PasswordHash, password) &&
			auth.ValidateTOTP(user.TOTPSecret, strings.TrimSpace(totpCode))
	} else {
		ok = auth.VerifyPassword(user.PasswordHash, password)
	}
	key := "reauth:" + strconv.FormatInt(user.ID, 10)
	if ok {
		s.limiter.Reset(key)
		return true
	}
	if allowed, wait := s.allow(ratelimit.Reauth, key); !allowed {
		rateLimited(c, wait)
		return false
	}
	if user.TOTPEnabled {
		failKey(c, http.StatusBadRequest, "api.error.totp_invalid")
	} else {
		failKey(c, http.StatusBadRequest, "api.error.current_password_wrong")
	}
	return false
}
