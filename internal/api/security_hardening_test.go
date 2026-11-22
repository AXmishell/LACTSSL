package api

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

// postSessionJSON 发送带会话与 CSRF 的 JSON 请求，可选覆盖 Host 与 TLS。
func postSessionJSON(t *testing.T, router http.Handler, path, body string, session *http.Cookie, host string, useTLS bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, "1")
	req.RemoteAddr = "192.0.2.30:1234"
	if host != "" {
		req.Host = host
	}
	if useTLS {
		req.TLS = &tls.ConnectionState{}
	}
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "1"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestTOTPEnrollmentRequiresReauth 验证“新增 TOTP 认证器”必须先证明当前身份，
// 避免失窃会话直接把账号绑定到攻击者的认证器上并永久接管。
func TestTOTPEnrollmentRequiresReauth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	missing := postSessionJSON(t, router, "/api/auth/totp/setup", `{}`, session, "", false)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少再认证不应允许初始化 TOTP: %d %s", missing.Code, missing.Body.String())
	}

	setupTOTP := postSessionJSON(t, router, "/api/auth/totp/setup",
		`{"password":"password1234"}`, session, "", false)
	if setupTOTP.Code != http.StatusOK {
		t.Fatalf("正确再认证应允许初始化 TOTP: %d %s", setupTOTP.Code, setupTOTP.Body.String())
	}
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPSecret == "" {
		t.Fatal("TOTP 密钥应已保存")
	}
	code, err := totp.GenerateCode(user.TOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	enableMissing := postSessionJSON(t, router, "/api/auth/totp/enable",
		`{"code":"`+code+`"}`, session, "", false)
	if enableMissing.Code != http.StatusBadRequest || !strings.Contains(enableMissing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少再认证不应允许启用 TOTP: %d %s", enableMissing.Code, enableMissing.Body.String())
	}

	enabled := postSessionJSON(t, router, "/api/auth/totp/enable",
		`{"code":"`+code+`","password":"password1234"}`, session, "", false)
	if enabled.Code != http.StatusOK {
		t.Fatalf("正确再认证应允许启用 TOTP: %d %s", enabled.Code, enabled.Body.String())
	}
	user, err = st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	if !user.TOTPEnabled {
		t.Fatal("TOTP 应已启用")
	}
}

// TestPasskeyRegistrationRequiresReauth 验证注册新的通行密钥同样需要再认证。
func TestPasskeyRegistrationRequiresReauth(t *testing.T) {
	router := newSessionTestServer(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	missing := postSessionJSON(t, router, "/api/auth/passkeys/register/begin", `{}`, session, "localhost", true)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少再认证不应允许注册通行密钥: %d %s", missing.Code, missing.Body.String())
	}

	// 再认证通过后才会进入 WebAuthn 选项生成；localhost + TLS 满足来源要求。
	begin := postSessionJSON(t, router, "/api/auth/passkeys/register/begin",
		`{"password":"password1234"}`, session, "localhost", true)
	if begin.Code != http.StatusOK {
		t.Fatalf("正确再认证应进入通行密钥注册: %d %s", begin.Code, begin.Body.String())
	}
	if !strings.Contains(begin.Body.String(), "publicKey") {
		t.Fatalf("应返回 WebAuthn 创建选项: %s", begin.Body.String())
	}

	// finish 依赖 begin 生成的一次性挑战；没有挑战时直接拒绝。
	finishMissing := postSessionJSON(t, router, "/api/auth/passkeys/register/finish",
		`{"session_id":"missing","credential":{}}`, session, "localhost", true)
	if finishMissing.Code != http.StatusBadRequest || !strings.Contains(finishMissing.Body.String(), "api.error.passkey_challenge_invalid") {
		t.Fatalf("没有有效挑战时不应允许完成通行密钥注册: %d %s", finishMissing.Code, finishMissing.Body.String())
	}
}

// TestOutboundSettingsDoNotRequireReauth 验证修改 SMTP/通知目标以及发送测试消息
// 只依赖登录会话或对应 scope，不再要求密码/TOTP。
func TestOutboundSettingsDoNotRequireReauth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	// 未配置 SMTP 时测试邮件直接返回配置错误，说明已经越过了再认证。
	testMail := sessionRequest(t, router, http.MethodPost, "/api/settings/test-mail",
		`{"to":"a@example.com"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if testMail.Code != http.StatusBadRequest || !strings.Contains(testMail.Body.String(), "api.error.smtp_config_required") {
		t.Fatalf("测试邮件不应要求再认证: %d %s", testMail.Code, testMail.Body.String())
	}
	testChannel := sessionRequest(t, router, http.MethodPost, "/api/settings/test-channel",
		`{"channel":"","config":{}}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if testChannel.Code != http.StatusBadRequest || !strings.Contains(testChannel.Body.String(), "api.error.channel_required") {
		t.Fatalf("测试通知不应要求再认证: %d %s", testChannel.Code, testChannel.Body.String())
	}

	saved := sessionRequest(t, router, http.MethodPut, "/api/settings",
		`{"smtp_host":"attacker.example.com"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("修改 SMTP 主机不应要求再认证: %d %s", saved.Code, saved.Body.String())
	}
	if host, _ := st.GetSetting("smtp.host"); host != "attacker.example.com" {
		t.Fatalf("SMTP 主机应已更新: %q", host)
	}

	// 非出站设置同样保持可用。
	language := sessionRequest(t, router, http.MethodPut, "/api/settings",
		`{"language":"en-US"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if language.Code != http.StatusOK {
		t.Fatalf("只修改语言不应失败: %d %s", language.Code, language.Body.String())
	}

	// API 密钥凭 settings:write 即可修改出站设置。
	key, _ := createAPIKeyForTest(t, router, session, "settings-only", []string{apiScopeSettingsWrite})
	keyResp := apiKeyRequest(t, router, key, http.MethodPut, "/api/settings",
		`{"smtp_host":"attacker2.example.com"}`, "")
	if keyResp.Code != http.StatusOK {
		t.Fatalf("settings:write 密钥应允许修改出站设置: %d %s", keyResp.Code, keyResp.Body)
	}
}

// TestTaskIssuanceRequiresRunAndPrivateCAScope 验证“创建即签发”需要 tasks:run，
// 使用私有 CA 还需要 private_cas:write，避免非敏感任务权限直接消耗私有 CA。
func TestTaskIssuanceRequiresRunAndPrivateCAScope(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "Scope CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	caValue := service.PrivateCAValue(ca.ID)
	body := fmt.Sprintf(`{"name":"pca-task","domains":["pca.example.com"],"ca":%q,"key_type":"ecdsa","enabled":true,"pipeline":[{"id":"apply","type":"apply","enabled":true,"config":{"domains":["pca.example.com"],"ca":%q,"key_type":"ecdsa"}}]}`, caValue, caValue)

	writer, _ := createAPIKeyForTest(t, router, session, "task-writer", []string{apiScopeTasksWrite})
	resp := apiKeyRequest(t, router, writer, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "tasks:run") {
		t.Fatalf("创建即签发应要求 tasks:run: %d %s", resp.Code, resp.Body)
	}

	runner, _ := createAPIKeyForTest(t, router, session, "task-runner", []string{apiScopeTasksWrite, apiScopeTasksRun})
	resp = apiKeyRequest(t, router, runner, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "private_cas:write") {
		t.Fatalf("私有 CA 签发应要求 private_cas:write: %d %s", resp.Code, resp.Body)
	}

	commander, _ := createAPIKeyForTest(t, router, session, "task-commander",
		[]string{apiScopeTasksWrite, apiScopeTasksRun, apiScopePrivateCAWrite})
	resp = apiKeyRequest(t, router, commander, http.MethodPost, "/api/tasks", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("具备 tasks:run 与 private_cas:write 时应允许创建: %d %s", resp.Code, resp.Body)
	}

	// 直接写入一个待运行任务，避免上面创建即执行的后台任务处于 running 状态。
	runTask := &model.Task{
		Name: "pca-run", Domains: []string{"pca.example.com"}, Challenge: "dns01",
		CA: caValue, KeyType: "ecdsa", RenewDays: 30, Enabled: true, Status: "pending",
	}
	if err := st.CreateTask(runTask); err != nil {
		t.Fatal(err)
	}
	runOnly, _ := createAPIKeyForTest(t, router, session, "run-only", []string{apiScopeTasksRun})
	run := apiKeyRequest(t, router, runOnly, http.MethodPost, fmt.Sprintf("/api/tasks/%d/run", runTask.ID), "", "")
	if run.Code != http.StatusForbidden || !strings.Contains(run.Body, "private_cas:write") {
		t.Fatalf("仅有 tasks:run 不应运行私有 CA 任务: %d %s", run.Code, run.Body)
	}
}
