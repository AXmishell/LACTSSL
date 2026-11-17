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

	// 证书链/证书下载只依赖登录会话。
	openCases := []struct {
		path string
		want string
	}{
		{"/api/certificates/" + itoa(cert.ID) + "/download?type=fullchain", "CHAIN"},
		{"/api/certificates/" + itoa(cert.ID) + "/download?type=cert", "CERT"},
	}
	for _, item := range openCases {
		allowed := sessionRequest(t, router, http.MethodGet, item.path, "", []*http.Cookie{session}, nil)
		if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), item.want) {
			t.Fatalf("%s 应直接允许访问: %d %s", item.path, allowed.Code, allowed.Body.String())
		}
	}

	// 证书私钥查看与下载只需要登录会话。
	keyCases := []string{
		"/api/certificates/" + itoa(cert.ID) + "/pem?type=key",
		"/api/certificates/" + itoa(cert.ID) + "/download?type=key",
	}
	for _, path := range keyCases {
		allowed := sessionRequest(t, router, http.MethodGet, path, "", []*http.Cookie{session}, nil)
		if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), "PRIVATE-KEY") {
			t.Fatalf("%s 登录会话应允许访问: %d %s", path, allowed.Code, allowed.Body.String())
		}
	}

	// 私有 CA 私钥导出只需要登录会话。
	caKeyPath := "/api/private-cas/" + itoa(ca.ID) + "/download?type=key"
	allowed := sessionRequest(t, router, http.MethodGet, caKeyPath, "", []*http.Cookie{session}, nil)
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), "BEGIN") {
		t.Fatalf("%s 登录会话应允许访问: %d %s", caKeyPath, allowed.Code, allowed.Body.String())
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
