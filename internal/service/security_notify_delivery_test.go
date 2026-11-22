package service

import (
	"sync"
	"testing"
	"time"

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
