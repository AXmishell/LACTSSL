package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

func useTestBaotaDeployType(t *testing.T) {
	t.Helper()
	original := service.DeployTypes
	service.DeployTypes = append(append([]service.ProviderDef{}, original...), service.ProviderDef{
		Value: "baota",
		DeployFields: []service.FieldDef{
			{Key: "target", Type: "select", Default: "site"},
			{
				Key:      "sites",
				Type:     "text",
				Multiple: true,
				ShowIf:   &service.Condition{Key: "target", Values: []string{"site", "docker"}},
			},
		},
	})
	t.Cleanup(func() { service.DeployTypes = original })
}

func createBatchTestCertificate(t *testing.T, st interface {
	CreateCertificate(*model.Certificate) error
}, domains ...string) *model.Certificate {
	t.Helper()
	cert := &model.Certificate{
		PrimaryDomain: domains[0],
		Domains:       domains,
		KeyAvailable:  true,
		CertPEM:       "CERT",
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestBatchDeployRejectsMismatchedSite(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)

	certA := createBatchTestCertificate(t, st, "a.example.com")
	certB := createBatchTestCertificate(t, st, "b.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{"sites":"a.example.com"}}]}`,
		certA.ID, certB.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "service.deploy.batch_site_mismatch") {
		t.Fatalf("mismatched site should be rejected: %d %s", response.Code, response.Body.String())
	}
	records, err := st.ListDeployRecords(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("no deployment should run before preflight passes: %+v", records)
	}
}

func TestBatchDeployRejectsDuplicateSite(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)

	certA := createBatchTestCertificate(t, st, "same.example.com")
	certB := createBatchTestCertificate(t, st, "same.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	// 站点留空时插件会回退到证书域名，两张同域名证书仍会写入同一个站点。
	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{}}]}`,
		certA.ID, certB.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "api.error.batch_deploy_site_conflict") {
		t.Fatalf("duplicate site should be rejected: %d %s", response.Code, response.Body.String())
	}
	records, err := st.ListDeployRecords(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("conflicting batch must not write any target: %+v", records)
	}
}

func TestBatchDeployRunsEachCertificate(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	certA := createBatchTestCertificate(t, st, "a.example.com")
	certB := createBatchTestCertificate(t, st, "b.example.com")
	account := &model.DeployAccount{Name: "local", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{
		"cert_path":%q,"key_path":%q}}]}`,
		certA.ID, certB.ID, account.ID,
		filepath.Join(dir, "cert.pem"),
		filepath.Join(dir, "key.pem"),
	)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("batch deploy failed: %d %s", response.Code, response.Body.String())
	}
	records, err := st.ListDeployRecords(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected two deploy records, got %+v", records)
	}
	if !strings.Contains(response.Body.String(), `"domain":"a.example.com"`) ||
		!strings.Contains(response.Body.String(), `"domain":"b.example.com"`) {
		t.Fatalf("results should identify each certificate: %s", response.Body.String())
	}
}
