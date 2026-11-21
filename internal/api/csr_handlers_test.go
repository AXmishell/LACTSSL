package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

func testCSRPEM(t *testing.T, domains ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestCSRRequestRejectsInvalidCSR(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests",
		`{"csr":"not-a-csr","ca":"letsencrypt","dns_account_id":1}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("非法 CSR 应返回 400: %d %s", resp.Code, resp.Body.String())
	}
	if !containsCode(resp.Body.Bytes(), "service.csr.decode_failed") {
		t.Fatalf("应返回 CSR 解析错误: %s", resp.Body.String())
	}
}

func TestCSRRequestCreatesPendingRequest(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{Name: "test-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}

	csr := testCSRPEM(t, "example.com", "www.example.com")
	body, _ := json.Marshal(map[string]any{
		"csr":             csr,
		"ca":              "letsencrypt",
		"challenge":       "dns01",
		"dns_account_id":  account.ID,
		"idempotency_key": "test-idempotency",
		"wait_seconds":    0,
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("创建 CSR 申请应返回 202: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ID == "" || payload.Data.Status != model.CSRStatusPending {
		t.Fatalf("申请响应不正确: %+v", payload.Data)
	}
	if len(payload.Data.Identifiers) != 2 || payload.Data.PrimaryDomain != "example.com" {
		t.Fatalf("申请域名不正确: %+v", payload.Data)
	}

	// 相同幂等键与相同 CSR 应返回原申请，而不是重复创建。
	second := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if second.Code != http.StatusAccepted {
		t.Fatalf("重复幂等请求应返回原申请: %d %s", second.Code, second.Body.String())
	}
	var secondPayload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if secondPayload.Data.ID != payload.Data.ID {
		t.Fatalf("幂等请求返回了不同申请: %s != %s", secondPayload.Data.ID, payload.Data.ID)
	}

	// 证书尚未签发，不能下载。
	download := sessionRequest(t, router, http.MethodGet, "/api/certificate-requests/"+payload.Data.ID+"/certificate", "", []*http.Cookie{session}, nil)
	if download.Code != http.StatusConflict {
		t.Fatalf("未签发证书应返回 409: %d %s", download.Code, download.Body.String())
	}
}

func TestCSRRequestPrivateCA(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{
		Name:      "Internal CA",
		KeyType:   "ecdsa",
		ValidDays: 365,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}

	// 私有 CA 不使用 DNS-01，也不需要预先配置 ACME 邮箱和允许域名；
	// 内网域名应可以直接申请。
	body, _ := json.Marshal(map[string]any{
		"csr":             testCSRPEM(t, "app.internal", "api.internal"),
		"private_ca_id":   ca.ID,
		"idempotency_key": "private-ca-test",
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("私有 CA 申请应返回 202: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.CA != service.PrivateCAValue(ca.ID) {
		t.Fatalf("私有 CA 引用不正确: %+v", payload.Data)
	}
	if payload.Data.Challenge != "private" || payload.Data.DNSAccountID != 0 {
		t.Fatalf("私有 CA 申请不应使用 DNS-01: %+v", payload.Data)
	}
	if len(payload.Data.Identifiers) != 2 || payload.Data.PrimaryDomain != "app.internal" {
		t.Fatalf("私有 CA 申请域名不正确: %+v", payload.Data)
	}

	// 仅持有 certificates:request 的密钥不能借用私有 CA。
	requestOnly, _ := createAPIKeyForTest(t, router, session, "csr-private-denied",
		[]string{apiScopeCertificatesRequest})
	deniedBody, _ := json.Marshal(map[string]any{
		"csr":           testCSRPEM(t, "app.internal"),
		"private_ca_id": ca.ID,
	})
	denied := apiKeyRequest(t, router, requestOnly, http.MethodPost, "/api/certificate-requests", string(deniedBody), "")
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body, apiScopePrivateCAWrite) {
		t.Fatalf("私有 CA 申请应要求 private_cas:write: %d %s", denied.Code, denied.Body)
	}

	allowedKey, _ := createAPIKeyForTest(t, router, session, "csr-private-allowed",
		[]string{apiScopeCertificatesRequest, apiScopePrivateCAWrite})
	allowedBody, _ := json.Marshal(map[string]any{
		"csr":           testCSRPEM(t, "app.internal"),
		"ca":            service.PrivateCAValue(ca.ID),
		"private_ca_id": ca.ID,
	})
	allowed := apiKeyRequest(t, router, allowedKey, http.MethodPost, "/api/certificate-requests", string(allowedBody), "")
	if allowed.Code != http.StatusAccepted {
		t.Fatalf("具备 private_cas:write 时应允许私有 CA 申请: %d %s", allowed.Code, allowed.Body)
	}
}

func TestCSRRequestAPIKeyOwnership(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{Name: "test-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	csr := testCSRPEM(t, "example.com")
	body, _ := json.Marshal(map[string]any{
		"csr":            csr,
		"ca":             "letsencrypt",
		"dns_account_id": account.ID,
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("会话创建申请失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	token, _ := createAPIKeyForTest(t, router, session, "csr-client", []string{apiScopeCertificatesRequest, apiScopeDNSRead})
	// 其他 API 密钥不能读取会话创建的申请。
	denied := apiKeyRequest(t, router, token, http.MethodGet, "/api/certificate-requests/"+payload.Data.ID, "", "")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("跨密钥访问应返回 404: %d %s", denied.Code, denied.Body)
	}

	// 该密钥自己创建的申请可以读取。
	ownResp := apiKeyRequest(t, router, token, http.MethodPost, "/api/certificate-requests", string(body), "")
	if ownResp.Code != http.StatusAccepted {
		t.Fatalf("API 密钥创建申请失败: %d %s", ownResp.Code, ownResp.Body)
	}
	var own struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal([]byte(ownResp.Body), &own); err != nil {
		t.Fatal(err)
	}
	allowed := apiKeyRequest(t, router, token, http.MethodGet, "/api/certificate-requests/"+own.Data.ID, "", "")
	if allowed.Code != http.StatusOK {
		t.Fatalf("API 密钥读取自己的申请失败: %d %s", allowed.Code, allowed.Body)
	}
}

func containsCode(body []byte, code string) bool {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Code == code
}

func TestCSRRequestRequiresAllowlist(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{Name: "test-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"csr":            testCSRPEM(t, "example.com"),
		"ca":             "letsencrypt",
		"dns_account_id": account.ID,
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusForbidden || !containsCode(resp.Body.Bytes(), "service.csr.allowed_domains_required") {
		t.Fatalf("未配置允许域名时应默认拒绝: %d %s", resp.Code, resp.Body.String())
	}
}

func TestCSRRequestAPIKeyRequiresDNSAccountScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{Name: "test-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"csr":            testCSRPEM(t, "example.com"),
		"ca":             "letsencrypt",
		"dns_account_id": account.ID,
	})
	token, _ := createAPIKeyForTest(t, router, session, "csr-only", []string{apiScopeCertificatesRequest})
	resp := apiKeyRequest(t, router, token, http.MethodPost, "/api/certificate-requests", string(body), "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeDNSRead) {
		t.Fatalf("仅 certificates:request 不应借用 DNS 账户: %d %s", resp.Code, resp.Body)
	}
}

func TestCSRRequestResolvesDNSAccountFromBindings(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{Name: "resolved-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, []model.DNSZoneBinding{
		{DNSAccountID: account.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"csr":             testCSRPEM(t, "example.com", "www.example.com"),
		"ca":              "letsencrypt",
		"idempotency_key": "auto-resolve",
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("自动解析账户应创建申请: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.DNSAccountID != account.ID || payload.Data.ResolvedBy != "binding" || payload.Data.ResolvedZone != "example.com" {
		t.Fatalf("自动解析结果不正确: %+v", payload.Data)
	}
}

func TestCSRRequestUnresolvedDNSAccountFailsClosed(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"csr": testCSRPEM(t, "example.com"),
		"ca":  "letsencrypt",
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusBadRequest || !containsCode(resp.Body.Bytes(), "service.csr.dns_account_unresolved") {
		t.Fatalf("未绑定域名应默认拒绝: %d %s", resp.Code, resp.Body.String())
	}
}

func TestCSRRequestAcrossDNSAccountsUsesRoutes(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("csr.allowed_domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(first); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDNSAccount(second); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(first.ID, []model.DNSZoneBinding{
		{DNSAccountID: first.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(second.ID, []model.DNSZoneBinding{
		{DNSAccountID: second.ID, Zone: "sub.example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"csr": testCSRPEM(t, "example.com", "a.sub.example.com"),
		"ca":  "letsencrypt",
	})
	resp := sessionRequest(t, router, http.MethodPost, "/api/certificate-requests", string(body),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("跨账户 CSR 应接受并按域名路由: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data csrRequestView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.DNSAccountID != 0 || len(payload.Data.DNSRoutes) != 2 {
		t.Fatalf("跨账户 CSR 应保存两条路由: %+v", payload.Data)
	}
	if payload.Data.DNSRoutes[0].DNSAccountID != first.ID || payload.Data.DNSRoutes[1].DNSAccountID != second.ID {
		t.Fatalf("DNS 路由账户不正确: %+v", payload.Data.DNSRoutes)
	}
}

func TestDNSZoneEndpointsAndResolvePreview(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	account := &model.DNSAccount{Name: "preview-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}

	body := `{"zones":[{"zone":"Example.COM.","match_mode":"suffix"}]}`
	resp := sessionRequest(t, router, http.MethodPut, "/api/dns-accounts/"+strconv.FormatInt(account.ID, 10)+"/zones", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("保存域名绑定失败: %d %s", resp.Code, resp.Body.String())
	}

	listResp := sessionRequest(t, router, http.MethodGet, "/api/dns-accounts/"+strconv.FormatInt(account.ID, 10)+"/zones", "",
		[]*http.Cookie{session}, nil)
	if listResp.Code != http.StatusOK || !strings.Contains(listResp.Body.String(), "example.com") {
		t.Fatalf("读取域名绑定失败: %d %s", listResp.Code, listResp.Body.String())
	}

	resolveResp := sessionRequest(t, router, http.MethodPost, "/api/dns-accounts/resolve",
		`{"domains":["a.example.com"]}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resolveResp.Code != http.StatusOK || !strings.Contains(resolveResp.Body.String(), `"account_id":`+strconv.FormatInt(account.ID, 10)) {
		t.Fatalf("解析预览失败: %d %s", resolveResp.Code, resolveResp.Body.String())
	}
}
