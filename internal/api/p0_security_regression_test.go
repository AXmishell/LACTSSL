package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

// taskScopePrivateCA 创建一个可用于任务授权测试的私有 CA。
func taskScopePrivateCA(t *testing.T, st interface {
	CreatePrivateCA(*model.PrivateCA) error
}) (string, int64) {
	t.Helper()
	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "P0 Scope CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	return service.PrivateCAValue(ca.ID), ca.ID
}

// TestTaskDisabledUpdateRequiresDeployAndPrivateScopes 验证禁用状态修改任务流程
// 仍要求 deploy:execute / deploy:command / private_cas:write，不再依赖 Enabled。
func TestTaskDisabledUpdateRequiresDeployAndPrivateScopes(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	caValue, _ := taskScopePrivateCA(t, st)
	account := &model.DeployAccount{Name: "local", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Name: "disabled-scope", Domains: []string{"scope.example.com"}, Challenge: "dns01",
		CA: caValue, KeyType: "ecdsa", RenewDays: 30, Enabled: false, Status: "pending",
		Pipeline: []model.PipelineNode{{
			ID: "apply", Type: model.PipelineApply, Enabled: true,
			Config: model.PipelineConfig{Domains: []string{"scope.example.com"}, CA: caValue, KeyType: "ecdsa"},
		}},
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := fmt.Sprintf(`{"name":"disabled-scope","domains":["scope.example.com"],"ca":%q,"key_type":"ecdsa","enabled":false,"pipeline":[
		{"id":"apply","type":"apply","enabled":true,"config":{"domains":["scope.example.com"],"ca":%q,"key_type":"ecdsa"}},
		{"id":"deploy-1","type":"deploy","enabled":true,"config":{"deploy_account_id":%d,"deploy_config":{"cert_path":%q,"key_path":%q,"reload_command":"true"}}}
	]}`, caValue, caValue, account.ID, filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	endpoint := fmt.Sprintf("/api/tasks/%d", task.ID)

	writer, _ := createAPIKeyForTest(t, router, session, "disabled-writer", []string{apiScopeTasksWrite})
	resp := apiKeyRequest(t, router, writer, http.MethodPut, endpoint, body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeDeployExecute) {
		t.Fatalf("禁用状态写入部署节点也应要求 deploy:execute: %d %s", resp.Code, resp.Body)
	}

	deployer, _ := createAPIKeyForTest(t, router, session, "disabled-deployer",
		[]string{apiScopeTasksWrite, apiScopeDeployExecute, apiScopePrivateCAWrite})
	resp = apiKeyRequest(t, router, deployer, http.MethodPut, endpoint, body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeDeployCommand) {
		t.Fatalf("禁用状态写入 reload_command 也应要求 deploy:command: %d %s", resp.Code, resp.Body)
	}

	commander, _ := createAPIKeyForTest(t, router, session, "disabled-commander",
		[]string{apiScopeTasksWrite, apiScopeDeployExecute, apiScopePrivateCAWrite, apiScopeDeployCommand})
	resp = apiKeyRequest(t, router, commander, http.MethodPut, endpoint, body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("具备完整 scope 时禁用任务更新应成功: %d %s", resp.Code, resp.Body)
	}
}

// TestTaskRunUsesAuthorizedSnapshot 验证执行时使用授权检查时的任务快照，
// 而不是按 ID 重新读取已被并发修改的任务。
func TestTaskRunUsesAuthorizedSnapshot(t *testing.T) {
	_, st := newSessionTestServerWithStore(t)
	caValue, _ := taskScopePrivateCA(t, st)
	task := &model.Task{
		Name: "snapshot", Domains: []string{"snapshot.example.com"}, Challenge: "dns01",
		CA: caValue, KeyType: "ecdsa", RenewDays: 30, Enabled: true, Status: "pending",
		Pipeline: []model.PipelineNode{{
			ID: "apply", Type: model.PipelineApply, Enabled: true,
			Config: model.PipelineConfig{Domains: []string{"snapshot.example.com"}, CA: caValue, KeyType: "ecdsa"},
		}},
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	snapshot := *task

	// 模拟检查完成后、执行前任务被改成无效 CA。若执行时按 ID 重载，会失败。
	task.CA = "invalid-ca"
	task.Domains = []string{"changed.example.com"}
	task.Pipeline = nil
	if err := st.UpdateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := service.New(st).RunTaskWithOptions(task.ID, "task.trigger.manual",
		service.RunOptions{Task: &snapshot}); err != nil {
		t.Fatalf("执行应使用授权时的任务快照，实际失败: %v", err)
	}
}

// TestTaskDNSAccountsRequireDNSReadScope 验证 tasks:* 不再隐式借用全部 DNS 凭据。
func TestTaskDNSAccountsRequireDNSReadScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	account := &model.DNSAccount{Name: "scope-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, []model.DNSZoneBinding{{
		DNSAccountID: account.ID, Zone: "scope.example.com", MatchMode: service.DNSZoneMatchSuffix, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"dns-scope","domains":["scope.example.com"],"dns_account_id":%d,"ca":"letsencrypt","key_type":"ecdsa","enabled":false}`, account.ID)

	writer, _ := createAPIKeyForTest(t, router, session, "dns-scope-writer", []string{apiScopeTasksWrite})
	resp := apiKeyRequest(t, router, writer, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeDNSRead) {
		t.Fatalf("任务使用 DNS 账户应要求 dns_accounts:read: %d %s", resp.Code, resp.Body)
	}

	allowed, _ := createAPIKeyForTest(t, router, session, "dns-scope-allowed",
		[]string{apiScopeTasksWrite, apiScopeDNSRead})
	resp = apiKeyRequest(t, router, allowed, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("显式授予 dns_accounts:read 后应允许创建任务: %d %s", resp.Code, resp.Body)
	}
}

// TestTaskCustomCARequiresCAReadScope 验证任务路径借用 custom/EAB 上游 CA 时，
// 与 CSR 路径一样要求 ca:read。
func TestTaskCustomCARequiresCAReadScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	account := &model.DNSAccount{Name: "custom-ca-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, []model.DNSZoneBinding{{
		DNSAccountID: account.ID, Zone: "custom.example.com", MatchMode: service.DNSZoneMatchSuffix, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	created := sessionRequest(t, router, http.MethodPost, "/api/acme-cas",
		`{"name":"P0 Custom CA","directory_url":"https://ca.example.com/acme/directory","protocol":"v2"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if created.Code != http.StatusOK {
		t.Fatalf("创建自定义 CA 失败: %d %s", created.Code, created.Body.String())
	}
	var custom model.ACMECA
	if err := json.Unmarshal([]byte(extractData(t, created.Body.String())), &custom); err != nil {
		t.Fatal(err)
	}
	caValue := service.ACMECAValue(custom.ID)
	body := fmt.Sprintf(`{"name":"custom-scope","domains":["custom.example.com"],"dns_account_id":%d,"ca":%q,"key_type":"ecdsa","enabled":false}`, account.ID, caValue)

	noCA, _ := createAPIKeyForTest(t, router, session, "custom-no-ca",
		[]string{apiScopeTasksWrite, apiScopeDNSRead})
	resp := apiKeyRequest(t, router, noCA, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeCARead) {
		t.Fatalf("任务使用 custom CA 应要求 ca:read: %d %s", resp.Code, resp.Body)
	}

	withCA, _ := createAPIKeyForTest(t, router, session, "custom-with-ca",
		[]string{apiScopeTasksWrite, apiScopeDNSRead, apiScopeCARead})
	resp = apiKeyRequest(t, router, withCA, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("显式授予 ca:read 后应允许创建任务: %d %s", resp.Code, resp.Body)
	}
}

// TestTaskCustomCAPoolRequiresCAReadScope 验证 CA 调度组中的 custom/EAB 上游
// 也会被任务路径识别，不能通过 ca_pool_id 绕过 ca:read。
func TestTaskCustomCAPoolRequiresCAReadScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	account := &model.DNSAccount{Name: "pool-dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, []model.DNSZoneBinding{{
		DNSAccountID: account.ID, Zone: "pool.example.com", MatchMode: service.DNSZoneMatchSuffix, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	created := sessionRequest(t, router, http.MethodPost, "/api/acme-cas",
		`{"name":"P0 Pool CA","directory_url":"https://ca.example.com/acme/directory","protocol":"v2"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if created.Code != http.StatusOK {
		t.Fatalf("创建自定义 CA 失败: %d %s", created.Code, created.Body.String())
	}
	var custom model.ACMECA
	if err := json.Unmarshal([]byte(extractData(t, created.Body.String())), &custom); err != nil {
		t.Fatal(err)
	}
	caValue := service.ACMECAValue(custom.ID)

	poolResp := sessionRequest(t, router, http.MethodPost, "/api/ca-pools",
		fmt.Sprintf(`{"name":"p0-custom-pool","strategy":"round_robin","enabled":true,"items":[{"ca_value":%q,"priority":10,"weight":1,"enabled":true}]}`, caValue),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if poolResp.Code != http.StatusOK {
		t.Fatalf("创建自定义 CA 调度组失败: %d %s", poolResp.Code, poolResp.Body.String())
	}
	var pool model.CAPool
	if err := json.Unmarshal([]byte(extractData(t, poolResp.Body.String())), &pool); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"pool-scope","domains":["pool.example.com"],"dns_account_id":%d,"ca_pool_id":%d,"key_type":"ecdsa","enabled":false}`, account.ID, pool.ID)

	noCA, _ := createAPIKeyForTest(t, router, session, "pool-no-ca",
		[]string{apiScopeTasksWrite, apiScopeDNSRead})
	resp := apiKeyRequest(t, router, noCA, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeCARead) {
		t.Fatalf("custom CA 调度组应要求 ca:read: %d %s", resp.Code, resp.Body)
	}

	withCA, _ := createAPIKeyForTest(t, router, session, "pool-with-ca",
		[]string{apiScopeTasksWrite, apiScopeDNSRead, apiScopeCARead})
	resp = apiKeyRequest(t, router, withCA, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("显式授予 ca:read 后应允许创建任务: %d %s", resp.Code, resp.Body)
	}
}

// TestSettingsPolicyRequiresSecurityWriteScope 验证 settings:write 不能单独
// 修改 CSR 白名单/默认 CA 等签发策略。
func TestSettingsPolicyRequiresSecurityWriteScope(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	body := `{"csr_allow_all_domains":true,"csr_allowed_domains":""}`

	settingsOnly, _ := createAPIKeyForTest(t, router, session, "settings-only-policy", []string{apiScopeSettingsWrite})
	resp := apiKeyRequest(t, router, settingsOnly, http.MethodPut, "/api/settings", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeSecurityWrite) {
		t.Fatalf("仅 settings:write 不应修改 CSR 策略: %d %s", resp.Code, resp.Body)
	}

	withSecurity, _ := createAPIKeyForTest(t, router, session, "settings-security-policy",
		[]string{apiScopeSettingsWrite, apiScopeSecurityWrite})
	resp = apiKeyRequest(t, router, withSecurity, http.MethodPut, "/api/settings", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("具备 security:write 时应允许修改 CSR 策略: %d %s", resp.Code, resp.Body)
	}
}

// TestTrustedCAPIImportRequiresSecurityWriteScope 验证 API Key 导入信任根
// 需要 security:write，不能仅凭 settings:write。
func TestTrustedCAPIImportRequiresSecurityWriteScope(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	certPEM := testCAPEM(t)
	body, err := json.Marshal(map[string]string{"name": "P0 Root", "cert_pem": certPEM})
	if err != nil {
		t.Fatal(err)
	}

	settingsOnly, _ := createAPIKeyForTest(t, router, session, "trust-settings-only", []string{apiScopeSettingsWrite})
	resp := apiKeyRequest(t, router, settingsOnly, http.MethodPost, "/api/trusted-cas/import", string(body), "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeSecurityWrite) {
		t.Fatalf("仅 settings:write 不应导入信任根: %d %s", resp.Code, resp.Body)
	}

	withSecurity, _ := createAPIKeyForTest(t, router, session, "trust-security",
		[]string{apiScopeSettingsWrite, apiScopeSecurityWrite})
	resp = apiKeyRequest(t, router, withSecurity, http.MethodPost, "/api/trusted-cas/import", string(body), "")
	if resp.Code != http.StatusOK {
		t.Fatalf("具备 security:write 时应允许导入信任根: %d %s", resp.Code, resp.Body)
	}
}
