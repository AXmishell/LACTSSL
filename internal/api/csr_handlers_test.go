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
	"testing"

	"lactssl/internal/model"
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

func TestCSRRequestAPIKeyOwnership(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("acme.email", "admin@example.com"); err != nil {
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

	token, _ := createAPIKeyForTest(t, router, session, "csr-client", []string{apiScopeCertificatesRequest})
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
