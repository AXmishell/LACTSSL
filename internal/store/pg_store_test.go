package store

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func openTestPG(t *testing.T) *Store {
	t.Helper()
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
	return New(conn, cipher)
}

func TestPostgresStoreBasics(t *testing.T) {
	st := openTestPG(t)
	// 清理上轮数据，允许重复执行。
	for _, table := range []string{"certificates", "tasks", "ca_pool_items", "ca_pools", "users"} {
		if _, err := st.DB.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	user, err := st.CreateUser("pg@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID == 0 {
		t.Fatal("用户 ID 未返回")
	}
	now := time.Now().UTC()
	cert := &model.Certificate{
		PrimaryDomain: "pg.example.com",
		Domains:       []string{"pg.example.com"},
		CA:            "test",
		CertPEM:       "cert",
		KeyPEM:        "key",
		FullchainPEM:  "chain",
		NotBefore:     now,
		NotAfter:      now.Add(time.Hour),
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	if cert.ID == 0 {
		t.Fatal("证书 ID 未返回")
	}
	loaded, err := st.GetCertificate(cert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrimaryDomain != cert.PrimaryDomain || !loaded.KeyAvailable {
		t.Fatalf("证书读取不一致: %+v", loaded)
	}
	task := &model.Task{Name: "pg-task", Domains: []string{"pg.example.com"}, CA: "test"}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if task.ID == 0 {
		t.Fatal("任务 ID 未返回")
	}
}

func TestPostgresCreateFirstUser(t *testing.T) {
	st := openTestPG(t)
	if _, err := st.DB.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateFirstUser("first-pg@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID == 0 {
		t.Fatal("首个用户 ID 未返回")
	}
	if _, err := st.CreateFirstUser("second-pg@example.com", "hash"); err == nil {
		t.Fatal("第二个首次初始化请求应被拒绝")
	}
}

// TestPostgresSecurityEventTerminalUpdate 回归 PostgreSQL 上投递结果写不回
// 数据库的问题：旧实现用 `CASE WHEN ? IS NULL` 判断是否清空 SMTP 快照，
// PostgreSQL 无法推断该参数类型（42P18），导致 delivered_at/attempts 永远
// 不更新，UI 一直显示“待投递”。
func TestPostgresSecurityEventTerminalUpdate(t *testing.T) {
	st := openTestPG(t)
	if _, err := st.DB.Exec(`DELETE FROM security_events WHERE user_id=?`, 990001); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.DB.Exec(`DELETE FROM security_events WHERE user_id=?`, 990001)
	})
	event := &model.SecurityEvent{
		UserID:     990001,
		Kind:       model.SecurityEventTest,
		Notify:     true,
		Recipients: []string{"pg@example.com"},
		Channels:   []string{"mail"},
		CreatedAt:  time.Now().UTC(),
		SMTP: &model.SMTPConfigSnapshot{
			Host:     "smtp.example.com",
			Port:     465,
			From:     "noreply@example.com",
			Password: "snapshot-secret",
		},
	}
	if err := st.CreateSecurityEvent(event); err != nil {
		t.Fatal(err)
	}
	deliveredAt := time.Now().UTC()
	if err := st.UpdateSecurityEventDelivery(event.ID, map[string]string{
		"mail:pg@example.com": "sent",
	}, 1, "", nil, &deliveredAt); err != nil {
		t.Fatalf("投递结果写入失败: %v", err)
	}
	loaded, err := st.GetSecurityEvent(event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveredAt == nil || loaded.DeliveredAt.Sub(deliveredAt).Abs() > time.Microsecond {
		t.Fatalf("delivered_at 未写入: %+v", loaded)
	}
	if loaded.Attempts != 1 {
		t.Fatalf("attempts 未写入: %+v", loaded)
	}
	if loaded.SMTP != nil {
		t.Fatalf("投递结束后应清空 SMTP 快照: %+v", loaded.SMTP)
	}
}

func TestPostgresCAPoolCursorConcurrent(t *testing.T) {
	st := openTestPG(t)
	if _, err := st.DB.Exec(`DELETE FROM ca_pool_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM ca_pools`); err != nil {
		t.Fatal(err)
	}
	pool := &model.CAPool{PublicID: "pg-pool", Name: "pg-pool", Strategy: "round_robin", Enabled: true}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	const workers = 40
	results := make(chan int64, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cursor, err := st.NextCAPoolCursor(pool.ID)
			if err != nil {
				t.Errorf("cursor: %v", err)
				return
			}
			results <- cursor
		}()
	}
	wg.Wait()
	close(results)
	seen := map[int64]bool{}
	for cursor := range results {
		if seen[cursor] {
			t.Fatalf("游标重复: %d", cursor)
		}
		seen[cursor] = true
	}
	if len(seen) != workers {
		t.Fatalf("游标数量 %d，期望 %d", len(seen), workers)
	}
	_ = fmt.Sprint()
}
