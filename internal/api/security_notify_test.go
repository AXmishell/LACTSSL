package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"lactssl/internal/auth"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

func findSecurityEvent(t *testing.T, st *store.Store, kind string) []model.SecurityEvent {
	t.Helper()
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.ListSecurityEvents(user.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]model.SecurityEvent, 0, 1)
	for _, event := range events {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func TestSecurityNotifyPasswordChangeEvent(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	response := sessionRequest(t, router, http.MethodPost, "/api/auth/change-password",
		`{"password":"password1234","new_password":"newpassword1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("改密失败: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventPasswordChanged)
	if len(events) != 1 {
		t.Fatalf("应写入 1 条改密安全事件，实际 %d", len(events))
	}
	event := events[0]
	if len(event.Recipients) != 1 || event.Recipients[0] != "admin@example.com" {
		t.Fatalf("改密事件收件人不正确: %+v", event.Recipients)
	}
	if event.Detail["sessions"] == "" || event.Detail["api_keys"] == "" || event.Detail["passkeys"] == "" {
		t.Fatalf("改密事件缺少失效数量: %+v", event.Detail)
	}
}

func TestSecurityNotifyEmailChangeNotifiesBothAddresses(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	response := sessionRequest(t, router, http.MethodPost, "/api/auth/change-email",
		`{"email":"new@example.com","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("换邮箱失败: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventEmailChanged)
	if len(events) != 2 {
		t.Fatalf("换邮箱应写入新旧两条事件，实际 %d", len(events))
	}
	recipients := map[string]bool{}
	for _, event := range events {
		for _, item := range event.Recipients {
			recipients[item] = true
		}
		if event.Detail["old_email"] != "admin@example.com" || event.Detail["new_email"] != "new@example.com" {
			t.Fatalf("事件详情不正确: %+v", event.Detail)
		}
	}
	if !recipients["admin@example.com"] || !recipients["new@example.com"] {
		t.Fatalf("未同时通知新旧邮箱: %+v", recipients)
	}
}

func TestSecurityNotifyTOTPDisableEvent(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, true); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response := sessionRequest(t, router, http.MethodPost, "/api/auth/totp/disable",
		`{"password":"password1234","totp":"`+code+`"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("关闭 TOTP 失败: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventTOTPDisabled)
	if len(events) != 1 {
		t.Fatalf("应写入 TOTP 关闭安全事件，实际 %d", len(events))
	}
}

func TestSecurityNotifyLoginNewDevice(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	_ = loginSessionTest(t, router)

	first := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("首次登录失败: %d %s", first.Code, first.Body.String())
	}
	device := findCookie(t, first.Result().Cookies(), deviceCookieName)
	if device == nil || device.Value == "" {
		t.Fatal("登录响应未下发设备 Cookie")
	}
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.ListSecurityEvents(user.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var firstLogin *model.SecurityEvent
	for index := range events {
		if events[index].Kind == model.SecurityEventLoginPassword && events[index].Notify {
			firstLogin = &events[index]
			break
		}
	}
	if firstLogin == nil {
		t.Fatal("首次登录未产生通知事件")
	}
	if firstLogin.Detail["new_device"] != "true" || firstLogin.Fingerprint == "" {
		t.Fatalf("首次登录应识别为新设备: %+v", firstLogin)
	}

	second := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`,
		[]*http.Cookie{device}, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("同设备再次登录失败: %d %s", second.Code, second.Body.String())
	}
	events, err = st.ListSecurityEvents(user.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var latest *model.SecurityEvent
	for index := range events {
		if events[index].Kind == model.SecurityEventLoginPassword {
			latest = &events[index]
			break
		}
	}
	if latest == nil {
		t.Fatal("同设备登录未记录历史事件")
	}
	if latest.Notify || latest.Detail["new_device"] != "false" {
		t.Fatalf("同设备登录不应重复提醒: %+v", latest)
	}
}

func TestSecurityNotifyLoginFailureAggregation(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	_ = loginSessionTest(t, router)

	for index := 0; index < 5; index++ {
		response := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
			`{"email":"admin@example.com","password":"wrong-password"}`, nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败登录状态不正确: %d %s", index+1, response.Code, response.Body.String())
		}
	}
	events := findSecurityEvent(t, st, model.SecurityEventLoginFailed)
	if len(events) != 1 {
		t.Fatalf("达到阈值应产生 1 条失败聚合事件，实际 %d", len(events))
	}
	event := events[0]
	if !event.Notify || event.Detail["count"] != "5" || event.Detail["ips"] == "" {
		t.Fatalf("失败聚合事件内容不正确: %+v", event)
	}

	response := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"wrong-password"}`, nil, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("静默期内失败登录状态不正确: %d %s", response.Code, response.Body.String())
	}
	events = findSecurityEvent(t, st, model.SecurityEventLoginFailed)
	if len(events) != 1 {
		t.Fatalf("静默期内不应重复产生失败聚合事件，实际 %d", len(events))
	}
}

func TestSecurityNotifyTestEndpoint(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	noCSRF := sessionRequest(t, router, http.MethodPost, "/api/settings/test-security-notify",
		`{"channels":["mail"],"password":"password1234"}`,
		[]*http.Cookie{session}, nil)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 不应发送测试安全提醒: %d %s", noCSRF.Code, noCSRF.Body.String())
	}
	response := sessionRequest(t, router, http.MethodPost, "/api/settings/test-security-notify",
		`{"channels":["mail"],"to":"","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("发送测试安全提醒失败: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventTest)
	if len(events) != 1 || !events[0].Notify {
		t.Fatalf("测试安全提醒未进入队列: %+v", events)
	}
}

func TestSecurityNotifySettingsWithoutReauth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	list := sessionRequest(t, router, http.MethodGet, "/api/settings", "",
		[]*http.Cookie{session}, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"security_notify"`) {
		t.Fatalf("设置接口未返回安全提醒策略: %d %s", list.Code, list.Body.String())
	}

	body := `{"security_notify":{"enabled":false,"events":[],"to":"","retention_days":30}}`
	allowed := sessionRequest(t, router, http.MethodPut, "/api/settings", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("修改安全提醒策略不应要求再认证: %d %s", allowed.Code, allowed.Body.String())
	}

	noCSRF := sessionRequest(t, router, http.MethodPut, "/api/settings",
		`{"security_notify":{"enabled":false,"events":[]}}`,
		[]*http.Cookie{session}, nil)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 不应修改安全提醒策略: %d %s", noCSRF.Code, noCSRF.Body.String())
	}

	body = `{"security_notify":{"enabled":false,"events":[],"to":"","retention_days":30,"login":"off","failed_login":false,"failed_threshold":3,"failed_window":120,"new_device_days":7,"channels":["mail"],"max_per_hour":25}}`
	saved := sessionRequest(t, router, http.MethodPut, "/api/settings", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存安全提醒策略失败: %d %s", saved.Code, saved.Body.String())
	}
	enabled, _ := st.GetSetting("security.notify.enabled")
	if enabled != "false" {
		t.Fatalf("安全提醒总开关未保存: %q", enabled)
	}
	retention, _ := st.GetSetting("security.notify.retention_days")
	if days, _ := strconv.Atoi(retention); days != 30 {
		t.Fatalf("保留天数未保存: %q", retention)
	}
	login, _ := st.GetSetting("security.notify.login")
	if login != "off" {
		t.Fatalf("登录提醒模式未保存: %q", login)
	}
	threshold, _ := st.GetSetting("security.notify.failed_threshold")
	if value, _ := strconv.Atoi(threshold); value != 3 {
		t.Fatalf("失败阈值未保存: %q", threshold)
	}
	window, _ := st.GetSetting("security.notify.failed_window")
	if value, _ := strconv.Atoi(window); value != 120 {
		t.Fatalf("失败窗口未保存: %q", window)
	}
	maxPerHour, _ := st.GetSetting("security.notify.max_per_hour")
	if value, _ := strconv.Atoi(maxPerHour); value != 25 {
		t.Fatalf("每小时上限未保存: %q", maxPerHour)
	}
}

func TestSecurityNotifyRecentEventsEndpoint(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	response := sessionRequest(t, router, http.MethodGet, "/api/security/events?limit=10", "",
		[]*http.Cookie{session}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("读取最近安全事件失败: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"events"`) || !strings.Contains(body, model.SecurityEventLoginPassword) {
		t.Fatalf("最近安全事件内容不正确: %s", body)
	}
	if !strings.Contains(body, `"deliveries"`) {
		t.Fatalf("最近安全事件缺少逐目标状态: %s", body)
	}
}

func TestSecurityNotifyPolicyChangeAlertsOldRecipients(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("smtp.host", "smtp.old.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@old.example.com"); err != nil {
		t.Fatal(err)
	}

	first := `{"security_notify":{"enabled":true,"events":[],"to":"old@example.com","retention_days":90},"password":"password1234"}`
	response := sessionRequest(t, router, http.MethodPut, "/api/settings", first,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("保存旧安全提醒策略失败: %d %s", response.Code, response.Body.String())
	}

	second := `{"security_notify":{"enabled":false,"events":[],"to":"new@example.com","retention_days":90},"password":"password1234"}`
	response = sessionRequest(t, router, http.MethodPut, "/api/settings", second,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("修改安全提醒策略失败: %d %s", response.Code, response.Body.String())
	}

	events := findSecurityEvent(t, st, model.SecurityEventNotifyChanged)
	if len(events) == 0 {
		t.Fatal("策略变更未产生安全事件")
	}
	latest := events[0]
	recipients := map[string]bool{}
	for _, item := range latest.Recipients {
		recipients[item] = true
	}
	if !recipients["old@example.com"] || !recipients["admin@example.com"] {
		t.Fatalf("策略变更应通知旧收件人: %+v", latest.Recipients)
	}
	if recipients["new@example.com"] {
		t.Fatalf("策略变更不应只通知新收件人: %+v", latest.Recipients)
	}
	if !latest.Notify {
		t.Fatal("策略变更事件应进入投递队列")
	}
	if latest.SMTP == nil || latest.SMTP.Host != "smtp.old.example.com" {
		t.Fatalf("策略变更事件应保存旧 SMTP 配置快照: %+v", latest.SMTP)
	}
}

func TestSecurityNotifyCriticalEventsBypassHourlyLimit(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	for index := 0; index < 11; index++ {
		body := fmt.Sprintf(`{"name":"key-%d","scopes":["system:read"]}`, index)
		response := sessionRequest(t, router, http.MethodPost, "/api/api-keys", body,
			[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
		if response.Code != http.StatusOK {
			t.Fatalf("创建第 %d 个 API 密钥失败: %d %s", index+1, response.Code, response.Body.String())
		}
	}
	events := findSecurityEvent(t, st, model.SecurityEventAPIKeyCreated)
	if len(events) != 11 {
		t.Fatalf("核心凭据事件不应被每小时上限合并，实际 %d", len(events))
	}
	if digest := findSecurityEvent(t, st, model.SecurityEventDigest); len(digest) != 0 {
		t.Fatalf("核心凭据事件不应产生摘要: %d", len(digest))
	}
}

func TestSecurityNotifyAPIKeyRoutePolicies(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	response := sessionRequest(t, router, http.MethodPost, "/api/api-keys",
		`{"name":"admin-key","scopes":["*"]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("创建测试密钥失败: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Data.Token == "" {
		t.Fatalf("解析测试密钥失败: %v %s", err, response.Body.String())
	}
	token := created.Data.Token

	events := apiKeyRequest(t, router, token, http.MethodGet, "/api/security/events", "", apiKeyHeader)
	if events.Code != http.StatusForbidden {
		t.Fatalf("API 密钥不应读取安全事件: %d %s", events.Code, events.Body)
	}

	policy := apiKeyRequest(t, router, token, http.MethodPut, "/api/settings",
		`{"security_notify":{"enabled":false,"events":[]}}`, apiKeyHeader)
	if policy.Code != http.StatusOK {
		t.Fatalf("具备 settings:write 的 API 密钥应可修改安全提醒策略: %d %s", policy.Code, policy.Body)
	}

	test := apiKeyRequest(t, router, token, http.MethodPost, "/api/settings/test-security-notify",
		`{"channels":["mail"]}`, apiKeyHeader)
	if test.Code != http.StatusForbidden {
		t.Fatalf("测试安全提醒仍只允许浏览器会话: %d %s", test.Code, test.Body)
	}
}

func TestSecurityNotifyRecentEventsOmitSMTPSecret(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := st.SetSetting("smtp.host", "smtp.old.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecret("smtp.password", "super-secret-smtp-password"); err != nil {
		t.Fatal(err)
	}
	body := `{"security_notify":{"enabled":true,"events":[],"to":"","retention_days":90},"password":"password1234"}`
	response := sessionRequest(t, router, http.MethodPut, "/api/settings", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("修改策略失败: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/settings", "/api/security/events?limit=20"} {
		result := sessionRequest(t, router, http.MethodGet, path, "",
			[]*http.Cookie{session}, nil)
		if result.Code != http.StatusOK {
			t.Fatalf("读取 %s 失败: %d %s", path, result.Code, result.Body.String())
		}
		text := result.Body.String()
		if strings.Contains(text, "super-secret-smtp-password") || strings.Contains(text, "smtp_snapshot") {
			t.Fatalf("%s 泄露 SMTP 配置快照: %s", path, text)
		}
	}
}

func TestSecurityNotifyAPIKeyCreateEvent(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	response := sessionRequest(t, router, http.MethodPost, "/api/api-keys",
		`{"name":"ci","scopes":["system:read"]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("创建 API 密钥失败: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventAPIKeyCreated)
	if len(events) != 1 {
		t.Fatalf("应写入 API 密钥创建事件，实际 %d", len(events))
	}
	if events[0].Detail["name"] != "ci" || events[0].Detail["prefix"] == "" {
		t.Fatalf("API 密钥事件详情不正确: %+v", events[0].Detail)
	}
}

func TestSecurityNotifyEventStoreFailureDoesNotBreakPasswordChange(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if _, err := st.DB.Exec(`DROP TABLE security_events`); err != nil {
		t.Fatalf("删除安全事件表失败: %v", err)
	}
	response := sessionRequest(t, router, http.MethodPost, "/api/auth/change-password",
		`{"password":"password1234","new_password":"newpassword1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("安全事件写库失败不应影响改密: %d %s", response.Code, response.Body.String())
	}
}

func TestSecurityNotifyAPIContinuesWithoutSMTP(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	// 未配置 SMTP 时，改密仍然必须成功，事件进入重试队列而不是阻塞请求。
	response := sessionRequest(t, router, http.MethodPost, "/api/auth/change-password",
		`{"password":"password1234","new_password":"newpassword1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("SMTP 未配置不应影响改密: %d %s", response.Code, response.Body.String())
	}
	events := findSecurityEvent(t, st, model.SecurityEventPasswordChanged)
	if len(events) != 1 {
		t.Fatalf("改密事件未落库: %d", len(events))
	}
	if !events[0].Notify {
		t.Fatal("安全事件应进入投递队列")
	}
}

func TestSettingsCanonicalizesNotifyRecipients(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	response := sessionRequest(t, router, http.MethodPut, "/api/settings",
		`{"notify_to":" Ops@Example.com , admin@example.com,ops@example.com "}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("保存通知收件人失败: %d %s", response.Code, response.Body.String())
	}
	value, err := st.GetSetting("notify.to")
	if err != nil {
		t.Fatal(err)
	}
	if value != "ops@example.com,admin@example.com" {
		t.Fatalf("通知收件人未规范化: %q", value)
	}

	invalid := sessionRequest(t, router, http.MethodPut, "/api/settings",
		`{"notify_to":"not-an-email"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("非法通知收件人应被拒绝: %d %s", invalid.Code, invalid.Body.String())
	}
}
