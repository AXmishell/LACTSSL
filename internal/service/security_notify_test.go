package service

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newNotifyTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("notify-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	return New(st), st
}

func TestSecurityNotifyPolicyRoundTrip(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	policy := SecurityNotifyPolicy{
		Enabled:       false,
		Events:        []string{model.SecurityEventPasswordChanged, model.SecurityEventEmailChanged},
		To:            "Ops@Example.com",
		RetentionDays: 30,
		FailedLogin:   true,
	}
	if err := m.SaveSecurityNotifyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	loaded := m.LoadSecurityNotifyPolicy(0)
	if loaded.Enabled || loaded.RetentionDays != 30 || loaded.To != "ops@example.com" {
		t.Fatalf("策略未正确保存: %+v", loaded)
	}
	if !loaded.EventEnabled(model.SecurityEventPasswordChanged) || !loaded.EventEnabled(model.SecurityEventEmailChanged) {
		t.Fatalf("事件开关未正确保存: %+v", loaded.Events)
	}
	if loaded.EventEnabled(model.SecurityEventTOTPDisabled) {
		t.Fatal("未启用的 TOTP 事件不应生效")
	}
	if loaded.Login != "new" || !loaded.FailedLogin || loaded.FailedThreshold != 5 || loaded.FailedWindow != 600 ||
		loaded.NewDeviceDays != 30 || len(loaded.Channels) != 1 || loaded.Channels[0] != "mail" || loaded.MaxPerHour != 10 {
		t.Fatalf("登录/失败/渠道/上限默认值不正确: %+v", loaded)
	}

	if err := ValidateSecurityNotifyPolicy(SecurityNotifyPolicy{Events: []string{"unknown.event"}}); err == nil {
		t.Fatal("未知事件应被拒绝")
	}
	if err := ValidateSecurityNotifyPolicy(SecurityNotifyPolicy{To: "not-an-email"}); err == nil {
		t.Fatal("非法收件人应被拒绝")
	}
	if err := ValidateSecurityNotifyPolicy(SecurityNotifyPolicy{To: "a@example.com,b@example.com"}); err != nil {
		t.Fatalf("合法收件人不应报错: %v", err)
	}
}

func TestSecurityNotifyRecipientRejectsHeaderInjection(t *testing.T) {
	for _, value := range []string{
		"a@example.com\r\nBcc: attacker@example.com",
		"a@example.com\nBcc: attacker@example.com",
		"a@example.com<script>",
	} {
		if _, err := parseSecurityRecipients(value); err == nil {
			t.Fatalf("危险收件人应被拒绝: %q", value)
		}
	}
	// 地址周围的空白会被规范化，不能把 CRLF 带进邮件头。
	items, err := parseSecurityRecipients("a@example.com, b@example.com\r\n")
	if err != nil || len(items) != 2 || strings.ContainsAny(strings.Join(items, ","), "\r\n") {
		t.Fatalf("收件人规范化不正确: %+v %v", items, err)
	}
	if got := sanitizeSecurityText("ua\r\nInjected: x", 64); strings.ContainsAny(got, "\r\n") {
		t.Fatalf("控制字符未清理: %q", got)
	}
}

func TestSecurityNotifyPolicyLoginFields(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	policy := SecurityNotifyPolicy{
		Enabled:         true,
		Events:          []string{model.SecurityEventPasswordChanged},
		Login:           "always",
		FailedLogin:     false,
		FailedThreshold: 3,
		FailedWindow:    120,
		NewDeviceDays:   7,
		Channels:        []string{"mail"},
		RetentionDays:   30,
	}
	if err := m.SaveSecurityNotifyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	loaded := m.LoadSecurityNotifyPolicy(0)
	if loaded.Login != "always" || loaded.FailedLogin || loaded.FailedThreshold != 3 ||
		loaded.FailedWindow != 120 || loaded.NewDeviceDays != 7 {
		t.Fatalf("登录策略未正确保存: %+v", loaded)
	}
	if !loaded.LoginNotifyEnabled(false) {
		t.Fatal("always 模式应对已知设备也提醒")
	}
	loaded.Login = "new"
	if loaded.LoginNotifyEnabled(false) || !loaded.LoginNotifyEnabled(true) {
		t.Fatal("new 模式只应提醒新设备")
	}
	loaded.Login = "off"
	if loaded.LoginNotifyEnabled(true) {
		t.Fatal("off 模式不应提醒")
	}

	for name, invalid := range map[string]SecurityNotifyPolicy{
		"login":     {Login: "sometimes"},
		"threshold": {FailedThreshold: 101},
		"window":    {FailedWindow: 10},
		"newdevice": {NewDeviceDays: 500},
		"channel":   {Channels: []string{"unknown-channel"}},
	} {
		if err := ValidateSecurityNotifyPolicy(invalid); err == nil {
			t.Fatalf("%s 非法配置应被拒绝", name)
		}
	}
}

func TestRecordLoginFailureAggregation(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	if err := m.SaveSecurityNotifyPolicy(SecurityNotifyPolicy{
		Enabled:         true,
		Events:          []string{model.SecurityEventPasswordChanged},
		Login:           "new",
		FailedLogin:     true,
		FailedThreshold: 3,
		FailedWindow:    600,
		NewDeviceDays:   30,
		Channels:        []string{"mail"},
		RetentionDays:   90,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index := 0; index < 2; index++ {
		result := m.RecordLoginFailure(1, "203.0.113.10", now.Add(time.Duration(index)*time.Second))
		if result.Notify {
			t.Fatalf("第 %d 次失败不应告警", index+1)
		}
	}
	result := m.RecordLoginFailure(1, "203.0.113.11", now.Add(3*time.Second))
	if !result.Notify || result.Count != 3 || len(result.IPs) != 2 {
		t.Fatalf("达到阈值应告警并保留来源 IP: %+v", result)
	}
	result = m.RecordLoginFailure(1, "203.0.113.12", now.Add(4*time.Second))
	if result.Notify {
		t.Fatal("告警后 1 小时内不应重复告警")
	}
	// 1 小时后再达到阈值应再次告警。
	now = now.Add(2 * time.Hour)
	for index := 0; index < 2; index++ {
		result = m.RecordLoginFailure(1, "203.0.113.13", now.Add(time.Duration(index)*time.Second))
		if result.Notify {
			t.Fatalf("第二次窗口内第 %d 次失败不应告警", index+1)
		}
	}
	result = m.RecordLoginFailure(1, "203.0.113.13", now.Add(3*time.Second))
	if !result.Notify {
		t.Fatal("静默期结束后达到阈值应再次告警")
	}
	m.ResetLoginFailures(1, "203.0.113.13")
	result = m.RecordLoginFailure(1, "203.0.113.13", now.Add(4*time.Second))
	if result.Count != 1 {
		t.Fatalf("重置后失败计数应重新开始: %+v", result)
	}
}

func TestRecordSecurityEventNotQueued(t *testing.T) {
	m, st := newNotifyTestManager(t)
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventLoginPassword,
		Fingerprint:  "device-a",
		AccountEmail: "admin@example.com",
		Detail:       map[string]string{"new_device": "true"},
	}
	if err := m.RecordSecurityEvent(*event); err != nil {
		t.Fatal(err)
	}
	pending, err := st.ListPendingSecurityEvents(time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("只记录的事件不应进入投递队列: %+v", pending)
	}
	events, err := st.ListSecurityEvents(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Notify || events[0].Fingerprint != "device-a" {
		t.Fatalf("登录历史记录不正确: %+v", events)
	}
}

func TestDeliverSecurityEventPluginChannel(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := m.SaveNotifyChannels(map[string]NotifyChannelConfig{
		"telegram": {"enabled": true, "bot_token": "token", "chat_id": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	called := 0
	m.channelSender = func(channel string, _ NotifyChannelConfig, _, _ string) error {
		called++
		if channel != "telegram" {
			t.Fatalf("渠道不正确: %s", channel)
		}
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventTOTPDisabled,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"telegram"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(event)
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil || called != 1 {
		t.Fatalf("插件渠道未正确投递: %+v called=%d", loaded, called)
	}
	if loaded.Deliveries["channel:telegram"] != "sent" {
		t.Fatalf("渠道投递状态不正确: %+v", loaded.Deliveries)
	}
}

func TestSecurityPolicyRecipients(t *testing.T) {
	policy := SecurityNotifyPolicy{To: "ops@example.com, Admin@Example.com"}
	recipients := SecurityPolicyRecipients(policy, "admin@example.com")
	if len(recipients) != 2 || recipients[0] != "admin@example.com" || recipients[1] != "ops@example.com" {
		t.Fatalf("收件人去重/排序不正确: %+v", recipients)
	}
}

func TestRenderSecurityEmailEscapesDynamicValues(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	event := model.SecurityEvent{
		Kind:         model.SecurityEventPasskeyAdded,
		AccountEmail: "admin@example.com",
		IP:           "203.0.113.10",
		UserAgent:    `<script>alert("ua")</script>`,
		Detail: map[string]string{
			"name":      `<b>my key</b>`,
			"remaining": "2",
		},
		CreatedAt: time.Date(2026, 11, 21, 18, 30, 0, 0, time.FixedZone("CST", 8*3600)),
		Link:      "https://ssl.example.com",
	}
	subject, body := m.renderSecurityEmail(event)
	if !strings.Contains(subject, "安全提醒") {
		t.Fatalf("主题缺少安全提醒标题: %s", subject)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>my key</b>") {
		t.Fatalf("正文存在未转义内容: %s", body)
	}
	if !strings.Contains(body, "&lt;b&gt;my key&lt;/b&gt;") {
		t.Fatalf("通行密钥名称未转义: %s", body)
	}
	if !strings.Contains(body, "https://ssl.example.com") {
		t.Fatalf("管理台链接缺失: %s", body)
	}
}

func TestRenderSecurityEmailAllKinds(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	for _, kind := range model.SecurityNotifyEventKinds() {
		subject, body := m.renderSecurityEmail(model.SecurityEvent{
			Kind:         kind,
			AccountEmail: "admin@example.com",
			IP:           "203.0.113.10",
			UserAgent:    "Mozilla/5.0",
			Detail: map[string]string{
				"old_email": "old@example.com",
				"new_email": "new@example.com",
				"name":      "my key",
				"prefix":    "abcd1234",
				"changes":   "smtp",
				"sessions":  "1",
				"api_keys":  "2",
				"passkeys":  "3",
				"remaining": "4",
			},
			CreatedAt: time.Now().UTC(),
			Link:      "https://ssl.example.com",
		})
		if subject == "" || strings.Contains(subject, "email.security.") {
			t.Fatalf("%s 主题未正确渲染: %q", kind, subject)
		}
		if strings.Contains(body, "email.security.") || strings.Contains(body, "{detail}") {
			t.Fatalf("%s 正文未正确渲染: %s", kind, body)
		}
	}
}

func TestDeliverSecurityEventMarksDelivered(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	sent := make([]string, 0, 2)
	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		sent = append(sent, to)
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventPasswordChanged,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com", "ops@example.com"},
		Channels:     []string{"mail"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(event)
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil {
		t.Fatalf("事件应标记已投递: %+v", loaded)
	}
	if len(sent) != 2 || loaded.Attempts != 1 {
		t.Fatalf("投递结果不正确: sent=%v attempts=%d", sent, loaded.Attempts)
	}
}

func TestDeliverSecurityEventRetriesOnlyFailedRecipient(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	failOps := true
	adminSends := 0
	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		if to == "admin@example.com" {
			adminSends++
			return nil
		}
		if failOps {
			return errors.New("ops mailbox down")
		}
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventTOTPDisabled,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com", "ops@example.com"},
		Channels:     []string{"mail"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(event)
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt != nil {
		t.Fatal("部分失败不应标记整条事件已投递")
	}
	if loaded.Deliveries["mail:admin@example.com"] != "sent" || loaded.Deliveries["mail:ops@example.com"] != "failed" {
		t.Fatalf("逐收件人状态不正确: %+v", loaded.Deliveries)
	}
	if loaded.NextAttemptAt.IsZero() || loaded.LastError == "" {
		t.Fatalf("应安排重试并记录错误: %+v", loaded)
	}

	failOps = false
	m.deliverSecurityEvent(loaded)
	loaded, err = st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil {
		t.Fatalf("重试成功后应标记已投递: %+v", loaded)
	}
	if adminSends != 1 {
		t.Fatalf("已成功收件人不应重复发送，实际发送 %d 次", adminSends)
	}
}

func TestRenderSecurityEmailLoginAndTestKinds(t *testing.T) {
	m, _ := newNotifyTestManager(t)
	cases := []struct {
		kind    string
		variant string
	}{
		{model.SecurityEventLoginPassword, "new"},
		{model.SecurityEventLoginPassword, "known"},
		{model.SecurityEventLoginPasskey, "new"},
		{model.SecurityEventLoginPasskey, "known"},
		{model.SecurityEventLoginFailed, "password"},
		{model.SecurityEventLoginFailed, "totp"},
		{model.SecurityEventLoginFailed, "passkey"},
		{model.SecurityEventTest, ""},
		{model.SecurityEventDigest, ""},
	}
	for _, item := range cases {
		subject, body := m.renderSecurityEmail(model.SecurityEvent{
			Kind:         item.kind,
			AccountEmail: "admin@example.com",
			IP:           "203.0.113.10",
			UserAgent:    "Mozilla/5.0",
			Detail: map[string]string{
				"variant": item.variant,
				"count":   "8",
				"window":  "600",
				"ips":     "203.0.113.10, 198.51.100.7",
			},
			CreatedAt: time.Now().UTC(),
		})
		if subject == "" || strings.Contains(subject, "email.security.") {
			t.Fatalf("%s/%s 主题未正确渲染: %q", item.kind, item.variant, subject)
		}
		for _, placeholder := range []string{"email.security.", "{count}", "{window}", "{ips}", "{detail}"} {
			if strings.Contains(body, placeholder) {
				t.Fatalf("%s/%s 正文残留 %s: %s", item.kind, item.variant, placeholder, body)
			}
		}
	}
}

func TestSecurityNotifyMaxPerHourDigest(t *testing.T) {
	m, st := newNotifyTestManager(t)
	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSecurityNotifyPolicy(SecurityNotifyPolicy{
		Enabled:         true,
		Events:          []string{model.SecurityEventPasswordChanged},
		Login:           "new",
		FailedLogin:     true,
		FailedThreshold: 5,
		FailedWindow:    600,
		NewDeviceDays:   30,
		Channels:        []string{"mail"},
		RetentionDays:   90,
		MaxPerHour:      2,
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := m.NotifySecurity(model.SecurityEvent{
			UserID:       user.ID,
			Kind:         model.SecurityEventPasswordChanged,
			AccountEmail: user.Email,
			Detail:       map[string]string{"sessions": strconv.Itoa(index)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.ListSecurityEvents(user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	changed, recorded, digest := 0, 0, 0
	for _, event := range events {
		switch event.Kind {
		case model.SecurityEventPasswordChanged:
			changed++
			if !event.Notify {
				recorded++
			}
		case model.SecurityEventDigest:
			digest++
		}
	}
	if changed != 3 || recorded != 1 || digest != 1 {
		t.Fatalf("超出每小时上限后应记录事件并合并为摘要: changed=%d recorded=%d digest=%d", changed, recorded, digest)
	}
	for _, event := range events {
		if event.Kind == model.SecurityEventDigest {
			if event.Detail["limit"] != "2" || event.Detail["count"] == "" {
				t.Fatalf("摘要内容不正确: %+v", event.Detail)
			}
		}
	}

	// 同一小时内摘要只发一次。
	if err := m.NotifySecurity(model.SecurityEvent{
		UserID:       user.ID,
		Kind:         model.SecurityEventPasswordChanged,
		AccountEmail: user.Email,
	}); err != nil {
		t.Fatal(err)
	}
	events, err = st.ListSecurityEvents(user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	digest = 0
	for _, event := range events {
		if event.Kind == model.SecurityEventDigest {
			digest++
		}
	}
	if digest != 1 {
		t.Fatalf("摘要不应重复发送: %d", digest)
	}
}

func TestDeliveryUsesSMTPConfigSnapshot(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "new.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "new@example.com"); err != nil {
		t.Fatal(err)
	}
	used := SMTPConfig{}
	m.mailSender = func(cfg SMTPConfig, _, _, _ string) error {
		used = cfg
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventNotifyChanged,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"mail"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
		SMTP: &model.SMTPConfigSnapshot{
			Host:     "old.example.com",
			Port:     465,
			Username: "mailer",
			Password: "secret",
			From:     "old@example.com",
			SSL:      true,
		},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(stored)
	if used.Host != "old.example.com" || used.From != "old@example.com" {
		t.Fatalf("应使用旧 SMTP 配置快照: %+v", used)
	}
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil {
		t.Fatal("使用快照投递后应标记已投递")
	}
	if loaded.SMTP != nil {
		t.Fatal("投递完成后应清除 SMTP 配置快照，缩短口令保留时间")
	}
}

func TestSecurityEventWorkerRecoversPendingAfterRestart(t *testing.T) {
	_, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventPasswordChanged,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"mail"},
		Notify:       true,
		CreatedAt:    time.Now().UTC().Add(-time.Minute),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}

	restarted := New(st)
	called := 0
	restarted.mailSender = func(_ SMTPConfig, _, _, _ string) error {
		called++
		return nil
	}
	restarted.processSecurityEvents()
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil || called != 1 {
		t.Fatalf("重启后待投递事件未恢复: %+v called=%d", loaded, called)
	}
}

func TestDeliverSecurityEventRetriesFailedPlugin(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := m.SaveNotifyChannels(map[string]NotifyChannelConfig{
		"telegram": {"enabled": true, "bot_token": "token", "chat_id": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	fail := true
	calls := 0
	m.channelSender = func(_ string, _ NotifyChannelConfig, _, _ string) error {
		calls++
		if fail {
			return errors.New("telegram down")
		}
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventTOTPDisabled,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"telegram"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(event)
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt != nil || loaded.Deliveries["channel:telegram"] != "failed" || loaded.NextAttemptAt.IsZero() {
		t.Fatalf("插件失败应安排重试: %+v", loaded)
	}
	fail = false
	m.deliverSecurityEvent(loaded)
	loaded, err = st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil || calls != 2 {
		t.Fatalf("插件重试后应投递成功: %+v calls=%d", loaded, calls)
	}
}

func TestDeliverSecurityEventMailSurvivesDisabledPlugin(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveNotifyChannels(map[string]NotifyChannelConfig{
		"telegram": {"enabled": false, "bot_token": "token", "chat_id": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	mailSent := 0
	channelSent := 0
	m.mailSender = func(_ SMTPConfig, _, _, _ string) error {
		mailSent++
		return nil
	}
	m.channelSender = func(_ string, _ NotifyChannelConfig, _, _ string) error {
		channelSent++
		return nil
	}
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventTOTPDisabled,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"mail", "telegram"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	m.deliverSecurityEvent(event)
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil || mailSent != 1 || channelSent != 0 {
		t.Fatalf("未启用插件不应阻塞邮件: %+v mail=%d channel=%d", loaded, mailSent, channelSent)
	}
	if loaded.Deliveries["channel:telegram"] != "skipped" {
		t.Fatalf("未启用插件应标记 skipped: %+v", loaded.Deliveries)
	}
}

func TestNotifySecurityRespectsDisabledPolicy(t *testing.T) {
	m, st := newNotifyTestManager(t)
	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSecurityNotifyPolicy(SecurityNotifyPolicy{Enabled: false, RetentionDays: 90}); err != nil {
		t.Fatal(err)
	}
	if err := m.NotifySecurity(model.SecurityEvent{
		UserID: user.ID,
		Kind:   model.SecurityEventPasswordChanged,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListSecurityEvents(user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("关闭策略后不应写入投递事件: %+v", events)
	}
}

func TestNotifySecurityStoresRecipients(t *testing.T) {
	m, st := newNotifyTestManager(t)
	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSecurityNotifyPolicy(SecurityNotifyPolicy{
		Enabled:       true,
		Events:        []string{model.SecurityEventPasswordChanged},
		To:            "ops@example.com",
		RetentionDays: 90,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.NotifySecurity(model.SecurityEvent{
		UserID: user.ID,
		Kind:   model.SecurityEventPasswordChanged,
		Detail: map[string]string{"sessions": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListSecurityEvents(user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("应写入 1 条安全事件，实际 %d", len(events))
	}
	event := events[0]
	if event.Kind != model.SecurityEventPasswordChanged || !event.Notify {
		t.Fatalf("事件内容不正确: %+v", event)
	}
	if len(event.Recipients) != 2 || event.Recipients[0] != "admin@example.com" || event.Recipients[1] != "ops@example.com" {
		t.Fatalf("收件人快照不正确: %+v", event.Recipients)
	}
	if len(event.Channels) != 1 || event.Channels[0] != "mail" {
		t.Fatalf("阶段 0 只应投递邮件: %+v", event.Channels)
	}
}
