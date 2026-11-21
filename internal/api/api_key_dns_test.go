package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"lactssl/internal/model"
)

func createAPIKeyWithDNSAccountsForTest(t *testing.T, router http.Handler, session *http.Cookie, name string, scopes []string, allowed []int64) (string, int64) {
	t.Helper()
	payload := map[string]any{
		"name":       name,
		"scopes":     scopes,
		"enabled":    true,
		"expires_at": "",
	}
	if allowed != nil {
		payload["allowed_dns_account_ids"] = allowed
	}
	body, _ := json.Marshal(payload)
	resp := sessionRequest(t, router, http.MethodPost, "/api/api-keys", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("创建带 DNS 限制的 API 密钥失败: %d %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Data struct {
			APIKey model.APIKey `json:"api_key"`
			Token  string       `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("解析 API 密钥响应失败: %v", err)
	}
	return result.Data.Token, result.Data.APIKey.ID
}

func TestAPIKeyDNSAccountRestrictionOnCSR(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com, other.net"); err != nil {
		t.Fatal(err)
	}
	allowed := &model.DNSAccount{Name: "allowed", Provider: "test", Config: map[string]string{}}
	other := &model.DNSAccount{Name: "other", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{allowed, other} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceDNSZoneBindings(allowed.ID, []model.DNSZoneBinding{
		{DNSAccountID: allowed.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(other.ID, []model.DNSZoneBinding{
		{DNSAccountID: other.ID, Zone: "other.net", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	token, _ := createAPIKeyWithDNSAccountsForTest(t, router, session, "csr-dns-limited",
		[]string{apiScopeCertificatesRequest, apiScopeDNSRead, apiScopeTasksWrite}, []int64{allowed.ID})

	explicitOther, _ := json.Marshal(map[string]any{
		"csr":            testCSRPEM(t, "other.net"),
		"ca":             "letsencrypt",
		"dns_account_id": other.ID,
	})
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/certificate-requests", string(explicitOther), "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_dns_account_denied") {
		t.Fatalf("显式使用未授权账户应拒绝: %d %s", resp.Code, resp.Body)
	}

	// 跨账户任务的自动路由也必须在密钥允许集合内。
	autoMixed, _ := json.Marshal(map[string]any{
		"name":     "mixed-task",
		"domains":  []string{"example.com", "other.net"},
		"ca":       "letsencrypt",
		"key_type": "ecdsa",
		"enabled":  false,
	})
	resp = apiKeyRequest(t, router, token, http.MethodPost, "/api/tasks", string(autoMixed), "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_dns_account_denied") {
		t.Fatalf("跨账户任务不应越过密钥 DNS 范围: %d %s", resp.Code, resp.Body)
	}

	// 自动解析只能看到允许集合内的绑定，因此 other.net 应视为未绑定。
	autoOther, _ := json.Marshal(map[string]any{"csr": testCSRPEM(t, "other.net"), "ca": "letsencrypt"})
	resp = apiKeyRequest(t, router, token, http.MethodPost, "/api/certificate-requests", string(autoOther), "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "service.csr.dns_account_unresolved") {
		t.Fatalf("自动解析不应落到未授权账户: %d %s", resp.Code, resp.Body)
	}

	autoAllowed, _ := json.Marshal(map[string]any{"csr": testCSRPEM(t, "example.com"), "ca": "letsencrypt"})
	resp = apiKeyRequest(t, router, token, http.MethodPost, "/api/certificate-requests", string(autoAllowed), "")
	if resp.Code != http.StatusAccepted {
		t.Fatalf("允许集合内的自动解析应成功: %d %s", resp.Code, resp.Body)
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.DNSAccountID != allowed.ID {
		t.Fatalf("解析到了错误的 DNS 账户: %+v", payload.Data)
	}
}

func TestAPIKeyDNSAccountRestrictionOnAccountEndpoints(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	allowed := &model.DNSAccount{Name: "allowed", Provider: "test", Config: map[string]string{}}
	other := &model.DNSAccount{Name: "other", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{allowed, other} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	token, _ := createAPIKeyWithDNSAccountsForTest(t, router, session, "dns-reader",
		[]string{apiScopeDNSRead}, []int64{allowed.ID})

	list := apiKeyRequest(t, router, token, http.MethodGet, "/api/dns-accounts", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body, `"name":"allowed"`) || strings.Contains(list.Body, `"name":"other"`) {
		t.Fatalf("账户列表应只包含允许账户: %d %s", list.Code, list.Body)
	}
	hidden := apiKeyRequest(t, router, token, http.MethodGet, "/api/dns-accounts/"+strconv.FormatInt(other.ID, 10), "", "")
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("未授权账户应按不存在处理: %d %s", hidden.Code, hidden.Body)
	}
	zones := apiKeyRequest(t, router, token, http.MethodGet, "/api/dns-accounts/"+strconv.FormatInt(other.ID, 10)+"/zones", "", "")
	if zones.Code != http.StatusNotFound {
		t.Fatalf("未授权账户的绑定应按不存在处理: %d %s", zones.Code, zones.Body)
	}
}

func TestAPIKeyDNSAccountEscalationPrevented(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{first, second} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	manager, _ := createAPIKeyWithDNSAccountsForTest(t, router, session, "dns-manager",
		[]string{apiScopeAPIKeysRead, apiScopeAPIKeysWrite, apiScopeDNSRead}, []int64{first.ID})

	// 受限密钥不能创建更宽或不受限的密钥。
	for _, body := range []string{
		`{"name":"wider","scopes":["dns_accounts:read"],"allowed_dns_account_ids":[` + strconv.FormatInt(first.ID, 10) + `,` + strconv.FormatInt(second.ID, 10) + `]}`,
		`{"name":"unrestricted","scopes":["dns_accounts:read"],"allowed_dns_account_ids":[]}`,
	} {
		resp := apiKeyRequest(t, router, manager, http.MethodPost, "/api/api-keys", body, "")
		if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_dns_escalation") {
			t.Fatalf("受限密钥不应扩大 DNS 范围: %d %s", resp.Code, resp.Body)
		}
	}

	narrowBody := `{"name":"narrow","scopes":["dns_accounts:read"],"allowed_dns_account_ids":[` + strconv.FormatInt(first.ID, 10) + `]}`
	narrow := apiKeyRequest(t, router, manager, http.MethodPost, "/api/api-keys", narrowBody, "")
	if narrow.Code != http.StatusOK {
		t.Fatalf("受限密钥应能创建同等范围的密钥: %d %s", narrow.Code, narrow.Body)
	}

	// 受限密钥不能管理或轮换范围更宽的密钥。
	_, unrestrictedID := createAPIKeyForTest(t, router, session, "unrestricted", []string{apiScopeDNSRead})
	rotated := apiKeyRequest(t, router, manager, http.MethodPost, "/api/api-keys/"+strconv.FormatInt(unrestrictedID, 10)+"/rotate", "", "")
	if rotated.Code != http.StatusForbidden || !strings.Contains(rotated.Body, "api.error.api_key_dns_target_forbidden") {
		t.Fatalf("受限密钥不应轮换更宽的密钥: %d %s", rotated.Code, rotated.Body)
	}

	invalid := apiKeyRequest(t, router, manager, http.MethodPost, "/api/api-keys",
		`{"name":"bad","scopes":["dns_accounts:read"],"allowed_dns_account_ids":[999999]}`, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "api.error.api_key_dns_account_invalid") {
		t.Fatalf("不存在的账户 ID 应拒绝保存: %d %s", invalid.Code, invalid.Body)
	}
}

func TestAPIKeyDNSAccountRestrictionOnTasks(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	allowed := &model.DNSAccount{Name: "allowed", Provider: "test", Config: map[string]string{}}
	other := &model.DNSAccount{Name: "other", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{allowed, other} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	token, _ := createAPIKeyWithDNSAccountsForTest(t, router, session, "task-dns-limited",
		[]string{apiScopeTasksWrite, apiScopeTasksRead, apiScopeDNSRead}, []int64{allowed.ID})

	disallowedBody := `{"name":"bad-task","domains":["example.com"],"dns_account_id":` + strconv.FormatInt(other.ID, 10) + `,"ca":"letsencrypt","key_type":"ecdsa","enabled":false}`
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/tasks", disallowedBody, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_dns_account_denied") {
		t.Fatalf("任务不应使用未授权账户: %d %s", resp.Code, resp.Body)
	}

	allowedBody := `{"name":"ok-task","domains":["example.com"],"dns_account_id":` + strconv.FormatInt(allowed.ID, 10) + `,"ca":"letsencrypt","key_type":"ecdsa","enabled":false}`
	resp = apiKeyRequest(t, router, token, http.MethodPost, "/api/tasks", allowedBody, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("任务应能使用允许账户: %d %s", resp.Code, resp.Body)
	}

	// 直接写入并运行一个使用未授权账户的任务，运行入口也必须收紧。
	task := &model.Task{
		Name: "existing", Domains: []string{"example.com"}, Challenge: "dns01",
		DNSAccountID: other.ID, CA: "letsencrypt", KeyType: "ecdsa", RenewDays: 30,
		Enabled: false, Status: "pending",
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	runToken, _ := createAPIKeyWithDNSAccountsForTest(t, router, session, "task-run-limited",
		[]string{apiScopeTasksRun, apiScopeDNSRead}, []int64{allowed.ID})
	run := apiKeyRequest(t, router, runToken, http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/run", "", "")
	if run.Code != http.StatusForbidden || !strings.Contains(run.Body, "api.error.api_key_dns_account_denied") {
		t.Fatalf("运行任务应校验 DNS 账户范围: %d %s", run.Code, run.Body)
	}
}
