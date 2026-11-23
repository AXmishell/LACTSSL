package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"lactssl/internal/auth"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

func TestPasswordLengthCountsRunes(t *testing.T) {
	if validPassword("short") {
		t.Fatal("过短密码不应通过")
	}
	if validPassword("1234567") {
		t.Fatal("7 个字符不应通过")
	}
	if !validPassword("12345678") {
		t.Fatal("8 个 ASCII 字符应通过")
	}
	if validPassword(strings.Repeat("密", 3)) {
		t.Fatal("3 个多字节字符（9 字节）不应被当作 8 个字符")
	}
	if !validPassword(strings.Repeat("密", 8)) {
		t.Fatal("8 个多字节字符应通过")
	}
}

func loginSessionTest(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	return findCookie(t, setup.Result().Cookies(), sessionCookieName)
}

func TestHostPrefixedCookieNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{}
	if got := s.cookieName(sessionCookieName, true); got != "__Host-lactssl_session" {
		t.Fatalf("HTTPS Cookie 名不正确: %s", got)
	}
	if got := s.cookieName(sessionCookieName, false); got != sessionCookieName {
		t.Fatalf("HTTP Cookie 名不应带前缀: %s", got)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "__Host-lactssl_session", Value: "secure-token"})
	value, err := s.readCookie(c, sessionCookieName)
	if err != nil || value != "secure-token" {
		t.Fatalf("应优先读取 __Host- Cookie: %q %v", value, err)
	}
}

func TestSecureCookiePrefixFlow(t *testing.T) {
	router := newSessionTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.30:1234"
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTPS 初始化失败: %d %s", w.Code, w.Body.String())
	}
	session := findCookie(t, w.Result().Cookies(), "__Host-"+sessionCookieName)
	csrf := findCookie(t, w.Result().Cookies(), "__Host-"+csrfCookieName)
	if !session.Secure || !csrf.Secure {
		t.Fatalf("__Host- Cookie 必须带 Secure: session=%+v csrf=%+v", session, csrf)
	}
	// 写入 __Host- Cookie 时必须同时删除明文时代的旧名称，避免两者并存。
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		legacy := findCookie(t, w.Result().Cookies(), name)
		if legacy.MaxAge >= 0 {
			t.Fatalf("应删除旧名称 Cookie %s: %+v", name, legacy)
		}
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.RemoteAddr = "192.0.2.30:1234"
	meReq.TLS = &tls.ConnectionState{}
	meReq.AddCookie(session)
	meResp := httptest.NewRecorder()
	router.ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("__Host- 会话 Cookie 未被读取: %d %s", meResp.Code, meResp.Body.String())
	}
}

// TestAuthenticatedHTTPSRequestClearsShadowedLegacyCookies 验证升级或切换到 HTTPS 后，
// 与新 __Host- Cookie 并存的旧名称 Cookie 会在首次认证请求时被清理，
// 防止前端读到旧 CSRF 值而把所有写操作误判为未登录。
func TestAuthenticatedHTTPSRequestClearsShadowedLegacyCookies(t *testing.T) {
	router := newSessionTestServer(t)
	setupReq := httptest.NewRequest(http.MethodPost, "/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupReq.RemoteAddr = "192.0.2.31:1234"
	setupReq.TLS = &tls.ConnectionState{}
	setupW := httptest.NewRecorder()
	router.ServeHTTP(setupW, setupReq)
	if setupW.Code != http.StatusOK {
		t.Fatalf("HTTPS 初始化失败: %d %s", setupW.Code, setupW.Body.String())
	}
	session := findCookie(t, setupW.Result().Cookies(), "__Host-"+sessionCookieName)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.RemoteAddr = "192.0.2.31:1234"
	req.TLS = &tls.ConnectionState{}
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "legacy-session"})
	req.AddCookie(&http.Cookie{Name: hostCookiePrefix + csrfCookieName, Value: strings.Repeat("b", csrfTokenLength)})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: strings.Repeat("a", csrfTokenLength)})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("认证请求失败: %d %s", resp.Code, resp.Body.String())
	}
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		legacy := findCookie(t, resp.Result().Cookies(), name)
		if legacy.MaxAge >= 0 {
			t.Fatalf("并存的旧名称 Cookie %s 应被删除: %+v", name, legacy)
		}
	}
}

// TestCSRFHeaderMayMatchLegacyCookieVariant 验证新旧 CSRF Cookie 并存时，
// 请求头与其中任意一个真实令牌一致即可通过，避免部署方式切换后的写操作
// 被误判为未登录；两个都不匹配时仍必须拒绝。
func TestCSRFHeaderMayMatchLegacyCookieVariant(t *testing.T) {
	router := newSessionTestServer(t)
	setupReq := httptest.NewRequest(http.MethodPost, "/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupReq.RemoteAddr = "192.0.2.32:1234"
	setupReq.TLS = &tls.ConnectionState{}
	setupW := httptest.NewRecorder()
	router.ServeHTTP(setupW, setupReq)
	if setupW.Code != http.StatusOK {
		t.Fatalf("HTTPS 初始化失败: %d %s", setupW.Code, setupW.Body.String())
	}
	session := findCookie(t, setupW.Result().Cookies(), hostCookiePrefix+sessionCookieName)
	prefixedCSRF := findCookie(t, setupW.Result().Cookies(), hostCookiePrefix+csrfCookieName)
	legacyValue := strings.Repeat("a", csrfTokenLength)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{name: "匹配旧名称 Cookie", header: legacyValue, want: http.StatusOK},
		{name: "匹配 __Host- Cookie", header: prefixedCSRF.Value, want: http.StatusOK},
		{name: "两个 Cookie 都不匹配", header: strings.Repeat("c", csrfTokenLength), want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(csrfHeaderName, tc.header)
			req.RemoteAddr = "192.0.2.32:1234"
			req.TLS = &tls.ConnectionState{}
			req.AddCookie(session)
			req.AddCookie(prefixedCSRF)
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: legacyValue})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("状态码 = %d，期望 %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestCertificateDownloadsAndPrivateCAKeyWithSession(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		CertPEM:       "CERT",
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "Test CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}

	// 证书链、证书与证书私钥查看/下载都只依赖登录会话。
	openCases := []struct {
		path string
		want string
	}{
		{"/api/certificates/" + itoa(cert.ID) + "/download?type=fullchain", "CHAIN"},
		{"/api/certificates/" + itoa(cert.ID) + "/download?type=cert", "CERT"},
		{"/api/certificates/" + itoa(cert.ID) + "/pem?type=key", "PRIVATE-KEY"},
		{"/api/certificates/" + itoa(cert.ID) + "/download?type=key", "PRIVATE-KEY"},
	}
	for _, item := range openCases {
		allowed := sessionRequest(t, router, http.MethodGet, item.path, "", []*http.Cookie{session}, nil)
		if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), item.want) {
			t.Fatalf("%s 应直接允许访问: %d %s", item.path, allowed.Code, allowed.Body.String())
		}
	}

	// 私有 CA 私钥导出仍需要再认证有效期。
	caKeyPath := "/api/private-cas/" + itoa(ca.ID) + "/download?type=key"
	denied := sessionRequest(t, router, http.MethodGet, caKeyPath, "", []*http.Cookie{session}, nil)
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "api.error.reauth_required") {
		t.Fatalf("未再认证时不应允许导出私有 CA 私钥: %d %s", denied.Code, denied.Body.String())
	}
	reauth := sessionRequest(t, router, http.MethodPost, "/api/auth/reauth",
		`{"password":"password1234"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if reauth.Code != http.StatusOK {
		t.Fatalf("再认证失败: %d %s", reauth.Code, reauth.Body.String())
	}
	allowed := sessionRequest(t, router, http.MethodGet, caKeyPath, "", []*http.Cookie{session}, nil)
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), "BEGIN") {
		t.Fatalf("%s 再认证后应允许访问: %d %s", caKeyPath, allowed.Code, allowed.Body.String())
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}

func TestReauthEndpointGrantsElevation(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	headers := map[string]string{csrfHeaderName: "1"}

	missing := sessionRequest(t, router, http.MethodPost, "/api/auth/reauth",
		`{}`, []*http.Cookie{session}, headers)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少密码不应建立再认证有效期: %d %s", missing.Code, missing.Body.String())
	}

	granted := sessionRequest(t, router, http.MethodPost, "/api/auth/reauth",
		`{"password":"password1234"}`, []*http.Cookie{session}, headers)
	if granted.Code != http.StatusOK || !strings.Contains(granted.Body.String(), `"expires_at"`) {
		t.Fatalf("正确密码应建立再认证有效期: %d %s", granted.Code, granted.Body.String())
	}

	// 有效期内低风险敏感操作无需重复输入密码。
	entry := sessionRequest(t, router, http.MethodPost, "/api/security/entry",
		`{"entry":"newentry123"}`, []*http.Cookie{session}, headers)
	if entry.Code != http.StatusOK {
		t.Fatalf("再认证有效期内应允许修改门禁入口: %d %s", entry.Code, entry.Body.String())
	}

	// 账户控制权变更仍要求现场验证，不能复用提权窗口。
	email := sessionRequest(t, router, http.MethodPost, "/api/auth/change-email",
		`{"email":"new@example.com"}`, []*http.Cookie{session}, headers)
	if email.Code != http.StatusBadRequest || !strings.Contains(email.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("改邮箱必须现场再认证: %d %s", email.Code, email.Body.String())
	}
}

func TestTOTPReauthUsesSeparatePurpose(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, true); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	login := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234","totp":"`+code+`"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("使用 TOTP 登录失败: %d %s", login.Code, login.Body.String())
	}
	session = findCookie(t, login.Result().Cookies(), sessionCookieName)

	headers := map[string]string{csrfHeaderName: "1"}
	reauth := sessionRequest(t, router, http.MethodPost, "/api/auth/reauth",
		`{"password":"password1234","totp":"`+code+`"}`, []*http.Cookie{session}, headers)
	if reauth.Code != http.StatusOK {
		t.Fatalf("登录用过的验证码应可在 reauth 用途复用: %d %s", reauth.Code, reauth.Body.String())
	}
}

func TestReauthReplayedCodeIsRejected(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, true); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// 先消费 reauth 用途的验证码，登录用途仍然可用。
	if got := auth.ValidateTOTPPurpose(secret, code, auth.TOTPPurposeReauth); got != auth.TOTPValid {
		t.Fatalf("预消费 reauth 验证码失败: %v", got)
	}
	login := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234","totp":"`+code+`"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("使用 TOTP 登录失败: %d %s", login.Code, login.Body.String())
	}
	session = findCookie(t, login.Result().Cookies(), sessionCookieName)

	headers := map[string]string{csrfHeaderName: "1"}
	replay := sessionRequest(t, router, http.MethodPost, "/api/auth/reauth",
		`{"password":"password1234","totp":"`+code+`"}`, []*http.Cookie{session}, headers)
	if replay.Code != http.StatusBadRequest || !strings.Contains(replay.Body.String(), "api.error.totp_reused") {
		t.Fatalf("同一用途重复使用应返回 totp_reused: %d %s", replay.Code, replay.Body.String())
	}
}

func TestReauthEndpointRejectsAPIKey(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyForTest(t, router, session, "reauth-key", []string{apiScopeAll})
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/auth/reauth",
		`{"password":"password1234"}`, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_route_forbidden") {
		t.Fatalf("API 密钥不应调用交互式再认证: %d %s", resp.Code, resp.Body)
	}
}

func TestValidEmailAllowsInternalDomain(t *testing.T) {
	for _, email := range []string{"admin@corp", "admin@example.com", "ops@localhost"} {
		if !validEmail(email) {
			t.Fatalf("合法邮箱被拒绝: %s", email)
		}
	}
	for _, email := range []string{"", "admin", "admin@", "@example.com", "Admin <admin@example.com>"} {
		if validEmail(email) {
			t.Fatalf("非法邮箱被接受: %s", email)
		}
	}
}
