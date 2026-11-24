package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/host/deploy"
	"lactssl/internal/service"
)

type fakeBatchPlanner struct {
	plan     deployplugin.PlanResult
	planErr  error
	deployed []deployplugin.Request
}

func (f *fakeBatchPlanner) ID() string        { return "baota" }
func (f *fakeBatchPlanner) Aliases() []string { return nil }
func (f *fakeBatchPlanner) Source() deployplugin.Source {
	return deployplugin.SourceExec
}
func (f *fakeBatchPlanner) Deploy(_ context.Context, req deployplugin.Request) error {
	f.deployed = append(f.deployed, req)
	return nil
}
func (f *fakeBatchPlanner) Plan(_ context.Context, _ deployplugin.PlanRequest) (deployplugin.PlanResult, error) {
	return f.plan, f.planErr
}

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

func setupFakePlanner(t *testing.T, objects ...string) *fakeBatchPlanner {
	t.Helper()
	plan := deployplugin.PlanResult{Field: "sites", Multiple: true}
	for _, value := range objects {
		plan.Objects = append(plan.Objects, deployplugin.PlanObject{Value: value, Name: value, Domains: []string{value}})
	}
	fake := &fakeBatchPlanner{plan: plan}
	if err := deployplugin.RegisterExternal(fake); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })
	return fake
}

func createBatchTestCertificate(t *testing.T, st interface {
	CreateCertificate(*model.Certificate) error
}, domains ...string) *model.Certificate {
	t.Helper()
	certPEM, fullchainPEM := testCertificateChainPEM(t)
	cert := &model.Certificate{
		PrimaryDomain: domains[0],
		Domains:       domains,
		KeyAvailable:  true,
		CertPEM:       certPEM,
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  fullchainPEM,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestBatchDeployAutoMatchesSites(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)
	fake := setupFakePlanner(t, "a.example.com", "b.example.com")

	certA := createBatchTestCertificate(t, st, "a.example.com")
	certB := createBatchTestCertificate(t, st, "b.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{}}]}`,
		certA.ID, certB.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("batch deploy failed: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"deployed"`) ||
		!strings.Contains(response.Body.String(), `"summary":{"conflict":0,"deployed":2,"failed":0,"skipped":0}`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if len(fake.deployed) != 2 {
		t.Fatalf("expected two deployments, got %d", len(fake.deployed))
	}
	if fake.deployed[0].Config["sites"] != "a.example.com" || fake.deployed[1].Config["sites"] != "b.example.com" {
		t.Fatalf("unexpected site configs: %+v", fake.deployed)
	}
}

func TestBatchDeploySkipsUnmatchedSite(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)
	fake := setupFakePlanner(t, "a.example.com")

	certA := createBatchTestCertificate(t, st, "a.example.com")
	certC := createBatchTestCertificate(t, st, "c.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{}}]}`,
		certA.ID, certC.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("batch deploy failed: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"skipped"`) ||
		!strings.Contains(response.Body.String(), `"skipped":1`) {
		t.Fatalf("unmatched certificate should be skipped: %s", response.Body.String())
	}
	if len(fake.deployed) != 1 {
		t.Fatalf("only matched certificate should deploy, got %d", len(fake.deployed))
	}
}

func TestBatchDeployForceSkipsPlanning(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)
	fake := setupFakePlanner(t, "a.example.com")
	fake.planErr = errors.New("panel unavailable")

	certC := createBatchTestCertificate(t, st, "c.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d],"targets":[{"deploy_account_id":%d,"deploy_config":{},"force":true}]}`,
		certC.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("force deploy should bypass plan: %d %s", response.Code, response.Body.String())
	}
	if len(fake.deployed) != 1 || fake.deployed[0].Config["sites"] != "c.example.com" {
		t.Fatalf("force deploy should use certificate domains: %+v", fake.deployed)
	}
}

func TestBatchDeploySkipsDuplicateSite(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)
	fake := setupFakePlanner(t, "same.example.com")

	certA := createBatchTestCertificate(t, st, "same.example.com")
	certB := createBatchTestCertificate(t, st, "same.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d,%d],"targets":[{"deploy_account_id":%d,"deploy_config":{}}]}`,
		certA.ID, certB.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"conflict"`) {
		t.Fatalf("duplicate site should be skipped: %d %s", response.Code, response.Body.String())
	}
	if len(fake.deployed) != 1 {
		t.Fatalf("only first certificate should deploy, got %d", len(fake.deployed))
	}
}

func TestBatchDeployPlanFailureAbortsBeforeWrite(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	useTestBaotaDeployType(t)
	fake := setupFakePlanner(t, "a.example.com")
	fake.planErr = errors.New("panel unavailable")

	certA := createBatchTestCertificate(t, st, "a.example.com")
	account := &model.DeployAccount{Name: "panel", Type: "baota", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"certificate_ids":[%d],"targets":[{"deploy_account_id":%d,"deploy_config":{}}]}`,
		certA.ID, account.ID)
	response := sessionRequest(t, router, http.MethodPost, "/api/certificates/batch/deploy", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "api.error.batch_deploy_plan_failed") {
		t.Fatalf("plan failure should abort: %d %s", response.Code, response.Body.String())
	}
	if len(fake.deployed) != 0 {
		t.Fatalf("no deployment should run when plan fails: %+v", fake.deployed)
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
