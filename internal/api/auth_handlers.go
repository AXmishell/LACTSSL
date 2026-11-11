package api

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/util"
)

func getUser(c *gin.Context) *model.User {
	v, exists := c.Get("user")
	if !exists {
		return nil
	}
	user, _ := v.(*model.User)
	return user
}

func validEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	return strings.EqualFold(addr.Address, email) && strings.Contains(email, ".")
}

func validPassword(password string) bool {
	return len(password) >= 8 && len(password) <= 72
}

func (s *Server) setupStatus(c *gin.Context) {
	count, err := s.Store.CountUsers()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"initialized": count > 0})
}

func (s *Server) handleSetup(c *gin.Context) {
	if allowed, wait := s.limiter.Allow("setup:"+clientRateKey(c), 5, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	count, err := s.Store.CountUsers()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		failKey(c, http.StatusBadRequest, "api.error.already_initialized")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(body.Email) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_email")
		return
	}
	if !validPassword(body.Password) {
		failKey(c, http.StatusBadRequest, "api.error.password_length")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.hash_failed")
		return
	}
	user, err := s.Store.CreateUser(body.Email, hash)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.create_account_failed", "error", err.Error())
		return
	}
	// 首次使用时把邮箱作为 ACME 账户邮箱的默认值。
	_ = s.Store.SetSetting("acme.email", body.Email)
	_ = s.Store.SetSetting("language", string(locale(c)))
	expire, err := s.issueSession(c, user)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.token_failed")
		return
	}
	s.Store.AddLogKey("auth", 0, "info", "log.auth.setup", map[string]any{"email": user.Email})
	ok(c, gin.H{"expire_at": expire, "user": user})
}

func (s *Server) handleLogin(c *gin.Context) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))

	// 登录限流：IP 维度在密码校验前扣减，成功后归还；账户维度只对失败扣减，
	// 这样即使账户配额被攻击者耗尽，正确密码仍可登录，避免账户被恶意锁定。
	ipKey := "login:ip:" + clientRateKey(c)
	if allowed, wait := s.limiter.Allow(ipKey, 10, 5*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	acctKey := "login:acct:" + util.HashToken(email)[:16]

	user, err := s.Store.GetUserByEmail(email)
	passwordOK := false
	if err == nil && user != nil {
		passwordOK = auth.VerifyPassword(user.PasswordHash, body.Password)
	} else {
		// 用户不存在时也执行一次 bcrypt，避免通过响应耗时枚举邮箱。
		auth.VerifyDummy(body.Password)
	}
	if !passwordOK {
		if allowed, wait := s.limiter.Allow(acctKey, 20, 15*time.Minute); !allowed {
			rateLimited(c, wait)
			return
		}
		s.Store.AddLogKey("auth", 0, "warning", "log.auth.login_failed", map[string]any{"ip": c.ClientIP()})
		failKey(c, http.StatusUnauthorized, "api.error.bad_credentials")
		return
	}
	if user.TOTPEnabled {
		if strings.TrimSpace(body.TOTP) == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success":       false,
				"code":          "api.error.totp_required",
				"message":       i18n.T(locale(c), "api.error.totp_required", nil),
				"totp_required": true,
			})
			return
		}
		totpKey := "login:totp:" + acctKey
		if !auth.ValidateTOTP(user.TOTPSecret, body.TOTP) {
			if allowed, wait := s.limiter.Allow(totpKey, 5, 10*time.Minute); !allowed {
				rateLimited(c, wait)
				return
			}
			failKey(c, http.StatusUnauthorized, "api.error.totp_invalid")
			return
		}
		s.limiter.Reset(totpKey)
	}
	expire, err := s.issueSession(c, user)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.token_failed")
		return
	}
	s.limiter.Reset(ipKey)
	s.limiter.Reset(acctKey)
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.login_success", map[string]any{"ip": c.ClientIP()})
	ok(c, gin.H{"expire_at": expire, "user": user})
}

// handleLogout 清除会话 Cookie。令牌本身无状态，客户端也可能已过期，因此不要求登录，
// 但仍要求 CSRF 头，避免被跨站请求强制退出。
func (s *Server) handleLogout(c *gin.Context) {
	if !s.validCSRF(c) {
		failKey(c, http.StatusForbidden, "api.error.unauthorized")
		return
	}
	// 退出时同步撤销服务端会话，令牌在 Cookie 清除后也不会再被复用。
	if token, _ := s.sessionToken(c); token != "" {
		if claims, err := s.Auth.Parse(token); err == nil {
			_, _ = s.Store.RevokeSession(claims.UserID, sessionIDForToken(claims, token))
		}
	}
	s.clearSessionCookie(c)
	s.clearCSRFCookie(c)
	ok(c, gin.H{"ok": true})
}

func (s *Server) handleMe(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.not_logged_in")
		return
	}
	ok(c, user)
}

func (s *Server) handleChangePassword(c *gin.Context) {
	user := getUser(c)
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, body.OldPassword) {
		failKey(c, http.StatusBadRequest, "api.error.current_password_wrong")
		return
	}
	if !validPassword(body.NewPassword) {
		failKey(c, http.StatusBadRequest, "api.error.new_password_length")
		return
	}
	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.hash_failed")
		return
	}
	if err := s.Store.UpdatePassword(user.ID, hash); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.change_password_failed", "error", err.Error())
		return
	}
	updated, _ := s.Store.GetUserByID(user.ID)
	// 改密会提升 TokenVersion，旧令牌全部失效，同时清理旧会话记录。
	_ = s.Store.DeleteUserSessions(user.ID)
	expire, err := s.issueSession(c, updated)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.token_failed")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.password_changed", nil)
	ok(c, gin.H{"expire_at": expire})
}

func (s *Server) handleChangeEmail(c *gin.Context) {
	user := getUser(c)
	var body struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, body.Password) {
		failKey(c, http.StatusBadRequest, "api.error.current_password_wrong")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(email) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_email")
		return
	}
	if err := s.Store.UpdateEmail(user.ID, email); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.change_email_failed", "error", err.Error())
		return
	}
	updated, _ := s.Store.GetUserByID(user.ID)
	// 换邮箱会提升 TokenVersion，旧令牌全部失效，同时清理旧会话记录。
	_ = s.Store.DeleteUserSessions(user.ID)
	expire, err := s.issueSession(c, updated)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.token_failed")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.email_changed", map[string]any{"email": email})
	ok(c, gin.H{"expire_at": expire, "user": updated})
}

func (s *Server) handleTOTPSetup(c *gin.Context) {
	user := getUser(c)
	if user.TOTPEnabled {
		failKey(c, http.StatusBadRequest, "api.error.totp_already_enabled")
		return
	}
	secret, url, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if err := s.Store.SetTOTP(user.ID, secret, false); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.save_totp_failed")
		return
	}
	ok(c, gin.H{"secret": secret, "url": url})
}

func (s *Server) handleTOTPEnable(c *gin.Context) {
	user := getUser(c)
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if user.TOTPSecret == "" {
		failKey(c, http.StatusBadRequest, "api.error.totp_secret_missing")
		return
	}
	if !auth.ValidateTOTP(user.TOTPSecret, body.Code) {
		failKey(c, http.StatusBadRequest, "api.error.totp_invalid")
		return
	}
	if err := s.Store.SetTOTP(user.ID, user.TOTPSecret, true); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.enable_totp_failed")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.totp_enabled", nil)
	ok(c, gin.H{"enabled": true})
}

func (s *Server) handleTOTPDisable(c *gin.Context) {
	user := getUser(c)
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, body.Password) {
		failKey(c, http.StatusBadRequest, "api.error.current_password_wrong")
		return
	}
	if !auth.ValidateTOTP(user.TOTPSecret, body.Code) {
		failKey(c, http.StatusBadRequest, "api.error.totp_invalid")
		return
	}
	if err := s.Store.SetTOTP(user.ID, "", false); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.disable_totp_failed")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "warning", "log.auth.totp_disabled", nil)
	ok(c, gin.H{"enabled": false})
}

func (s *Server) handleForgotPassword(c *gin.Context) {
	var body struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if allowed, wait := s.limiter.Allow("forgot:ip:"+clientRateKey(c), 5, 10*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	// 邮箱维度限制重置邮件发送频率；超限时仍返回统一提示，避免泄露邮箱是否存在。
	if allowed, _ := s.limiter.Allow("forgot:acct:"+util.HashToken(email)[:16], 3, time.Hour); !allowed {
		ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.reset_sent", nil)})
		return
	}
	// 配置检查放在查询账户之前，避免通过错误类型判断邮箱是否存在。
	smtpCfg := s.Manager.LoadSMTPConfig()
	if smtpCfg.Host == "" {
		failKey(c, http.StatusBadRequest, "api.error.smtp_not_configured")
		return
	}
	baseURL, err := s.publicBaseURL(c)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.public_url_required")
		return
	}
	user, err := s.Store.GetUserByEmail(email)
	if err != nil {
		// 无论邮箱是否存在都返回相同提示。
		ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.reset_sent", nil)})
		return
	}

	rawToken, err := util.RandomHex(32)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.reset_token_failed")
		return
	}
	if err := s.Store.CreatePasswordReset(user.ID, util.HashToken(rawToken), time.Now().Add(30*time.Minute)); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.save_reset_token_failed")
		return
	}
	link := fmt.Sprintf("%s/%s/reset?token=%s", baseURL, s.Entry(), rawToken)
	current := locale(c)
	bodyHTML := i18n.Th(current, "email.reset.body", map[string]any{"link": link})
	if err := service.SendMail(smtpCfg, user.Email, i18n.T(current, "email.reset.subject", nil), bodyHTML); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.reset_mail_sent", nil)
	ok(c, gin.H{"message": i18n.T(current, "api.ok.reset_sent", nil)})
}

func (s *Server) handleValidateReset(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" || !s.Store.PasswordResetValid(util.HashToken(token)) {
		failKey(c, http.StatusBadRequest, "api.error.reset_link_invalid")
		return
	}
	ok(c, gin.H{"valid": true})
}

func (s *Server) handleResetPassword(c *gin.Context) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !validPassword(body.Password) {
		failKey(c, http.StatusBadRequest, "api.error.password_length")
		return
	}
	userID, err := s.Store.ConsumePasswordReset(util.HashToken(strings.TrimSpace(body.Token)))
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.hash_failed")
		return
	}
	if err := s.Store.UpdatePassword(userID, hash); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.change_password_failed", "error", err.Error())
		return
	}
	// 密码重置后旧令牌全部失效，清理对应会话记录。
	_ = s.Store.DeleteUserSessions(userID)
	s.Store.AddLogKey("auth", userID, "info", "log.auth.password_reset", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.password_reset", nil)})
}
