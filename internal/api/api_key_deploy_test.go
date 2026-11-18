package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

// TestAPIKeyDeployRequiresDeployScope 验证仅拥有 certificates:write 的 API 密钥
// 不能触发部署；必须显式拥有 deploy:execute。部署配置包含 reload 命令时，
// 还必须额外拥有 deploy:command，避免“执行部署”变成任意命令执行。
func TestAPIKeyDeployRequiresDeployScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	cert := &model.Certificate{
		PrimaryDomain: "deploy.example.com",
		CertPEM:       "CERT",
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	account := &model.DeployAccount{Name: "local", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	body := fmt.Sprintf(`{"targets":[{"deploy_account_id":%d,"deploy_config":{
		"cert_path":%q,"key_path":%q,"reload_command":%q}}]}`,
		account.ID,
		filepath.Join(dir, "cert.pem"),
		filepath.Join(dir, "key.pem"),
		"touch "+marker,
	)
	endpoint := fmt.Sprintf("/api/certificates/%d/deploy", cert.ID)

	limited, _ := createAPIKeyForTest(t, router, session, "cert-writer", []string{apiScopeCertificatesWrite})
	resp := apiKeyRequest(t, router, limited, http.MethodPost, endpoint, body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("仅有 certificates:write 时不应允许部署: %d %s", resp.Code, resp.Body)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("缺少 deploy:execute 时不应执行部署命令: %v", err)
	}

	deployer, _ := createAPIKeyForTest(t, router, session, "deployer", []string{apiScopeDeployExecute})
	resp = apiKeyRequest(t, router, deployer, http.MethodPost, endpoint, body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "deploy:command") {
		t.Fatalf("包含 reload 命令时仅有 deploy:execute 不应允许部署: %d %s", resp.Code, resp.Body)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("缺少 deploy:command 时不应执行部署命令: %v", err)
	}

	commander, _ := createAPIKeyForTest(t, router, session, "commander", []string{apiScopeDeployExecute, apiScopeDeployCommand})
	resp = apiKeyRequest(t, router, commander, http.MethodPost, endpoint, body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("拥有 deploy:execute 和 deploy:command 时应允许部署: %d %s", resp.Code, resp.Body)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("拥有 deploy:command 时应执行部署命令: %v", err)
	}
}

// TestAPIKeyTaskDeployRequiresDeployScope 验证创建/修改/运行包含部署节点的
// 任务时，API 密钥在 tasks:* 之外还需要 deploy:execute。
func TestAPIKeyTaskDeployRequiresDeployScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "Deploy CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	account := &model.DeployAccount{Name: "local", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}

	caValue := service.PrivateCAValue(ca.ID)
	dir := t.TempDir()
	cert := &model.Certificate{
		PrimaryDomain: "task.example.com",
		CertPEM:       "CERT",
		KeyPEM:        "PRIVATE-KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(24 * time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	deployNode := fmt.Sprintf(`{"id":"deploy-1","type":"deploy","enabled":true,"config":{
		"deploy_account_id":%d,"deploy_config":{"cert_path":%q,"key_path":%q,"reload_command":"true"}}}`,
		account.ID,
		filepath.Join(dir, "cert.pem"),
		filepath.Join(dir, "key.pem"),
	)
	applyNode := fmt.Sprintf(`{"id":"apply","type":"apply","enabled":true,"config":{
		"domains":["task.example.com"],"ca":%q,"key_type":"ecdsa"}}`, caValue)
	body := fmt.Sprintf(`{"name":"task-deploy","domains":["task.example.com"],
		"ca":%q,"key_type":"ecdsa","enabled":true,"pipeline":[%s,%s]}`, caValue, applyNode, deployNode)

	task := &model.Task{
		Name:      "base",
		Domains:   []string{"task.example.com"},
		Challenge: "dns01",
		CA:        caValue,
		KeyType:   "ecdsa",
		RenewDays: 30,
		CertID:    cert.ID,
		Status:    "pending",
		Pipeline: []model.PipelineNode{{
			ID:      "apply",
			Type:    model.PipelineApply,
			Enabled: true,
			Config: model.PipelineConfig{
				Domains: []string{"task.example.com"},
				CA:      caValue,
				KeyType: "ecdsa",
			},
		}},
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTaskResult(task.ID, task.Status, task.Message, nil, nil, cert.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/tasks/%d", task.ID)

	writer, _ := createAPIKeyForTest(t, router, session, "task-writer", []string{apiScopeTasksWrite})
	updated := apiKeyRequest(t, router, writer, http.MethodPut, path, body, "")
	if updated.Code != http.StatusForbidden || !strings.Contains(updated.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("仅有 tasks:write 时不应修改为含部署节点的任务: %d %s", updated.Code, updated.Body)
	}

	created := apiKeyRequest(t, router, writer, http.MethodPost, "/api/tasks", body, "")
	if created.Code != http.StatusForbidden || !strings.Contains(created.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("仅有 tasks:write 时不应创建含部署节点的任务: %d %s", created.Code, created.Body)
	}

	deployWriter, _ := createAPIKeyForTest(t, router, session, "task-deployer", []string{apiScopeTasksWrite, apiScopeTasksRun, apiScopePrivateCAWrite, apiScopeDeployExecute})
	updated = apiKeyRequest(t, router, deployWriter, http.MethodPut, path, body, "")
	if updated.Code != http.StatusForbidden || !strings.Contains(updated.Body, "deploy:command") {
		t.Fatalf("含 reload 命令时仅有 deploy:execute 不应允许修改任务: %d %s", updated.Code, updated.Body)
	}

	commander, _ := createAPIKeyForTest(t, router, session, "task-commander",
		[]string{apiScopeTasksWrite, apiScopeTasksRun, apiScopePrivateCAWrite, apiScopeDeployExecute, apiScopeDeployCommand})
	updated = apiKeyRequest(t, router, commander, http.MethodPut, path, body, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("拥有 deploy:command 时应允许修改含命令的任务: %d %s", updated.Code, updated.Body)
	}

	// 停用含部署节点的任务不会触发部署，应允许；重新启用会交给调度器执行，
	// 因此需要 deploy:execute，含命令时还需要 deploy:command。
	batchPath := "/api/tasks/batch/enabled"
	disabled := apiKeyRequest(t, router, writer, http.MethodPost, batchPath,
		fmt.Sprintf(`{"ids":[%d],"enabled":false}`, task.ID), "")
	if disabled.Code != http.StatusOK {
		t.Fatalf("停用含部署节点的任务不应要求 deploy:execute: %d %s", disabled.Code, disabled.Body)
	}
	enabled := apiKeyRequest(t, router, writer, http.MethodPost, batchPath,
		fmt.Sprintf(`{"ids":[%d],"enabled":true}`, task.ID), "")
	if enabled.Code != http.StatusForbidden || !strings.Contains(enabled.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("重新启用含部署节点的任务应要求 deploy:execute: %d %s", enabled.Code, enabled.Body)
	}
	enabled = apiKeyRequest(t, router, deployWriter, http.MethodPost, batchPath,
		fmt.Sprintf(`{"ids":[%d],"enabled":true}`, task.ID), "")
	if enabled.Code != http.StatusForbidden || !strings.Contains(enabled.Body, "deploy:command") {
		t.Fatalf("重新启用含 reload 命令的任务应要求 deploy:command: %d %s", enabled.Code, enabled.Body)
	}

	runner, _ := createAPIKeyForTest(t, router, session, "task-runner", []string{apiScopeTasksRun})
	runPath := path + "/run"
	run := apiKeyRequest(t, router, runner, http.MethodPost, runPath, "", "")
	if run.Code != http.StatusForbidden || !strings.Contains(run.Body, "api.error.api_key_scope_denied") {
		t.Fatalf("仅有 tasks:run 时不应运行含部署节点的任务: %d %s", run.Code, run.Body)
	}

	commandRunner, _ := createAPIKeyForTest(t, router, session, "task-command-runner",
		[]string{apiScopeTasksRun, apiScopePrivateCAWrite, apiScopeDeployExecute})
	run = apiKeyRequest(t, router, commandRunner, http.MethodPost, runPath, "", "")
	if run.Code != http.StatusForbidden || !strings.Contains(run.Body, "deploy:command") {
		t.Fatalf("仅有 tasks:run + deploy:execute 时不应运行含命令的任务: %d %s", run.Code, run.Body)
	}
}
