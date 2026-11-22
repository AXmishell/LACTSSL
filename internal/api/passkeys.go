package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"lactssl/internal/auth"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/store"
)

const (
	passkeyCeremonyRegister = "register"
	passkeyCeremonyLogin    = "login"

	passkeyCeremonyTTL = 5 * time.Minute
)

// passkeyCeremony 暂存一次 WebAuthn 挑战。
// 挑战数据只保存在服务端内存中，客户端仅持有随机会话 ID 和公开的选项。
type passkeyCeremony struct {
	Kind      string
	UserID    int64
	SessionID string
	Session   webauthn.SessionData
	Expires   time.Time
}

// passkeyOrigin 推导当前请求的 WebAuthn RP ID 与 Origin。
// 优先使用固定的 security.public_url，避免 Host 头影响挑战绑定的来源。
func (s *Server) passkeyOrigin(c *gin.Context) (origin, rpID string, err error) {
	if s.PublicURL != "" {
		parsed, parseErr := url.Parse(s.PublicURL)
		if parseErr != nil || parsed.Host == "" {
			return "", "", i18n.E("api.error.passkey_origin_invalid")
		}
		origin = parsed.Scheme + "://" + parsed.Host
		rpID = parsed.Hostname()
	} else {
		host := strings.TrimSpace(c.Request.Host)
		if !validPublicHost(host) {
			return "", "", i18n.E("api.error.passkey_origin_invalid")
		}
		parsed, parseErr := url.Parse("//" + host)
		if parseErr != nil || parsed.Hostname() == "" {
			return "", "", i18n.E("api.error.passkey_origin_invalid")
		}
		scheme := "http"
		if c.Request.TLS != nil || (s.isTrustedRequest(c) &&
			strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")) {
			scheme = "https"
		}
		origin = scheme + "://" + parsed.Host
		rpID = parsed.Hostname()
	}
	// WebAuthn 要求 RP ID 是域名，裸 IP 无法作为可信来源；同时非 HTTPS 的远程地址
	// 不是安全上下文，浏览器不会允许创建通行密钥。
	if ip := net.ParseIP(rpID); ip != nil {
		return "", "", i18n.E("api.error.passkey_domain_required")
	}
	lowerHost := strings.ToLower(rpID)
	if !strings.HasPrefix(origin, "https://") && lowerHost != "localhost" && !isLoopbackHost(lowerHost) {
		return "", "", i18n.E("api.error.passkey_https_required")
	}
	return origin, strings.ToLower(rpID), nil
}

// passkeyWebAuthn 按当前请求构造 WebAuthn 实例。
func (s *Server) passkeyWebAuthn(c *gin.Context) (*webauthn.WebAuthn, error) {
	origin, rpID, err := s.passkeyOrigin(c)
	if err != nil {
		return nil, err
	}
	instance, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "LACTSSL",
		RPID:          rpID,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, i18n.E("api.error.passkey_config_invalid", "error", err.Error())
	}
	return instance, nil
}

// putPasskeyCeremony 保存挑战并返回随机会话 ID。
func (s *Server) putPasskeyCeremony(item passkeyCeremony) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	item.Expires = time.Now().Add(passkeyCeremonyTTL)
	if item.Session.Expires.IsZero() || item.Session.Expires.After(item.Expires) {
		item.Session.Expires = item.Expires
	}
	s.passkeyMu.Lock()
	if s.passkeyCeremonies == nil {
		s.passkeyCeremonies = make(map[string]passkeyCeremony)
	}
	now := time.Now()
	for key, value := range s.passkeyCeremonies {
		if !now.Before(value.Expires) {
			delete(s.passkeyCeremonies, key)
		}
	}
	s.passkeyCeremonies[id] = item
	s.passkeyMu.Unlock()
	return id, nil
}

// takePasskeyCeremony 取出并删除挑战，确保同一挑战只能使用一次。
func (s *Server) takePasskeyCeremony(id, kind string) (*passkeyCeremony, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	s.passkeyMu.Lock()
	defer s.passkeyMu.Unlock()
	item, ok := s.passkeyCeremonies[id]
	if !ok {
		return nil, false
	}
	delete(s.passkeyCeremonies, id)
	if item.Kind != kind || !time.Now().Before(item.Expires) {
		return nil, false
	}
	return &item, true
}

func (s *Server) passkeyUser(user *model.User) (*auth.WebAuthnUser, error) {
	items, err := s.Store.ListPasskeys(user.ID)
	if err != nil {
		return nil, err
	}
	return auth.NewWebAuthnUser(user, items), nil
}

type passkeyCredentialBody struct {
	SessionID  string          `json:"session_id"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

func decodePasskeyCredential(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("missing credential")
	}
	return raw, nil
}

// findPasskeyUser 供可发现凭据登录回调使用。
func (s *Server) findPasskeyUser(rawID, _ []byte) (webauthn.User, error) {
	credentialID := base64.RawURLEncoding.EncodeToString(rawID)
	item, err := s.Store.GetPasskeyByCredentialID(credentialID)
	if err != nil {
		return nil, err
	}
	user, err := s.Store.GetUserByID(item.UserID)
	if err != nil {
		return nil, err
	}
	return s.passkeyUser(user)
}

func passkeyCredentialID(rawID []byte) string {
	return base64.RawURLEncoding.EncodeToString(rawID)
}

// ---------------------------- 登录 ----------------------------

// handlePasskeyAvailability 供匿名登录页判断是否展示通行密钥入口。
// 未注册任何通行密钥时不应展示按钮，否则用户点击后只会得到验证失败。
// 只返回布尔值，不暴露具体账户或凭据信息。
func (s *Server) handlePasskeyAvailability(c *gin.Context) {
	count, err := s.Store.CountAllPasskeys()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"available": count > 0})
}

func (s *Server) handlePasskeyLoginBegin(c *gin.Context) {
	if allowed, wait := s.allow(ratelimit.PasskeyLoginBegin, "passkey:login:begin:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	instance, err := s.passkeyWebAuthn(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	assertion, session, err := instance.BeginDiscoverableLogin()
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	id, err := s.putPasskeyCeremony(passkeyCeremony{Kind: passkeyCeremonyLogin, Session: *session})
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.passkey_challenge_failed")
		return
	}
	ok(c, gin.H{"session_id": id, "options": assertion})
}

func (s *Server) handlePasskeyLoginFinish(c *gin.Context) {
	var body passkeyCredentialBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	raw, err := decodePasskeyCredential(body.Credential)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_credential_required")
		return
	}
	ceremony, found := s.takePasskeyCeremony(body.SessionID, passkeyCeremonyLogin)
	if !found {
		failKey(c, http.StatusBadRequest, "api.error.passkey_challenge_invalid")
		return
	}
	instance, err := s.passkeyWebAuthn(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_verify_failed")
		return
	}
	webauthnUser, credential, err := instance.ValidatePasskeyLogin(s.findPasskeyUser, ceremony.Session, parsed)
	if err != nil {
		s.Store.AddLogKey("auth", 0, "warning", "log.auth.passkey_login_failed", map[string]any{"ip": s.clientIP(c)})
		s.notifyLoginFailure(c, 0, "passkey")
		if allowed, wait := s.allow(ratelimit.PasskeyLoginFail, "passkey:login:fail:"+s.clientRateKey(c)); !allowed {
			rateLimited(c, wait)
			return
		}
		failKey(c, http.StatusUnauthorized, "api.error.passkey_login_failed")
		return
	}
	adapter, okAdapter := webauthnUser.(*auth.WebAuthnUser)
	if !okAdapter || adapter.User == nil || credential == nil {
		failKey(c, http.StatusUnauthorized, "api.error.passkey_login_failed")
		return
	}
	user := adapter.User
	if err := s.updatePasskeyCredential(user.ID, credential); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.passkey_save_failed")
		return
	}
	expire, err := s.issueSessionStrong(c, user, true)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.token_failed")
		return
	}
	s.limiter.Reset("passkey:login:begin:" + s.clientRateKey(c))
	s.limiter.Reset("passkey:login:fail:" + s.clientRateKey(c))
	user.StrongAuth = true
	s.resetLoginFailures(c, user.ID)
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.passkey_login_success", map[string]any{"ip": s.clientIP(c)})
	s.notifyLogin(c, user, model.SecurityEventLoginPasskey, "passkey", false)
	ok(c, gin.H{"expire_at": expire, "user": user})
}

// updatePasskeyCredential 保存认证后更新的签名计数与凭据标记。
func (s *Server) updatePasskeyCredential(userID int64, credential *webauthn.Credential) error {
	if credential == nil {
		return nil
	}
	encoded, err := auth.EncodePasskeyCredential(credential)
	if err != nil {
		return err
	}
	item, err := s.Store.GetPasskeyByCredentialID(passkeyCredentialID(credential.ID))
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return err
	}
	if item.UserID != userID {
		return errors.New("passkey owner mismatch")
	}
	return s.Store.UpdatePasskeyUsage(item.ID, encoded)
}

// ---------------------------- 管理 ----------------------------

func (s *Server) listPasskeys(c *gin.Context) {
	user := getUser(c)
	items, err := s.Store.ListPasskeys(user.ID)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, items)
}

func (s *Server) handlePasskeyRegisterBegin(c *gin.Context) {
	user := getUser(c)
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	// 兼容不带请求体的旧客户端；注册新认证器必须先再认证。
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauthFresh(c, user, body.Password, body.TOTP) {
		return
	}
	passkeys, err := s.Store.ListPasskeys(user.ID)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	instance, err := s.passkeyWebAuthn(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	webauthnUser := auth.NewWebAuthnUser(user, passkeys)
	creation, session, err := instance.BeginRegistration(webauthnUser,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(webauthnUser.WebAuthnCredentials()).CredentialDescriptors()),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()),
	)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	id, err := s.putPasskeyCeremony(passkeyCeremony{
		Kind:      passkeyCeremonyRegister,
		UserID:    user.ID,
		SessionID: currentSessionID(c),
		Session:   *session,
	})
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.passkey_challenge_failed")
		return
	}
	ok(c, gin.H{"session_id": id, "options": creation})
}

func (s *Server) handlePasskeyRegisterFinish(c *gin.Context) {
	user := getUser(c)
	var body passkeyCredentialBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	raw, err := decodePasskeyCredential(body.Credential)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_credential_required")
		return
	}
	// 完成注册不再次要求 TOTP：begin 已经完成再认证，且挑战在服务端一次性、
	// 绑定当前用户/会话且 5 分钟过期；再次校验会让 TOTP 重放保护拒绝同一个码。
	ceremony, found := s.takePasskeyCeremony(body.SessionID, passkeyCeremonyRegister)
	if !found || ceremony.UserID != user.ID {
		failKey(c, http.StatusBadRequest, "api.error.passkey_challenge_invalid")
		return
	}
	instance, err := s.passkeyWebAuthn(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(raw)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_verify_failed")
		return
	}
	webauthnUser, err := s.passkeyUser(user)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	credential, err := instance.CreateCredential(webauthnUser, ceremony.Session, parsed)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_verify_failed")
		return
	}
	encoded, err := auth.EncodePasskeyCredential(credential)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.passkey_save_failed")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > 64 {
		name = string([]rune(name)[:64])
	}
	item := &model.Passkey{
		UserID:       user.ID,
		Name:         name,
		CredentialID: passkeyCredentialID(credential.ID),
		Credential:   encoded,
	}
	if err := s.Store.CreatePasskey(item); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.passkey_save_failed")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.passkey_added", map[string]any{"name": item.Name})
	remaining, _ := s.Store.CountPasskeys(user.ID)
	s.notifySecurityEvent(c, user.ID, model.SecurityEventPasskeyAdded, map[string]string{
		"name":      item.Name,
		"remaining": fmt.Sprint(remaining),
	})
	ok(c, item)
}

func (s *Server) handlePasskeyDelete(c *gin.Context) {
	user := getUser(c)
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	id, okID := parseID(c)
	if !okID {
		return
	}
	if !s.requireReauthFresh(c, user, body.Password, body.TOTP) {
		return
	}
	deleted, err := s.Store.DeletePasskey(user.ID, id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !deleted {
		failKey(c, http.StatusNotFound, "api.error.passkey_not_found")
		return
	}
	count, _ := s.Store.CountPasskeys(user.ID)
	s.Store.AddLogKey("auth", user.ID, "warning", "log.auth.passkey_deleted", nil)
	s.notifySecurityEvent(c, user.ID, model.SecurityEventPasskeyDeleted, map[string]string{
		"remaining": fmt.Sprint(count),
	})
	ok(c, gin.H{"deleted": true, "remaining": count})
}
