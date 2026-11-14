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
)

// 二次验证方式。需要 TOTP 的模式在 TOTP 不可用时统一回退到密码。
const (
	verifyModeSetting      = "security.verify_mode"
	verifyModeNone         = "none"
	verifyModePassword     = "password"
	verifyModeTOTP         = "totp"
	verifyModePasswordTOTP = "password_totp"
	verifyModePasskey      = "passkey"
	// verifyModeLegacyPasswordPasskey 仅为兼容旧配置保留。
	// 组合验证方式已移除，读取时统一按通行密钥验证处理。
	verifyModeLegacyPasswordPasskey = "password_passkey"
)

// verifyModeNeedsTOTP 判断验证方式是否需要动态验证码。
func verifyModeNeedsTOTP(mode string) bool {
	return mode == verifyModeTOTP || mode == verifyModePasswordTOTP
}

// verifyModeNeedsPassword 判断验证方式是否需要账户密码。
func verifyModeNeedsPassword(mode string) bool {
	return mode == verifyModePassword || mode == verifyModePasswordTOTP
}

// verifyModeNeedsPasskey 判断验证方式是否需要通行密钥。
func verifyModeNeedsPasskey(mode string) bool {
	return mode == verifyModePasskey
}

// effectiveVerifyMode 返回当前实际生效的验证方式。
// TOTP 或通行密钥未启用时回退到密码，避免用户被锁死在无法完成的操作上。
func (s *Server) effectiveVerifyMode(user *model.User) string {
	mode := strings.TrimSpace(s.setting(verifyModeSetting, verifyModePassword))
	switch mode {
	case verifyModeNone:
		return verifyModeNone
	case verifyModeTOTP, verifyModePasswordTOTP:
		if user != nil && user.TOTPEnabled && user.TOTPSecret != "" {
			return mode
		}
		return verifyModePassword
	case verifyModePasskey, verifyModeLegacyPasswordPasskey:
		if user != nil {
			if count, err := s.Store.CountPasskeys(user.ID); err == nil && count > 0 {
				return verifyModePasskey
			}
		}
		return verifyModePassword
	default:
		return verifyModePassword
	}
}

// allowVerifyFailure 对二次验证失败做账户维度限流。
// 不能只按 IP 限流，否则攻击者可以轮换地址爆破密码或 6 位 TOTP。
func (s *Server) allowVerifyFailure(c *gin.Context, user *model.User) bool {
	key := "reauth:acct:" + strconv.FormatInt(user.ID, 10)
	if allowed, wait := s.limiter.Allow(key, 20, 15*time.Minute); !allowed {
		rateLimited(c, wait)
		return false
	}
	return true
}

// verifySensitive 按当前“二次验证”方式校验密码、TOTP 或通行密钥。
// 返回 true 表示验证通过；失败时已写入 HTTP 响应。
func (s *Server) verifySensitive(c *gin.Context, user *model.User, password, code, passkeyProof string) bool {
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.not_logged_in")
		return false
	}
	// 通过通行密钥登录的会话已经完成强认证，登录后不再重复要求二次验证。
	if sessionStrongAuth(c) {
		return true
	}
	mode := s.effectiveVerifyMode(user)
	if mode == verifyModeNone {
		return true
	}
	if !s.allowReauth(c, user) {
		return false
	}
	if verifyModeNeedsPassword(mode) && !auth.VerifyPassword(user.PasswordHash, password) {
		if !s.allowVerifyFailure(c, user) {
			return false
		}
		failKey(c, http.StatusBadRequest, "api.error.current_password_wrong")
		return false
	}
	if verifyModeNeedsTOTP(mode) {
		if strings.TrimSpace(code) == "" {
			// 这里使用 400 而不是 401：前端只对 /auth/reauth 的 401 做特殊处理，
			// 其他接口的 401 会触发强制跳转登录页。
			c.Abort()
			c.JSON(http.StatusBadRequest, gin.H{
				"success":       false,
				"code":          "api.error.totp_required",
				"message":       i18n.T(locale(c), "api.error.totp_required", nil),
				"totp_required": true,
			})
			return false
		}
		if !auth.ValidateTOTP(user.TOTPSecret, code) {
			if !s.allowVerifyFailure(c, user) {
				return false
			}
			failKey(c, http.StatusBadRequest, "api.error.totp_invalid")
			return false
		}
	}
	if verifyModeNeedsPasskey(mode) {
		if strings.TrimSpace(passkeyProof) == "" {
			c.Abort()
			c.JSON(http.StatusBadRequest, gin.H{
				"success":          false,
				"code":             "api.error.passkey_required",
				"message":          i18n.T(locale(c), "api.error.passkey_required", nil),
				"passkey_required": true,
			})
			return false
		}
		if !s.validPasskeyProof(user, currentSessionID(c), passkeyProof) {
			if !s.allowVerifyFailure(c, user) {
				return false
			}
			failKey(c, http.StatusBadRequest, "api.error.passkey_verify_failed")
			return false
		}
	}
	s.resetReauth(c, user)
	return true
}

// changeVerifyMode 切换二次验证方式。
// 切换本身必须使用当前生效的验证方式确认身份，不能只凭登录会话修改。
func (s *Server) changeVerifyMode(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.not_logged_in")
		return
	}
	var body struct {
		Mode         string          `json:"mode"`
		Password     string          `json:"password"`
		TOTP         string          `json:"totp"`
		PasskeyProof string          `json:"passkey_proof"`
		StepUp       map[string]bool `json:"step_up"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	for action := range body.StepUp {
		if !isToggleableStepUp(action) {
			failKey(c, http.StatusBadRequest, "api.error.invalid_params")
			return
		}
	}
	mode := strings.TrimSpace(body.Mode)
	switch mode {
	case verifyModeNone, verifyModePassword, verifyModeTOTP, verifyModePasswordTOTP, verifyModePasskey:
	default:
		failKey(c, http.StatusBadRequest, "api.error.verify_mode_invalid")
		return
	}
	if verifyModeNeedsTOTP(mode) && (!user.TOTPEnabled || user.TOTPSecret == "") {
		failKey(c, http.StatusBadRequest, "api.error.totp_not_enabled")
		return
	}
	if verifyModeNeedsPasskey(mode) {
		count, err := s.Store.CountPasskeys(user.ID)
		if err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		if count == 0 {
			failKey(c, http.StatusBadRequest, "api.error.passkey_not_enabled")
			return
		}
	}
	if !s.verifySensitive(c, user, body.Password, body.TOTP, body.PasskeyProof) {
		return
	}
	if err := s.Store.SetSetting(verifyModeSetting, mode); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.save_verify_mode_failed")
		return
	}
	for action, enabled := range body.StepUp {
		if err := s.Store.SetSetting(stepUpSettingKey(action), strconv.FormatBool(enabled)); err != nil {
			failKey(c, http.StatusInternalServerError, "api.error.save_verify_mode_failed")
			return
		}
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.verify_mode_changed", map[string]any{"mode": mode})
	stepUp := make(map[string]bool, len(toggleableStepUpActions))
	for _, action := range toggleableStepUpActions {
		stepUp[action] = s.stepUpEnabled(action)
	}
	ok(c, gin.H{
		"mode":    mode,
		"step_up": stepUp,
		"message": i18n.T(locale(c), "api.ok.verify_mode_updated", nil),
	})
}
