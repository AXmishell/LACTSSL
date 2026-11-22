package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/util"
)

func TestAPIKeyPoliciesCoverAllRoutes(t *testing.T) {
	router := newSessionTestServer(t)
	existing := map[string]bool{}
	for _, route := range router.Routes() {
		if !strings.HasPrefix(route.Path, "/api") {
			continue
		}
		key := route.Method + " " + route.Path
		existing[key] = true
		if publicAPIRoutes[key] {
			continue
		}
		if _, found := apiKeyRoutePolicies[key]; !found {
			t.Errorf("受保护路由缺少 API 密钥权限策略: %s", key)
		}
	}
	for key := range apiKeyRoutePolicies {
		if !existing[key] {
			t.Errorf("API 密钥权限策略指向不存在的路由: %s", key)
		}
	}
}

func createAPIKeyForTest(t *testing.T, router http.Handler, session *http.Cookie, name string, scopes []string) (string, int64) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":       name,
		"scopes":     scopes,
		"enabled":    true,
		"expires_at": "",
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/api-keys", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("创建 API 密钥失败: %d %s", resp.Code, resp.Body.String())
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
	if decoded, err := base64.RawURLEncoding.DecodeString(payload.Data.Token); err != nil || len(decoded) != apiKeyTokenBytes {
		t.Fatalf("API 密钥令牌格式不正确: %q", payload.Data.Token)
	}
	if strings.HasPrefix(payload.Data.Token, legacyAPIKeyPrefix) {
		t.Fatalf("API 密钥令牌不应携带固定前缀: %q", payload.Data.Token)
	}
	if payload.Data.Token != strings.TrimSpace(payload.Data.Token) || len(payload.Data.Token) != apiKeyTokenLength {
		t.Fatalf("API 密钥令牌长度不正确: %q", payload.Data.Token)
	}
	if payload.Data.APIKey.ID == 0 {
		t.Fatal("API 密钥 ID 为空")
	}
	return payload.Data.Token, payload.Data.APIKey.ID
}

func apiKeyRequest(t *testing.T, router http.Handler, token, method, path, body string, headerName string) *httptestResponse {
	t.Helper()
	headers := map[string]string{}
	if headerName == apiKeyHeader {
		headers[apiKeyHeader] = token
	} else {
		headers["Authorization"] = "Bearer " + token
	}
	resp := sessionRequest(t, router, method, path, body, nil, headers)
	return &httptestResponse{Code: resp.Code, Body: resp.Body.String(), Cookies: resp.Result().Cookies()}
}

type httptestResponse struct {
	Code    int
	Body    string
	Cookies []*http.Cookie
}

func TestAPIKeyScopesAndRouteRestrictions(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	readToken, _ := createAPIKeyForTest(t, router, session, "read-only", []string{apiScopeCertificatesRead})

	allowed := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/certificates", "", "")
	if allowed.Code != http.StatusOK {
		t.Fatalf("拥有证书读取权限的密钥应能访问证书列表: %d %s", allowed.Code, allowed.Body)
	}
	if got := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusForbidden || !strings.Contains(got.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("缺少权限时应返回 403: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/auth/me", "", ""); got.Code != http.StatusForbidden {
		t.Fatalf("缺少 profile:read 时不应访问 /auth/me: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/api-keys", "", ""); got.Code != http.StatusForbidden {
		t.Fatalf("缺少 api_keys:read 时不应列出密钥: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/certificates", "", apiKeyHeader); got.Code != http.StatusOK {
		t.Fatalf("X-API-Key 请求头应可用: %d %s", got.Code, got.Body)
	}

	fullToken, _ := createAPIKeyForTest(t, router, session, "full-access", []string{apiScopeAll})
	if got := apiKeyRequest(t, router, fullToken, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusOK {
		t.Fatalf("全权限密钥应能访问系统信息: %d %s", got.Code, got.Body)
	}
	// 账户安全与会话管理仍只能通过浏览器登录会话操作。
	if got := apiKeyRequest(t, router, fullToken, http.MethodGet, "/api/sessions", "", ""); got.Code != http.StatusForbidden || !strings.Contains(got.Body, "api.error.api_key_route_forbidden") {
		t.Fatalf("会话接口不应允许 API 密钥调用: %d %s", got.Code, got.Body)
	}

	// 私钥导出需要单独的敏感权限。
	cert := &model.Certificate{
		PrimaryDomain: "api-key.example.com",
		CertPEM:       "CERT",
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	keyPath := "/api/certificates/" + itoa(cert.ID) + "/download?type=key"
	if got := apiKeyRequest(t, router, fullToken, http.MethodGet, keyPath, "", ""); got.Code != http.StatusOK || !strings.Contains(got.Body, "PRIVATE-KEY") {
		t.Fatalf("全权限密钥应能导出私钥: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, readToken, http.MethodGet, keyPath, "", ""); got.Code != http.StatusForbidden || !strings.Contains(got.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("仅证书读取权限不应导出私钥: %d %s", got.Code, got.Body)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, id := createAPIKeyForTest(t, router, session, "lifecycle", []string{apiScopeSystemRead})

	if got := apiKeyRequest(t, router, token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusOK {
		t.Fatalf("密钥创建后应可用: %d %s", got.Code, got.Body)
	}

	// 停用后立即失效。
	body := `{"name":"lifecycle","scopes":["system:read"],"enabled":false,"expires_at":""}`
	resp := sessionRequest(t, router, http.MethodPut, "/api/api-keys/"+itoa(id), body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("停用 API 密钥失败: %d %s", resp.Code, resp.Body.String())
	}
	if got := apiKeyRequest(t, router, token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusUnauthorized || !strings.Contains(got.Body, "api.error.api_key_disabled") {
		t.Fatalf("停用后的密钥应被拒绝: %d %s", got.Code, got.Body)
	}

	// 重新启用并轮换令牌，旧令牌失效、新令牌可用。
	body = `{"name":"lifecycle","scopes":["system:read"],"enabled":true,"expires_at":""}`
	resp = sessionRequest(t, router, http.MethodPut, "/api/api-keys/"+itoa(id), body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("启用 API 密钥失败: %d %s", resp.Code, resp.Body.String())
	}
	rotated := sessionRequest(t, router, http.MethodPost, "/api/api-keys/"+itoa(id)+"/rotate", "",
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if rotated.Code != http.StatusOK {
		t.Fatalf("轮换 API 密钥失败: %d %s", rotated.Code, rotated.Body.String())
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &payload); err != nil || payload.Data.Token == "" {
		t.Fatalf("解析轮换后的令牌失败: %v", err)
	}
	if got := apiKeyRequest(t, router, token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("轮换后旧令牌应失效: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, payload.Data.Token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusOK {
		t.Fatalf("轮换后新令牌应可用: %d %s", got.Code, got.Body)
	}

	// 删除后立即失效。
	deleted := sessionRequest(t, router, http.MethodDelete, "/api/api-keys/"+itoa(id), "",
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if deleted.Code != http.StatusOK {
		t.Fatalf("删除 API 密钥失败: %d %s", deleted.Code, deleted.Body.String())
	}
	if got := apiKeyRequest(t, router, payload.Data.Token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("删除后的密钥应失效: %d %s", got.Code, got.Body)
	}
}

func TestAPIKeyManagementUsesSessionAuth(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	body := `{"name":"session-key","scopes":["system:read"],"expires_at":""}`
	resp := sessionRequest(t, router, http.MethodPost, "/api/api-keys", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("登录会话应允许创建 API 密钥: %d %s", resp.Code, resp.Body.String())
	}
}

func TestAPIKeyCannotEscalateOwnScopes(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyForTest(t, router, session, "key-manager", []string{apiScopeAPIKeysWrite})

	body := `{"name":"escalated","scopes":["system:read"],"expires_at":""}`
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/api-keys", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_scope_escalation") {
		t.Fatalf("API 密钥不应授予自身没有的权限: %d %s", resp.Code, resp.Body)
	}

	allowed := `{"name":"same-scope","scopes":["api_keys:write"],"expires_at":""}`
	resp = apiKeyRequest(t, router, token, http.MethodPost, "/api/api-keys", allowed, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("API 密钥应能授予自身已有权限: %d %s", resp.Code, resp.Body)
	}
}

func TestAPIKeyCannotManageBroaderKey(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	adminToken, adminID := createAPIKeyForTest(t, router, session, "admin-key", []string{apiScopeAll})
	lowToken, _ := createAPIKeyForTest(t, router, session, "key-manager", []string{apiScopeAPIKeysWrite})
	path := "/api/api-keys/" + itoa(adminID)

	// 轮换高权限密钥会直接返回它的新令牌，必须拒绝。
	rotated := apiKeyRequest(t, router, lowToken, http.MethodPost, path+"/rotate", "", "")
	if rotated.Code != http.StatusForbidden || !strings.Contains(rotated.Body, "api.error.api_key_target_forbidden") {
		t.Fatalf("低权限密钥不应轮换高权限密钥: %d %s", rotated.Code, rotated.Body)
	}

	// 修改时提交的权限本身在调用者范围内，确保拦截来自目标密钥范围校验。
	body := `{"name":"hijacked","scopes":["api_keys:write"],"expires_at":""}`
	updated := apiKeyRequest(t, router, lowToken, http.MethodPut, path, body, "")
	if updated.Code != http.StatusForbidden || !strings.Contains(updated.Body, "api.error.api_key_target_forbidden") {
		t.Fatalf("低权限密钥不应修改高权限密钥: %d %s", updated.Code, updated.Body)
	}

	deleted := apiKeyRequest(t, router, lowToken, http.MethodDelete, path, "", "")
	if deleted.Code != http.StatusForbidden || !strings.Contains(deleted.Body, "api.error.api_key_target_forbidden") {
		t.Fatalf("低权限密钥不应删除高权限密钥: %d %s", deleted.Code, deleted.Body)
	}

	// 高权限密钥应保持可用，且名称没有被修改。
	if got := apiKeyRequest(t, router, adminToken, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusOK {
		t.Fatalf("高权限密钥应在拒绝操作后仍然可用: %d %s", got.Code, got.Body)
	}
	list := sessionRequest(t, router, http.MethodGet, "/api/api-keys", "", []*http.Cookie{session}, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"admin-key"`) {
		t.Fatalf("高权限密钥不应被修改: %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), `"name":"hijacked"`) {
		t.Fatalf("高权限密钥名称被越权修改: %s", list.Body.String())
	}
}

func TestAPIKeyCanManagePeerKeyWithSameScopes(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	managerToken, _ := createAPIKeyForTest(t, router, session, "manager", []string{apiScopeAPIKeysWrite})
	_, peerID := createAPIKeyForTest(t, router, session, "peer", []string{apiScopeAPIKeysWrite})
	path := "/api/api-keys/" + itoa(peerID)

	rotated := apiKeyRequest(t, router, managerToken, http.MethodPost, path+"/rotate", "", "")
	if rotated.Code != http.StatusOK {
		t.Fatalf("同权限密钥应允许轮换: %d %s", rotated.Code, rotated.Body)
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(rotated.Body), &payload); err != nil || payload.Data.Token == "" {
		t.Fatalf("解析轮换后的令牌失败: %v %s", err, rotated.Body)
	}

	created := apiKeyRequest(t, router, payload.Data.Token, http.MethodPost,
		"/api/api-keys", `{"name":"child","scopes":["api_keys:write"],"expires_at":""}`, "")
	if created.Code != http.StatusOK {
		t.Fatalf("轮换后的同权限令牌应可用: %d %s", created.Code, created.Body)
	}

	deleted := apiKeyRequest(t, router, managerToken, http.MethodDelete, path, "", "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("同权限密钥应允许删除: %d %s", deleted.Code, deleted.Body)
	}
	if got := apiKeyRequest(t, router, payload.Data.Token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("删除后的密钥令牌应立即失效: %d %s", got.Code, got.Body)
	}
}

func TestExpiredAPIKeyIsRejected(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	loginSessionTest(t, router)
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	token, err := newAPIKeyToken()
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	if err := st.CreateAPIKey(&model.APIKey{
		UserID:    user.ID,
		Name:      "expired",
		Prefix:    apiKeyPrefixOf(token),
		TokenHash: util.HashToken(token),
		Scopes:    []string{apiScopeAll},
		Enabled:   true,
		ExpiresAt: &expired,
	}); err != nil {
		t.Fatal(err)
	}
	if got := apiKeyRequest(t, router, token, http.MethodGet, "/api/meta", "", ""); got.Code != http.StatusUnauthorized || !strings.Contains(got.Body, "api.error.api_key_expired") {
		t.Fatalf("过期密钥应被拒绝: %d %s", got.Code, got.Body)
	}
}

func TestDeprecatedBackupScopesAreSessionOnly(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	scopes := sessionRequest(t, router, http.MethodGet, "/api/api-keys/scopes", "",
		[]*http.Cookie{session}, nil)
	if scopes.Code != http.StatusOK {
		t.Fatalf("读取权限列表失败: %d %s", scopes.Code, scopes.Body.String())
	}
	for _, deprecated := range []string{apiScopeBackupDownload, apiScopeBackupRestore} {
		if strings.Contains(scopes.Body.String(), `"value":"`+deprecated+`"`) {
			t.Fatalf("新建密钥不应再提供已废弃的 %s 权限", deprecated)
		}
	}
	createBody := `{"name":"new-backup","scopes":["` + apiScopeBackupDownload + `"],"enabled":true,"expires_at":""}`
	createResp := sessionRequest(t, router, http.MethodPost, "/api/api-keys", createBody,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if createResp.Code != http.StatusBadRequest || !strings.Contains(createResp.Body.String(), "api.error.api_key_scope_invalid") {
		t.Fatalf("新建密钥不应接受已废弃 scope: %d %s", createResp.Code, createResp.Body.String())
	}

	// 历史密钥仍能保留该 scope，但对应路由只允许浏览器会话。
	token, _ := createAPIKeyForTest(t, router, session, "backup-reader", []string{apiScopeAll})
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret"}`, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_route_forbidden") {
		t.Fatalf("备份下载应只允许浏览器会话: %d %s", resp.Code, resp.Body)
	}
}
