package store

import (
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestSecurityEventLifecycle(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	event := &model.SecurityEvent{
		UserID:     1,
		Kind:       model.SecurityEventPasswordChanged,
		IP:         "203.0.113.10",
		UserAgent:  "Mozilla/5.0",
		Detail:     map[string]string{"sessions": "2"},
		Recipients: []string{"admin@example.com"},
		Channels:   []string{"mail"},
		Notify:     true,
		CreatedAt:  now,
		Deliveries: map[string]string{},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	if event.ID == 0 {
		t.Fatal("事件 ID 未返回")
	}

	pending, err := st.ListPendingSecurityEvents(now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != model.SecurityEventPasswordChanged {
		t.Fatalf("待投递事件不正确: %+v", pending)
	}
	if pending[0].Recipients[0] != "admin@example.com" || pending[0].Channels[0] != "mail" {
		t.Fatalf("事件快照未正确恢复: %+v", pending[0])
	}

	deliveredAt := now.Add(time.Minute)
	if err := st.UpdateSecurityEventDelivery(event.ID, map[string]string{"mail:admin@example.com": "sent"},
		1, "", nil, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	pending, err = st.ListPendingSecurityEvents(now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("已投递事件不应仍在队列: %+v", pending)
	}
	lastError, delivered, err := st.LatestSecurityEventStatus(1)
	if err != nil {
		t.Fatal(err)
	}
	if lastError != "" || delivered == nil || !delivered.Equal(deliveredAt) {
		t.Fatalf("投递状态不正确: %q %v", lastError, delivered)
	}

	// 失败事件在重试耗尽后不再进入队列，但应保留 last_error。
	failed := &model.SecurityEvent{
		UserID:     1,
		Kind:       model.SecurityEventTOTPDisabled,
		Notify:     true,
		CreatedAt:  now,
		Recipients: []string{"admin@example.com"},
		Channels:   []string{"mail"},
	}
	if err := st.CreateSecurityEvent(failed); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSecurityEventDelivery(failed.ID, nil, 3, "smtp down", nil, nil); err != nil {
		t.Fatal(err)
	}
	pending, err = st.ListPendingSecurityEvents(now.Add(48*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("重试耗尽事件不应仍在队列: %+v", pending)
	}
	lastError, delivered, err = st.LatestSecurityEventStatus(1)
	if err != nil {
		t.Fatal(err)
	}
	if lastError != "smtp down" || delivered != nil {
		t.Fatalf("失败状态不正确: %q %v", lastError, delivered)
	}

	// 清理只删除已投递或已放弃的事件。
	oldDelivered := &model.SecurityEvent{
		UserID:    1,
		Kind:      model.SecurityEventPasswordChanged,
		Notify:    true,
		CreatedAt: now.AddDate(0, 0, -100),
	}
	if err := st.CreateSecurityEvent(oldDelivered); err != nil {
		t.Fatal(err)
	}
	oldDeliveredAt := oldDelivered.CreatedAt.Add(time.Minute)
	if err := st.UpdateSecurityEventDelivery(oldDelivered.ID, nil, 1, "", nil, &oldDeliveredAt); err != nil {
		t.Fatal(err)
	}
	// notify=0 的历史事件（阶段 1 登录历史）同样应按保留期清理。
	oldHistory := &model.SecurityEvent{
		UserID:      1,
		Kind:        model.SecurityEventLoginPassword,
		Fingerprint: "old-device",
		Notify:      false,
		CreatedAt:   now.AddDate(0, 0, -100),
	}
	if err := st.CreateSecurityEvent(oldHistory); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.CleanupSecurityEvents(now.AddDate(0, 0, -90))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("应清理 2 条过期事件，实际 %d", deleted)
	}
	events, err := st.ListSecurityEvents(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range events {
		if item.ID == oldDelivered.ID {
			t.Fatal("过期事件未被清理")
		}
	}
}

func TestSecurityEventSMTPConfigSnapshotEncrypted(t *testing.T) {
	st := newTestStore(t)
	event := &model.SecurityEvent{
		UserID: 1,
		Kind:   model.SecurityEventNotifyChanged,
		Notify: true,
		SMTP: &model.SMTPConfigSnapshot{
			Host:     "smtp.old.example.com",
			Port:     465,
			Username: "mailer",
			Password: "smtp-secret-password",
			From:     "noreply@example.com",
			SSL:      true,
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SMTP == nil || loaded.SMTP.Host != "smtp.old.example.com" || loaded.SMTP.Password != "smtp-secret-password" {
		t.Fatalf("SMTP 快照未正确解密: %+v", loaded.SMTP)
	}
	var raw string
	if err := st.DB.QueryRow(`SELECT smtp_snapshot FROM security_events WHERE id=?`, event.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "" || strings.Contains(raw, "smtp-secret-password") {
		t.Fatalf("SMTP 口令不应明文落库: %q", raw)
	}
}

func TestSecurityEventCountsAndDigestLookup(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	for _, item := range []struct {
		kind    string
		created time.Time
	}{
		{model.SecurityEventPasswordChanged, now.Add(-2 * time.Hour)},
		{model.SecurityEventPasswordChanged, now.Add(-10 * time.Minute)},
		{model.SecurityEventDigest, now.Add(-5 * time.Minute)},
	} {
		event := &model.SecurityEvent{
			UserID:    1,
			Kind:      item.kind,
			Notify:    true,
			CreatedAt: item.created,
		}
		if err := st.CreateSecurityEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	count, err := st.CountSecurityEventsSince(1, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("一小时事件数不正确: %d", count)
	}
	exists, err := st.HasSecurityEventKindSince(1, model.SecurityEventDigest, now.Add(-time.Hour))
	if err != nil || !exists {
		t.Fatalf("应找到摘要事件: %v %v", exists, err)
	}
}

func TestHasRecentSecurityLogin(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	if ok, err := st.HasRecentSecurityLogin(1, "device-a", now.Add(-time.Hour)); err != nil || ok {
		t.Fatalf("空历史不应命中: %v %v", ok, err)
	}
	event := &model.SecurityEvent{
		UserID:      1,
		Kind:        model.SecurityEventLoginPassword,
		Fingerprint: "device-a",
		Notify:      false,
		CreatedAt:   now,
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.HasRecentSecurityLogin(1, "device-a", now.Add(-time.Hour)); err != nil || !ok {
		t.Fatalf("同设备应命中: %v %v", ok, err)
	}
	if ok, err := st.HasRecentSecurityLogin(1, "device-b", now.Add(-time.Hour)); err != nil || ok {
		t.Fatalf("不同设备不应命中: %v %v", ok, err)
	}
	if ok, err := st.HasRecentSecurityLogin(1, "device-a", now.Add(time.Hour)); err != nil || ok {
		t.Fatalf("超出窗口不应命中: %v %v", ok, err)
	}
}

func TestClaimSecurityEventPreventsConcurrentDelivery(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	event := &model.SecurityEvent{
		UserID:     1,
		Kind:       model.SecurityEventTest,
		Notify:     true,
		CreatedAt:  now,
		Recipients: []string{"admin@example.com"},
		Channels:   []string{"mail"},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}

	// 第一个 worker 抢到租约。
	claimed, err := st.ClaimSecurityEvent(event.ID, now, now.Add(5*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("首个 worker 应抢到事件: %v %v", claimed, err)
	}
	// 租约未到期时，其他 worker 不能再次投递。
	claimed, err = st.ClaimSecurityEvent(event.ID, now, now.Add(5*time.Minute))
	if err != nil || claimed {
		t.Fatalf("租约内不应重复抢占: %v %v", claimed, err)
	}
	// 租约到期后事件重新可投递，保证进程崩溃后能恢复。
	claimed, err = st.ClaimSecurityEvent(event.ID, now.Add(6*time.Minute), now.Add(10*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("租约到期后应重新可抢占: %v %v", claimed, err)
	}
	// 投递完成后不再进入队列。
	deliveredAt := now.Add(7 * time.Minute)
	if err := st.UpdateSecurityEventDelivery(event.ID, map[string]string{"mail:admin@example.com": "sent"},
		1, "", nil, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	claimed, err = st.ClaimSecurityEvent(event.ID, now.Add(20*time.Minute), now.Add(25*time.Minute))
	if err != nil || claimed {
		t.Fatalf("已投递事件不应再被抢占: %v %v", claimed, err)
	}
}

func TestHasPendingSecurityEventsIncludesScheduledRetry(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	event := &model.SecurityEvent{
		UserID:     1,
		Kind:       model.SecurityEventTest,
		Notify:     true,
		CreatedAt:  now,
		Recipients: []string{"admin@example.com"},
		Channels:   []string{"mail"},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.HasPendingSecurityEvents(); err != nil || !pending {
		t.Fatalf("新事件应视为积压: %v %v", pending, err)
	}

	next := now.Add(time.Hour)
	if err := st.UpdateSecurityEventDelivery(event.ID, map[string]string{"mail:admin@example.com": "failed"},
		1, "smtp down", &next, nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.HasPendingSecurityEvents(); err != nil || !pending {
		t.Fatalf("等待重试的事件仍应视为积压: %v %v", pending, err)
	}

	deliveredAt := now.Add(2 * time.Hour)
	if err := st.UpdateSecurityEventDelivery(event.ID, map[string]string{"mail:admin@example.com": "sent"},
		1, "", nil, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.HasPendingSecurityEvents(); err != nil || pending {
		t.Fatalf("已投递事件不应视为积压: %v %v", pending, err)
	}
}
