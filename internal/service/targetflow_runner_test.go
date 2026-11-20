package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/deploy"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

const targetFlowTestManifest = `{"protocol":"v1","name":"target flow fake","version":"1.0.0","deployers":[{"id":"targetflowfake","name":"Target Flow Fake","fields":[{"key":"api_token","label":"Token","type":"password","secret":true,"required":true}],"deploy_fields":[{"key":"sites","label":"Sites","type":"text","required":true}],"capabilities":["plan-v1"]}]}`

func setupTargetFlowPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	output := filepath.Join(dir, "deploy-requests.jsonl")
	script := "#!/bin/sh\ncase \"$1\" in\nmanifest)\ncat <<'JSON'\n" + targetFlowTestManifest + "\nJSON\n;;\nplan)\nprintf '%s' '{\"ok\":true,\"field\":\"sites\",\"multiple\":true,\"objects\":[{\"value\":\"a.example.com\",\"name\":\"Site A\",\"domains\":[\"a.example.com\",\"www.a.example.com\"]},{\"value\":\"b.example.com\",\"name\":\"Site B\",\"domains\":[\"b.example.com\"]}]}'\n;;\ndeploy)\ncat >> " + output + "\nprintf '\\n' >> " + output + "\nprintf '%s' '{\"ok\":true}'\n;;\n*)\nexit 2\n;;\nesac\n"
	path := filepath.Join(dir, "targetflow-helper")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result := deployplugin.Discover(deployplugin.DiscoverConfig{
		Dir:              dir,
		Timeout:          3 * time.Second,
		EnabledDeployers: []string{"targetflowfake"},
	})
	if result.Deployers != 1 {
		t.Fatalf("fake deployer not discovered: %+v", result)
	}
	t.Cleanup(func() {
		deployplugin.UnregisterSource(deployplugin.SourceExec)
		deployplugin.Discover(deployplugin.DiscoverConfig{Dir: ""})
	})
	return output
}

func newTargetFlowStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "targetflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("target-flow-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(conn, cipher)
}

func seedTargetFlowTask(t *testing.T, st *store.Store, strategy string) (*model.Task, int64) {
	t.Helper()
	account := &model.DeployAccount{
		Name:   "fake panel",
		Type:   "targetflowfake",
		Config: map[string]string{"api_token": "secret"},
	}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fullchainPEM := testFullchainPEM(t)
	cert := &model.Certificate{
		Domains:       []string{"a.example.com", "www.a.example.com", "b.example.com"},
		PrimaryDomain: "a.example.com",
		Issuer:        "Test CA",
		CA:            "letsencrypt",
		CertPEM:       "CERT",
		KeyPEM:        "KEY",
		FullchainPEM:  fullchainPEM,
		Serial:        "01",
		NotBefore:     now.Add(-time.Hour),
		NotAfter:      now.Add(90 * 24 * time.Hour),
		Status:        "valid",
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Name:      "target flow",
		FlowMode:  model.FlowTargetBeta,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
		Status:    "pending",
		TargetFlow: model.TargetFlowConfig{
			Sources: []model.TargetSource{{
				DeployAccountID: account.ID,
				DeployConfig:    map[string]string{},
			}},
			Selection: model.SiteSelection{Mode: model.SelectionAll},
			Policy:    model.CertPolicy{Strategy: strategy, MaxDomainsPerCert: 100},
		},
		Pipeline: []model.PipelineNode{{
			ID:      "apply",
			Type:    model.PipelineApply,
			Enabled: true,
			Config: model.PipelineConfig{
				CA:        "letsencrypt",
				KeyType:   "ecdsa",
				AutoRenew: true,
				RenewDays: 30,
			},
		}},
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task, cert.ID
}

func TestExecuteTargetFlowReusesCertificateAndDeploys(t *testing.T) {
	output := setupTargetFlowPlugin(t)
	st := newTargetFlowStore(t)
	task, certID := seedTargetFlowTask(t, st, model.StrategyPerSite)
	manager := New(st)

	if err := manager.RunTask(task.ID, "task.trigger.manual"); err != nil {
		t.Fatalf("run target flow: %v", err)
	}
	latest, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "success" {
		t.Fatalf("unexpected status: %s %s", latest.Status, latest.Message)
	}
	if latest.CertID != certID {
		t.Fatalf("primary cert should be the reused certificate: %d", latest.CertID)
	}
	if latest.TargetFlow.LastResult == nil ||
		latest.TargetFlow.LastResult.SiteCount != 2 ||
		latest.TargetFlow.LastResult.GroupCount != 2 ||
		latest.TargetFlow.LastResult.DeployedCount != 2 {
		t.Fatalf("unexpected result: %+v", latest.TargetFlow.LastResult)
	}
	records, err := st.ListDeployRecords(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 deploy records, got %d", len(records))
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"action":"deploy"`) != 2 {
		t.Fatalf("expected two deploy calls: %s", raw)
	}
	if !strings.Contains(string(raw), `"sites":"a.example.com"`) || !strings.Contains(string(raw), `"sites":"b.example.com"`) {
		t.Fatalf("sites were not deployed individually: %s", raw)
	}
}

func TestExecuteTargetFlowSingleGroupJoinsSites(t *testing.T) {
	output := setupTargetFlowPlugin(t)
	st := newTargetFlowStore(t)
	task, _ := seedTargetFlowTask(t, st, model.StrategySingle)
	manager := New(st)

	if err := manager.RunTask(task.ID, "task.trigger.manual"); err != nil {
		t.Fatalf("run target flow: %v", err)
	}
	latest, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "success" || latest.TargetFlow.LastResult == nil || latest.TargetFlow.LastResult.GroupCount != 1 {
		t.Fatalf("unexpected result: %s %+v", latest.Status, latest.TargetFlow.LastResult)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"action":"deploy"`) != 1 {
		t.Fatalf("expected one deploy call: %s", raw)
	}
	var request map[string]any
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(raw)), "\n")[0]), &request); err != nil {
		t.Fatalf("decode deploy request: %v", err)
	}
	if request["config"].(map[string]any)["sites"] != "a.example.com,b.example.com" {
		t.Fatalf("sites were not joined: %+v", request["config"])
	}
}
