package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"lactssl/internal/auth"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/util"
)

func createAPIKeyWithIPsForTest(t *testing.T, router http.Handler, session *http.Cookie, name string, scopes, allowedIPs []string) (string, int64) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":        name,
		"scopes":      scopes,
		"enabled":     true,
		"expires_at":  "",
		"allowed_ips": allowedIPs,
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/api-keys", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("创建带 IP 白名单的 API 密钥失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			APIKey model.APIKey `json:"api_key"`
			Token  string       `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 API 密钥响应失败: %v", err)
	}
	if payload.Data.Token == "" || payload.Data.APIKey.ID == 0 {
		t.Fatalf("API 密钥响应不完整: %s", resp.Body.String())
	}
	return payload.Data.Token, payload.Data.APIKey.ID
}

func apiKeyRequestFromRemote(t *testing.T, router http.Handler, token, method, path, body, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func apiKeyRequestFromIP(t *testing.T, router http.Handler, token, method, path, body, ip, headerName string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	if headerName == apiKeyHeader {
		headers[apiKeyHeader] = token
	}
	return apiKeyRequestFromRemote(t, router, token, method, path, body, net.JoinHostPort(ip, "1234"), headers)
}

func TestAPIKeyScopesReturnsClientIP(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	resp := sessionRequest(t, router, http.MethodGet, "/api/api-keys/scopes", "",
		[]*http.Cookie{session}, nil)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"client_ip":"192.0.2.30"`) {
		t.Fatalf("权限接口应返回当前客户端 IP: %d %s", resp.Code, resp.Body.String())
	}
}

func TestClientIPUsesUnifiedResolver(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		[]string{"192.0.2.0/24"},
	)
	s.AllowInsecureSetup = true
	router := s.Router()
	session := loginSessionTest(t, router)

	tests := []struct {
		name       string
		remote     string
		headers    map[string]string
		wantSubstr string
	}{
		{
			name:       "可信代理跳过可信中间跳",
			remote:     "192.0.2.30:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 192.0.2.30"},
			wantSubstr: `"client_ip":"203.0.113.9"`,
		},
		{
			name:       "非可信来源忽略转发头",
			remote:     "198.51.100.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			wantSubstr: `"client_ip":"198.51.100.1"`,
		},
		{
			name:       "仅 X-Real-IP 也可识别",
			remote:     "192.0.2.30:1234",
			headers:    map[string]string{"X-Real-IP": "203.0.113.10"},
			wantSubstr: `"client_ip":"203.0.113.10"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/api-keys/scopes", nil)
			req.RemoteAddr = test.remote
			req.AddCookie(session)
			for key, value := range test.headers {
				req.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("请求失败: %d %s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.wantSubstr) {
				t.Fatalf("客户端 IP 不正确，期望包含 %s: %s", test.wantSubstr, recorder.Body.String())
			}
		})
	}
}

func sessionRequestFromRemote(t *testing.T, router http.Handler, method, path, body, remoteAddr string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestAPIKeyScopesProxyDiagnostics(t *testing.T) {
	// 直连：使用 TCP 对端地址。
	directRouter, _ := newSessionTestServerWithStore(t)
	directSession := loginSessionTest(t, directRouter)
	direct := sessionRequest(t, directRouter, http.MethodGet, "/api/api-keys/scopes", "",
		[]*http.Cookie{directSession}, nil)
	if direct.Code != http.StatusOK ||
		!strings.Contains(direct.Body.String(), `"client_ip_source":"remote"`) ||
		!strings.Contains(direct.Body.String(), `"peer_trusted":false`) ||
		!strings.Contains(direct.Body.String(), `"has_forwarded":false`) {
		t.Fatalf("直连诊断不正确: %d %s", direct.Code, direct.Body.String())
	}

	// 可信代理：X-Forwarded-For 生效。
	trustedStore := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	trustedServer := New(
		trustedStore,
		auth.New([]byte("jwt-secret")),
		service.New(trustedStore),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		[]string{"192.0.2.0/24"},
	)
	trustedServer.AllowInsecureSetup = true
	trustedRouter := trustedServer.Router()
	trustedSession := loginSessionTest(t, trustedRouter)
	trusted := sessionRequest(t, trustedRouter, http.MethodGet, "/api/api-keys/scopes", "",
		[]*http.Cookie{trustedSession}, map[string]string{"X-Forwarded-For": "203.0.113.10"})
	if trusted.Code != http.StatusOK ||
		!strings.Contains(trusted.Body.String(), `"client_ip":"203.0.113.10"`) ||
		!strings.Contains(trusted.Body.String(), `"client_ip_source":"x-forwarded-for"`) ||
		!strings.Contains(trusted.Body.String(), `"peer_trusted":true`) ||
		!strings.Contains(trusted.Body.String(), `"has_forwarded":true`) {
		t.Fatalf("可信代理诊断不正确: %d %s", trusted.Code, trusted.Body.String())
	}

	// 非可信代理：伪造转发头不生效，但诊断应标记 has_forwarded。
	untrustedRouter, _ := newSessionTestServerWithStore(t)
	untrustedSession := loginSessionTest(t, untrustedRouter)
	untrusted := sessionRequestFromRemote(t, untrustedRouter, http.MethodGet, "/api/api-keys/scopes", "",
		"198.51.100.1:1234", []*http.Cookie{untrustedSession},
		map[string]string{"X-Forwarded-For": "203.0.113.10"})
	if untrusted.Code != http.StatusOK ||
		!strings.Contains(untrusted.Body.String(), `"client_ip":"198.51.100.1"`) ||
		!strings.Contains(untrusted.Body.String(), `"client_ip_source":"remote"`) ||
		!strings.Contains(untrusted.Body.String(), `"peer_trusted":false`) ||
		!strings.Contains(untrusted.Body.String(), `"has_forwarded":true`) {
		t.Fatalf("非可信代理诊断不正确: %d %s", untrusted.Code, untrusted.Body.String())
	}
}

func TestClientIPCloudflareAndMTLSModes(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		[]string{"192.0.2.0/24"},
	)
	s.ClientIPMode = "cloudflare"
	s.AllowInsecureSetup = true
	router := s.Router()
	session := loginSessionTest(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/api-keys/scopes", nil)
	req.RemoteAddr = "192.0.2.30:1234"
	req.AddCookie(session)
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"client_ip":"203.0.113.9"`) {
		t.Fatalf("Cloudflare 模式应优先 CF-Connecting-IP: %d %s", recorder.Code, recorder.Body.String())
	}

	// mTLS 已验证的连接不依赖 trusted_proxies 中的 IP。
	st2 := newSessionTestStore(t)
	s2 := New(
		st2,
		auth.New([]byte("jwt-secret")),
		service.New(st2),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	s2.AllowInsecureSetup = true
	router2 := s2.Router()
	session2 := loginSessionTest(t, router2)
	req = httptest.NewRequest(http.MethodGet, "/api/api-keys/scopes", nil)
	req.RemoteAddr = "198.51.100.1:1234"
	req.AddCookie(session2)
	req.Header.Set("X-Forwarded-For", "203.0.113.11")
	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	recorder = httptest.NewRecorder()
	router2.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"client_ip":"203.0.113.11"`) {
		t.Fatalf("mTLS 已验证代理应采信转发头: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIKeyIPRestriction(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyWithIPsForTest(t, router, session, "ip-restricted",
		[]string{apiScopeSystemRead}, []string{"203.0.113.10"})

	allowed := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.10", "")
	if allowed.Code != http.StatusOK {
		t.Fatalf("允许 IP 应可访问: %d %s", allowed.Code, allowed.Body.String())
	}
	denied := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.11", "")
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "api.error.api_key_ip_denied") {
		t.Fatalf("非允许 IP 应返回 403: %d %s", denied.Code, denied.Body.String())
	}

	// X-API-Key 请求头走同一套校验。
	allowedHeader := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.10", apiKeyHeader)
	if allowedHeader.Code != http.StatusOK {
		t.Fatalf("X-API-Key 允许 IP 应可访问: %d %s", allowedHeader.Code, allowedHeader.Body.String())
	}
	deniedHeader := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.11", apiKeyHeader)
	if deniedHeader.Code != http.StatusForbidden {
		t.Fatalf("X-API-Key 非允许 IP 应返回 403: %d %s", deniedHeader.Code, deniedHeader.Body.String())
	}

	// 空白名单密钥不限制来源 IP。
	unrestricted, _ := createAPIKeyForTest(t, router, session, "unrestricted", []string{apiScopeSystemRead})
	if got := apiKeyRequestFromIP(t, router, unrestricted, http.MethodGet, "/api/meta", "", "198.51.100.7", ""); got.Code != http.StatusOK {
		t.Fatalf("空白名单密钥不应限制 IP: %d %s", got.Code, got.Body.String())
	}
}

func TestAPIKeyIPDeniedDoesNotTouchLastUsed(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyWithIPsForTest(t, router, session, "touch",
		[]string{apiScopeSystemRead}, []string{"203.0.113.10"})

	denied := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.11", "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("非允许 IP 应被拒绝: %d %s", denied.Code, denied.Body.String())
	}
	key, err := st.GetAPIKeyByTokenHash(util.HashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if key.LastUsedAt != nil || key.LastUsedIP != "" {
		t.Fatal("IP 拒绝不应更新 last_used_at / last_used_ip")
	}

	allowed := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", "203.0.113.10", "")
	if allowed.Code != http.StatusOK {
		t.Fatalf("允许 IP 应可访问: %d %s", allowed.Code, allowed.Body.String())
	}
	key, err = st.GetAPIKeyByTokenHash(util.HashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if key.LastUsedAt == nil {
		t.Fatal("允许请求应更新 last_used_at")
	}
	if key.LastUsedIP != "203.0.113.10" {
		t.Fatalf("允许请求应记录 last_used_ip，实际 %q", key.LastUsedIP)
	}
	list := sessionRequest(t, router, http.MethodGet, "/api/api-keys", "",
		[]*http.Cookie{session}, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"last_used_ip":"203.0.113.10"`) {
		t.Fatalf("密钥列表应返回 last_used_ip: %d %s", list.Code, list.Body.String())
	}
}

func TestAPIKeyIPRestrictionCIDRAndIPv6(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyWithIPsForTest(t, router, session, "cidr",
		[]string{apiScopeSystemRead}, []string{"10.0.0.0/8", "2001:db8::/32"})

	for _, tc := range []struct {
		ip   string
		want int
	}{
		{"10.1.2.3", http.StatusOK},
		{"11.0.0.1", http.StatusForbidden},
		{"2001:db8::1", http.StatusOK},
		{"2001:db9::1", http.StatusForbidden},
	} {
		got := apiKeyRequestFromIP(t, router, token, http.MethodGet, "/api/meta", "", tc.ip, "")
		if got.Code != tc.want {
			t.Fatalf("IP %s 期望 %d，实际 %d %s", tc.ip, tc.want, got.Code, got.Body.String())
		}
	}
}

func TestAPIKeyIPRestrictionTrustedProxy(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		[]string{"192.0.2.0/24"},
	)
	s.AllowInsecureSetup = true
	router := s.Router()
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyWithIPsForTest(t, router, session, "proxy",
		[]string{apiScopeSystemRead}, []string{"203.0.113.10"})

	// 来自可信代理，X-Forwarded-For 生效。
	allowed := apiKeyRequestFromRemote(t, router, token, http.MethodGet, "/api/meta", "",
		"192.0.2.30:1234", map[string]string{"X-Forwarded-For": "203.0.113.10"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("可信代理转发头应生效: %d %s", allowed.Code, allowed.Body.String())
	}
	key, err := st.GetAPIKeyByTokenHash(util.HashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if key.LastUsedIP != "203.0.113.10" {
		t.Fatalf("last_used_ip 应为代理还原后的客户端 IP，实际 %q", key.LastUsedIP)
	}
	denied := apiKeyRequestFromRemote(t, router, token, http.MethodGet, "/api/meta", "",
		"192.0.2.30:1234", map[string]string{"X-Forwarded-For": "203.0.113.11"})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("可信代理转发的非允许 IP 应被拒绝: %d %s", denied.Code, denied.Body.String())
	}

	// 非可信来源即使伪造 X-Forwarded-For 也只看 RemoteAddr。
	untrusted := apiKeyRequestFromRemote(t, router, token, http.MethodGet, "/api/meta", "",
		"198.51.100.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.10"})
	if untrusted.Code != http.StatusForbidden {
		t.Fatalf("非可信来源不应采信转发头: %d %s", untrusted.Code, untrusted.Body.String())
	}
}

func TestAPIKeyIPPolicyCannotEscalate(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	managerToken, managerID := createAPIKeyWithIPsForTest(t, router, session, "manager",
		[]string{apiScopeAPIKeysRead, apiScopeAPIKeysWrite}, []string{"10.0.0.0/8"})

	// 省略 allowed_ips：继承调用者的白名单，允许创建。
	inheritBody := `{"name":"inherit","scopes":["api_keys:write"],"expires_at":""}`
	inherited := apiKeyRequestFromIP(t, router, managerToken, http.MethodPost, "/api/api-keys", inheritBody, "10.1.2.3", "")
	if inherited.Code != http.StatusOK {
		t.Fatalf("受限密钥创建子密钥应继承白名单: %d %s", inherited.Code, inherited.Body.String())
	}
	var inheritedPayload struct {
		Data struct {
			APIKey model.APIKey `json:"api_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(inherited.Body.Bytes(), &inheritedPayload); err != nil {
		t.Fatalf("解析继承白名单响应失败: %v", err)
	}
	if len(inheritedPayload.Data.APIKey.AllowedIPs) != 1 || inheritedPayload.Data.APIKey.AllowedIPs[0] != "10.0.0.0/8" {
		t.Fatalf("子密钥未继承白名单: %+v", inheritedPayload.Data.APIKey.AllowedIPs)
	}

	// 显式清空或放宽白名单必须被拒绝。
	for _, body := range []string{
		`{"name":"clear","scopes":["api_keys:write"],"expires_at":"","allowed_ips":[]}`,
		`{"name":"wider","scopes":["api_keys:write"],"expires_at":"","allowed_ips":["11.0.0.0/8"]}`,
		`{"name":"wider-v6","scopes":["api_keys:write"],"expires_at":"","allowed_ips":["2001:db8::/32"]}`,
	} {
		got := apiKeyRequestFromIP(t, router, managerToken, http.MethodPost, "/api/api-keys", body, "10.1.2.3", "")
		if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "api.error.api_key_ip_escalation") {
			t.Fatalf("受限密钥不应授予更宽白名单: %d %s", got.Code, got.Body.String())
		}
	}

	// 收紧到子网允许。
	subset := apiKeyRequestFromIP(t, router, managerToken, http.MethodPost, "/api/api-keys",
		`{"name":"subset","scopes":["api_keys:write"],"expires_at":"","allowed_ips":["10.1.0.0/16"]}`, "10.1.2.3", "")
	if subset.Code != http.StatusOK {
		t.Fatalf("受限密钥应能授予子网白名单: %d %s", subset.Code, subset.Body.String())
	}

	// 创建无 IP 限制的 peer，受限密钥不能轮换或修改它。
	_, peerID := createAPIKeyWithIPsForTest(t, router, session, "peer",
		[]string{apiScopeAPIKeysWrite}, nil)
	rotate := apiKeyRequestFromIP(t, router, managerToken, http.MethodPost,
		"/api/api-keys/"+itoa(peerID)+"/rotate", "", "10.1.2.3", "")
	if rotate.Code != http.StatusForbidden || !strings.Contains(rotate.Body.String(), "api.error.api_key_ip_target_forbidden") {
		t.Fatalf("受限密钥不应轮换无 IP 限制的密钥: %d %s", rotate.Code, rotate.Body.String())
	}

	// 受限密钥不能清空自身白名单，但可以保持原值。
	selfBody := `{"name":"manager","scopes":["api_keys:read","api_keys:write"],"expires_at":"","allowed_ips":[]}`
	selfClear := apiKeyRequestFromIP(t, router, managerToken, http.MethodPut,
		"/api/api-keys/"+itoa(managerID), selfBody, "10.1.2.3", "")
	if selfClear.Code != http.StatusForbidden || !strings.Contains(selfClear.Body.String(), "api.error.api_key_ip_escalation") {
		t.Fatalf("受限密钥不应清空自身白名单: %d %s", selfClear.Code, selfClear.Body.String())
	}
	selfKeep := apiKeyRequestFromIP(t, router, managerToken, http.MethodPut,
		"/api/api-keys/"+itoa(managerID),
		`{"name":"manager","scopes":["api_keys:read","api_keys:write"],"expires_at":"","allowed_ips":["10.0.0.0/8"]}`,
		"10.1.2.3", "")
	if selfKeep.Code != http.StatusOK {
		t.Fatalf("受限密钥应能保持自身白名单: %d %s", selfKeep.Code, selfKeep.Body.String())
	}
}

func TestAPIKeyIPValidation(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	invalid := []string{
		`{"name":"bad","scopes":["system:read"],"expires_at":"","allowed_ips":["not-an-ip"]}`,
		`{"name":"all-v4","scopes":["system:read"],"expires_at":"","allowed_ips":["0.0.0.0/0"]}`,
		`{"name":"all-v6","scopes":["system:read"],"expires_at":"","allowed_ips":["::/0"]}`,
	}
	for _, body := range invalid {
		got := sessionRequest(t, router, http.MethodPost, "/api/api-keys", body,
			[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
		if got.Code != http.StatusBadRequest {
			t.Fatalf("非法 IP 规则应返回 400: %d %s", got.Code, got.Body.String())
		}
	}

	many := make([]string, 0, apiKeyMaxAllowedIPs+1)
	for i := 0; i <= apiKeyMaxAllowedIPs; i++ {
		many = append(many, "10.0."+itoa(int64(i))+".1")
	}
	manyBody, _ := json.Marshal(map[string]any{
		"name": "many", "scopes": []string{apiScopeSystemRead}, "expires_at": "", "allowed_ips": many,
	})
	got := sessionRequest(t, router, http.MethodPost, "/api/api-keys", string(manyBody),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "api.error.api_key_ip_limit") {
		t.Fatalf("超过数量上限应返回 400: %d %s", got.Code, got.Body.String())
	}

	// 空白项和重复项会被规范化。
	dup := sessionRequest(t, router, http.MethodPost, "/api/api-keys",
		`{"name":"dup","scopes":["system:read"],"expires_at":"","allowed_ips":[" 203.0.113.10 ","203.0.113.10"]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if dup.Code != http.StatusOK {
		t.Fatalf("重复 IP 应被规范化而不是拒绝: %d %s", dup.Code, dup.Body.String())
	}
	var dupPayload struct {
		Data struct {
			APIKey model.APIKey `json:"api_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(dup.Body.Bytes(), &dupPayload); err != nil {
		t.Fatalf("解析规范化响应失败: %v", err)
	}
	if len(dupPayload.Data.APIKey.AllowedIPs) != 1 || dupPayload.Data.APIKey.AllowedIPs[0] != "203.0.113.10" {
		t.Fatalf("重复 IP 未规范化: %+v", dupPayload.Data.APIKey.AllowedIPs)
	}

	// 更新时省略字段保持原值，显式空数组清空。
	_, id := createAPIKeyWithIPsForTest(t, router, session, "update-ip",
		[]string{apiScopeSystemRead}, []string{"203.0.113.10"})
	keep := sessionRequest(t, router, http.MethodPut, "/api/api-keys/"+itoa(id),
		`{"name":"update-ip","scopes":["system:read"],"expires_at":""}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if keep.Code != http.StatusOK || !strings.Contains(keep.Body.String(), `"allowed_ips":["203.0.113.10"]`) {
		t.Fatalf("省略 allowed_ips 应保持原值: %d %s", keep.Code, keep.Body.String())
	}
	clear := sessionRequest(t, router, http.MethodPut, "/api/api-keys/"+itoa(id),
		`{"name":"update-ip","scopes":["system:read"],"expires_at":"","allowed_ips":[]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if clear.Code != http.StatusOK || !strings.Contains(clear.Body.String(), `"allowed_ips":[]`) {
		t.Fatalf("显式空数组应清空白名单: %d %s", clear.Code, clear.Body.String())
	}
}

func TestNormalizeAPIKeyAllowedIPs(t *testing.T) {
	out, err := normalizeAPIKeyAllowedIPs([]string{
		" 203.0.113.10 ",
		"10.0.0.1/8",
		"::ffff:198.51.100.7",
		"2001:db8::/32",
		"203.0.113.10",
		"",
	})
	if err != nil {
		t.Fatalf("规范化失败: %v", err)
	}
	want := []string{"10.0.0.0/8", "198.51.100.7", "2001:db8::/32", "203.0.113.10"}
	if len(out) != len(want) {
		t.Fatalf("规范化结果数量不正确: %v", out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("规范化结果不正确: %v，期望 %v", out, want)
		}
	}

	for _, input := range [][]string{{"not-an-ip"}, {"0.0.0.0/0"}, {"::/0"}, {"fe80::1%eth0"}} {
		if _, err := normalizeAPIKeyAllowedIPs(input); err == nil {
			t.Fatalf("非法规则应报错: %v", input)
		}
	}
}

func TestAPIKeyIPPolicySubset(t *testing.T) {
	cases := []struct {
		actor, target []string
		want          bool
	}{
		{nil, []string{"203.0.113.10"}, true},
		{[]string{"10.0.0.0/8"}, []string{"10.1.2.3"}, true},
		{[]string{"10.0.0.0/8"}, []string{"10.1.0.0/16"}, true},
		{[]string{"10.1.0.0/16"}, []string{"10.0.0.0/8"}, false},
		{[]string{"203.0.113.10"}, nil, false},
		{[]string{"203.0.113.10"}, []string{"203.0.113.11"}, false},
		{[]string{"2001:db8::/32"}, []string{"2001:db8::1"}, true},
		{[]string{"2001:db8::/32"}, []string{"2001:db9::1"}, false},
	}
	for _, tc := range cases {
		if got := apiKeyIPPolicySubset(tc.actor, tc.target); got != tc.want {
			t.Fatalf("subset(%v, %v) = %v，期望 %v", tc.actor, tc.target, got, tc.want)
		}
	}
}

func TestParseAPIKeyClientIP(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"203.0.113.10:1234", "203.0.113.10", true},
		{"203.0.113.10", "203.0.113.10", true},
		{"[2001:db8::1]:443", "2001:db8::1", true},
		{"2001:db8::1", "2001:db8::1", true},
		{"fe80::1%eth0", "fe80::1", true},
		{"", "", false},
		{"not-an-ip", "", false},
	}
	for _, tc := range cases {
		got, ok := parseAPIKeyClientIP(tc.raw)
		if ok != tc.ok {
			t.Fatalf("parse(%q) ok = %v，期望 %v", tc.raw, ok, tc.ok)
		}
		if ok && got.String() != tc.want {
			t.Fatalf("parse(%q) = %s，期望 %s", tc.raw, got, tc.want)
		}
	}
}

func TestAPIKeyIPAllowed(t *testing.T) {
	key := &model.APIKey{AllowedIPs: []string{"10.0.0.0/8", "2001:db8::1"}}
	if !apiKeyIPAllowed(key, netip.MustParseAddr("10.1.2.3")) {
		t.Fatal("CIDR 规则应命中")
	}
	if !apiKeyIPAllowed(key, netip.MustParseAddr("2001:db8::1")) {
		t.Fatal("单 IP 规则应命中")
	}
	if apiKeyIPAllowed(key, netip.MustParseAddr("2001:db8::2")) {
		t.Fatal("未命中规则不应放行")
	}
	if !apiKeyIPAllowed(&model.APIKey{}, netip.MustParseAddr("198.51.100.1")) {
		t.Fatal("空白名单应放行")
	}
}
