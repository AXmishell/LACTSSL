package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// pendingSecurityEvent 写入一条立即可投递的测试用安全事件。
func pendingSecurityEvent(t *testing.T, m *Manager) *model.SecurityEvent {
	t.Helper()
	event := &model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventTest,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"mail"},
		Notify:       true,
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := m.Store.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	return event
}

// TestConcurrentSecurityWorkersDeliverOnce 回归并发 worker（多实例或重启重叠）
// 读取同一条待投递事件时重复发送邮件的问题。
func TestConcurrentSecurityWorkersDeliverOnce(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sent := 0
	send := func(_ SMTPConfig, _, _, _ string) error {
		mu.Lock()
		sent++
		mu.Unlock()
		// 模拟慢速 SMTP，让两个 worker 都能先读到同一条待投递事件。
		time.Sleep(50 * time.Millisecond)
		return nil
	}
	m.mailSender = send
	event := pendingSecurityEvent(t, m)

	second := New(st)
	second.mailSender = send
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m.processSecurityEvents() }()
	go func() { defer wg.Done(); second.processSecurityEvents() }()
	wg.Wait()

	mu.Lock()
	got := sent
	mu.Unlock()
	if got != 1 {
		t.Fatalf("同一事件被并发投递了 %d 次", got)
	}
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil {
		t.Fatalf("事件应标记为已投递: %+v", loaded)
	}
}

// TestStartSecurityWorkerRecoversPending 验证进程重启后会主动处理上次遗留的
// 待投递事件，而不是等到下一条新事件写入才唤醒 worker。
func TestStartSecurityWorkerRecoversPending(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	sent := 0
	m.mailSender = func(_ SMTPConfig, _, _, _ string) error {
		sent++
		return nil
	}
	event := pendingSecurityEvent(t, m)
	// 模拟上次进程退出前未完成的事件：事件仍在队列中，但当前进程还没有
	// 因为“新事件”调用过 wakeSecurityWorker。
	m.StartSecurityWorker()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := st.GetSecurityEvent(event.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.DeliveredAt != nil {
			if sent != 1 {
				t.Fatalf("恢复投递应只发送一次，实际 %d 次", sent)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("启动 worker 后未恢复待投递事件")
}

// TestDeliverSecurityEventPersistsPerTargetProgress 回归“已发出但最终状态
// 未落库导致整条事件重发”的问题：模拟第二个收件人投递时异常退出，下一轮
// 只能补发失败目标，已成功收件人不得重复发送。
func TestDeliverSecurityEventPersistsPerTargetProgress(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	adminSends := 0
	opsSends := 0
	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		switch to {
		case "admin@example.com":
			adminSends++
			return nil
		case "ops@example.com":
			opsSends++
			panic("simulate process exit during delivery")
		default:
			return nil
		}
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
	func() {
		defer func() { _ = recover() }()
		m.deliverSecurityEvent(event)
	}()

	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Deliveries["mail:admin@example.com"] != "sent" {
		t.Fatalf("成功目标未在崩溃前落库: %+v", loaded.Deliveries)
	}
	if loaded.DeliveredAt != nil {
		t.Fatalf("崩溃时不应标记整条事件已投递: %+v", loaded)
	}

	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		switch to {
		case "admin@example.com":
			adminSends++
		case "ops@example.com":
			opsSends++
		}
		return nil
	}
	m.deliverSecurityEvent(loaded)
	loaded, err = st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adminSends != 1 {
		t.Fatalf("已成功收件人不应重复发送，实际发送 %d 次", adminSends)
	}
	if opsSends != 2 {
		t.Fatalf("失败收件人应在重试时补发，实际发送 %d 次", opsSends)
	}
	if loaded.DeliveredAt == nil {
		t.Fatalf("补发成功后应标记已投递: %+v", loaded)
	}
}

// TestSecurityNotifyWritesNotificationLogs 验证安全提醒的入队与投递结果
// 都写入 source=notify 的操作日志，供“日志 → 通知日志”页签查看。
func TestSecurityNotifyWritesNotificationLogs(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	m.mailSender = func(_ SMTPConfig, _, _, _ string) error { return nil }
	event := model.SecurityEvent{
		UserID:       1,
		Kind:         model.SecurityEventPasswordChanged,
		AccountEmail: "admin@example.com",
		Recipients:   []string{"admin@example.com"},
		Channels:     []string{"mail"},
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
	}
	if err := m.NotifySecurity(event); err != nil {
		t.Fatal(err)
	}
	// NotifySecurity 会唤醒后台 worker；这里再主动跑一次，并用轮询等待
	// 入队/投递两条日志都出现，避免与后台协程竞争导致断言偶发失败。
	m.processSecurityEvents()
	deadline := time.Now().Add(3 * time.Second)
	for {
		logs, _, err := st.ListLogs("notify", 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		keys := map[string]bool{}
		for _, entry := range logs {
			if key, _, ok := i18n.Decode(entry.Message); ok {
				keys[key] = true
				if key == "log.security.notify_delivered" {
					rendered := i18n.Render(i18n.ZhCN, entry.Message)
					if !strings.Contains(rendered, "账户密码已修改") || strings.Contains(rendered, "credential.password.changed") {
						t.Fatalf("通知日志事件类型未渲染为可读文案: %q", rendered)
					}
				}
			}
		}
		if keys["log.security.notify_enqueued"] && keys["log.security.notify_delivered"] {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("通知日志缺少安全提醒记录: %+v", keys)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSecurityDeliveryLeaseScalesWithTargets(t *testing.T) {
	small := securityDeliveryLease(&model.SecurityEvent{Recipients: []string{"a@example.com"}, Channels: []string{"mail"}})
	large := securityDeliveryLease(&model.SecurityEvent{
		Recipients: []string{"a@example.com", "b@example.com", "c@example.com"},
		Channels:   []string{"mail", "telegram"},
	})
	if large <= small {
		t.Fatalf("租约应随目标数量增加: small=%s large=%s", small, large)
	}
	if got := securityDeliveryLease(&model.SecurityEvent{
		Recipients: make([]string, 100),
		Channels:   make([]string, 100),
	}); got != securityNotifyDeliveryMaxLease {
		t.Fatalf("租约应封顶在 %s，实际 %s", securityNotifyDeliveryMaxLease, got)
	}
}

func TestSendMailNotificationUsesConfiguredRecipients(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("admin@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("notify.to", "Ops@Example.com,admin@example.com,backup@example.com"); err != nil {
		t.Fatal(err)
	}
	sent := make([]string, 0, 3)
	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		sent = append(sent, to)
		return nil
	}
	m.sendMailNotification("subject", "body")
	if len(sent) != 3 {
		t.Fatalf("应只发送给配置的收件人，实际 %v", sent)
	}
	if sent[0] != "ops@example.com" || sent[1] != "admin@example.com" || sent[2] != "backup@example.com" {
		t.Fatalf("收件人顺序或去重不正确: %v", sent)
	}
}

func TestSendMailNotificationFallsBackToAccountEmail(t *testing.T) {
	m, st := newNotifyTestManager(t)
	if err := st.SetSetting("smtp.host", "smtp.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("smtp.from", "noreply@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("admin@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	sent := make([]string, 0, 1)
	m.mailSender = func(_ SMTPConfig, to, _, _ string) error {
		sent = append(sent, to)
		return nil
	}
	m.sendMailNotification("subject", "body")
	if len(sent) != 1 || sent[0] != "admin@example.com" {
		t.Fatalf("未配置收件人时应默认引用账户邮箱，实际 %v", sent)
	}
}

func TestCanonicalNotifyRecipients(t *testing.T) {
	got, err := CanonicalNotifyRecipients(" Ops@Example.com , admin@example.com,ops@example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ops@example.com,admin@example.com" {
		t.Fatalf("收件人规范化不正确: %q", got)
	}
	if _, err := CanonicalNotifyRecipients("not-an-email"); err == nil {
		t.Fatal("非法收件人应被拒绝")
	}
	if _, err := ParseNotifyRecipients(""); err != nil {
		t.Fatalf("空值不应报错: %v", err)
	}
}
