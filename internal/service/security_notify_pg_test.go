package service

import (
	"os"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

// TestPostgresSecurityEventFullFlow 在真实 PostgreSQL 上回归完整链路：
// 事件入队 -> worker 投递 -> 逐目标/终态落库 -> 通知日志。重点验证
// delivered_at 能写回（旧实现 42P18 会失败），以及日志不会缺失。
func TestPostgresSecurityEventFullFlow(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	const userID int64 = 990003
	cleanup := func() {
		_, _ = st.DB.Exec(`DELETE FROM security_events WHERE user_id=?`, userID)
		_, _ = st.DB.Exec(`DELETE FROM logs WHERE source='notify' AND ref_id=?`, userID)
	}
	if _, err := st.DB.Exec(`DELETE FROM security_events WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	m := New(st)
	sent := 0
	m.mailSender = func(_ SMTPConfig, _, _, _ string) error {
		sent++
		return nil
	}
	event := model.SecurityEvent{
		UserID:       userID,
		Kind:         model.SecurityEventPasswordChanged,
		AccountEmail: "pg@example.com",
		Recipients:   []string{"pg@example.com"},
		Channels:     []string{"mail"},
		CreatedAt:    time.Now().UTC(),
		Deliveries:   map[string]string{},
		SMTP: &model.SMTPConfigSnapshot{
			Host: "smtp.example.com",
			Port: 465,
			From: "noreply@example.com",
		},
	}
	if err := m.NotifySecurity(event); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		events, err := st.ListSecurityEvents(userID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			t.Fatal("安全事件未写入 PostgreSQL")
		}
		loaded := &events[0]
		if loaded.DeliveredAt != nil {
			if loaded.DeliveryState() != model.SecurityEventStateDelivered {
				t.Fatalf("投递状态不正确: %+v", loaded)
			}
			if loaded.Attempts < 1 {
				t.Fatalf("attempts 未落库: %+v", loaded)
			}
			if loaded.SMTP != nil {
				t.Fatalf("终态应清空 SMTP 快照: %+v", loaded.SMTP)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 PostgreSQL 投递结果落库超时: %+v", loaded)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sent != 1 {
		t.Fatalf("应只发送一次，实际 %d 次", sent)
	}

	logs, err := st.ListLogsByRef("notify", userID, 20)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, entry := range logs {
		if key, _, ok := i18n.Decode(entry.Message); ok {
			keys[key] = true
		}
	}
	if !keys["log.security.notify_enqueued"] || !keys["log.security.notify_delivered"] {
		t.Fatalf("PostgreSQL 通知日志不完整: %+v", keys)
	}
}
