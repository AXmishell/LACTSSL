package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

func TestDeployAccountVerifyWithoutReauthAndPersistsStatus(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	account := &model.DeployAccount{Name: "local-dir", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/deploy-accounts/%d/verify", account.ID)

	response := sessionRequest(t, router, http.MethodPost, path, `{}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("验证不应再要求密码/TOTP: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data service.VerifyResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Data.OK {
		t.Fatalf("本地凭据无路径时应降级为跳过并通过: %+v", payload.Data)
	}

	loaded, err := st.GetDeployAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VerifyStatus != "ok" || loaded.VerifiedAt == nil {
		t.Fatalf("验证结果未落库: %+v", loaded)
	}
}

func TestVerifyUnsavedConfigRequiresExplicitSecret(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	response := sessionRequest(t, router, http.MethodPost, "/api/deploy-accounts/verify",
		`{"type":"ssh","config":{"host":"example.com","user":"root","auth_type":"password"}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "verify_secret_required") {
		t.Fatalf("未提交密钥时必须拒绝验证: %d %s", response.Code, response.Body.String())
	}
}

func TestVerifyAPIKeyScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	account := &model.DeployAccount{Name: "local-dir", Type: "local", Config: map[string]string{}}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/deploy-accounts/%d/verify", account.ID)

	readToken, _ := createAPIKeyForTest(t, router, session, "reader", []string{apiScopeDeployAccountsRead})
	denied := sessionRequest(t, router, http.MethodPost, path, `{}`, nil,
		map[string]string{"Authorization": "Bearer " + readToken})
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "api_key_scope_denied") {
		t.Fatalf("只读密钥不能触发验证: %d %s", denied.Code, denied.Body.String())
	}

	writeToken, _ := createAPIKeyForTest(t, router, session, "writer", []string{apiScopeDeployAccountsWrite})
	allowed := sessionRequest(t, router, http.MethodPost, path, `{}`, nil,
		map[string]string{"Authorization": "Bearer " + writeToken})
	if allowed.Code != http.StatusOK {
		t.Fatalf("写权限密钥应可验证: %d %s", allowed.Code, allowed.Body.String())
	}
}

func TestVerifyDNSAccountPersistsUnsupportedStatus(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	account := &model.DNSAccount{Name: "missing-provider", Provider: "not-installed", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/dns-accounts/%d/verify", account.ID)
	response := sessionRequest(t, router, http.MethodPost, path, `{"domain":"example.com"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("验证接口应返回分项结果: %d %s", response.Code, response.Body.String())
	}

	loaded, err := st.GetDNSAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VerifyStatus != "unsupported" {
		t.Fatalf("未安装插件应记录为 unsupported: %+v", loaded)
	}
}

func TestProbeDNSZoneWithoutReauth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	account := &model.DNSAccount{Name: "probe", Provider: "not-installed", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/dns-accounts/%d/zones/probe", account.ID)
	response := sessionRequest(t, router, http.MethodPost, path,
		`{"domain":"example.com","mode":"read","apply":true}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("探测不应再要求密码/TOTP: %d %s", response.Code, response.Body.String())
	}
}

func TestUpdateDeployAccountResetsVerification(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	account := &model.DeployAccount{
		Name: "ssh-target", Type: "ssh",
		Config: map[string]string{"host": "old.example.com", "user": "root", "auth_type": "password", "password": "secret"},
	}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := st.SetDeployAccountVerification(account.ID, "ok", "", "read", &at); err != nil {
		t.Fatal(err)
	}

	// 目标变化但密钥留空必须被拒绝，避免把旧密码发往新主机。
	rejected := `{"name":"ssh-target","type":"ssh","config":{"host":"new.example.com","user":"root","auth_type":"password","password":""}}`
	response := sessionRequest(t, router, http.MethodPut, fmt.Sprintf("/api/deploy-accounts/%d", account.ID),
		rejected, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "credentials_target_changed") {
		t.Fatalf("目标变化且密钥留空应被拒绝: %d %s", response.Code, response.Body.String())
	}

	body := `{"name":"ssh-target","type":"ssh","config":{"host":"new.example.com","user":"root","auth_type":"password","password":"new-secret"}}`
	response = sessionRequest(t, router, http.MethodPut, fmt.Sprintf("/api/deploy-accounts/%d", account.ID),
		body, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("更新凭据失败: %d %s", response.Code, response.Body.String())
	}
	loaded, err := st.GetDeployAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VerifyStatus != "" || loaded.VerifiedAt != nil {
		t.Fatalf("修改连接配置后应清空旧验证结果: %+v", loaded)
	}
}
