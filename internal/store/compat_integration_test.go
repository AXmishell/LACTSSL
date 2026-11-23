package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// openCompatStore 打开真实 PostgreSQL/MySQL 测试库。仅当对应 DSN 环境变量存在时运行。
func openCompatStore(t *testing.T, driver db.Dialect, env string) *Store {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skip(env + " 未设置")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: driver, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("compat-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return New(conn, cipher)
}

// TestMySQLFirstUserWithSmallPool 验证单实例锁 + 极小连接池不会死锁。
func TestMySQLFirstUserWithSmallPool(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_MYSQL_DSN 未设置")
	}
	conn, err := db.OpenWithOptions(db.Options{
		Driver: db.MySQL, DSN: dsn, Migrate: true,
		SingleInstance: true, MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	st := New(conn, nil)
	if _, err := st.DB.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateFirstUser("small-pool@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateFirstUser: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("首个用户 ID 未返回")
	}
	if _, err := st.CreateFirstUser("small-pool-second@example.com", "hash"); err == nil {
		t.Fatal("已有用户后不应再次初始化")
	}
}

// TestMySQLMaintenanceOperations 覆盖 MySQL 5.7 最容易出问题的维护类 SQL：
// 日志按行数清理、ACME 清理、证书批量清理。
func TestMySQLMaintenanceOperations(t *testing.T) {
	st := openCompatStore(t, db.MySQL, "LACTSSL_TEST_MYSQL_DSN")
	now := time.Now().UTC()

	for index := 0; index < 3; index++ {
		if err := st.AddLogEvent(model.LogEntry{
			Category:  model.LogCategoryRuntime,
			Source:    "compat",
			Level:     "info",
			Message:   fmt.Sprintf("runtime log %d", index),
			CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	st.SystemLogMaxRows = 1
	if err := st.CleanupSystemLogs(); err != nil {
		t.Fatalf("CleanupSystemLogs: %v", err)
	}

	suffix := now.UnixNano()
	accountID, err := st.DB.InsertID(`INSERT INTO acme_accounts(public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at)
		VALUES(?,?,?,?,?,'[]','valid',0,NULL,?,?)`,
		fmt.Sprintf("compat-account-%d", suffix), 1, fmt.Sprintf("kid-%d", suffix), "{}", fmt.Sprintf("thumb-%d", suffix), now, now)
	if err != nil {
		t.Fatal(err)
	}
	orderID, err := st.DB.InsertID(`INSERT INTO acme_orders(public_id,endpoint_id,account_id,status,identifiers,replaces,expires_at,certificate_id,error,created_at,updated_at)
		VALUES(?,?,?,'invalid','[]','',NULL,0,'',?,?)`,
		fmt.Sprintf("compat-order-%d", suffix), 1, accountID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	authzID, err := st.DB.InsertID(`INSERT INTO acme_authorizations(public_id,order_id,identifier,identifier_type,wildcard,status,expires_at,created_at,updated_at)
		VALUES(?,?,'compat.example','dns',0,'invalid',NULL,?,?)`,
		fmt.Sprintf("compat-authz-%d", suffix), orderID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO acme_challenges(public_id,authz_id,type,token,status,validated_at,error,attempts,next_check_at,created_at,updated_at)
		VALUES(?,?,'dns-01',?,'invalid',NULL,'',0,NULL,?,?)`,
		fmt.Sprintf("compat-challenge-%d", suffix), authzID, fmt.Sprintf("compat-token-%d", suffix), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CleanupACME(ACMECleanupFilter{Orders: true, Accounts: true, Scope: "all"}, now); err != nil {
		t.Fatalf("CleanupACME: %v", err)
	}

	taskID := int64(990001)
	for index := 0; index < 2; index++ {
		cert := &model.Certificate{
			TaskID:        taskID,
			PrimaryDomain: "compat-cert.example",
			Domains:       []string{"compat-cert.example"},
			CA:            "compat",
			CertPEM:       "cert",
			KeyPEM:        "key",
			FullchainPEM:  "chain",
			NotBefore:     now,
			NotAfter:      now.Add(time.Hour),
			Status:        "valid",
		}
		if err := st.CreateCertificate(cert); err != nil {
			t.Fatal(err)
		}
	}
	dashboard, err := st.Dashboard()
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	found := false
	for _, point := range dashboard.Trend {
		if point.Date == now.Format("2006-01-02") && point.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("MySQL Dashboard 趋势没有包含当天签发的证书")
	}
	if _, err := st.CleanupCertificates(CertCleanupFilter{Scope: "all", KeepLatestPerTask: true}, now); err != nil {
		t.Fatalf("CleanupCertificates: %v", err)
	}
	var remaining int64
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE task_id=?`, taskID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("保留最新证书后剩余 %d 张，期望 1 张", remaining)
	}
}

// TestPostgresMaintenanceOperations 覆盖 PG 上的域名过滤清理与仪表盘趋势。
func TestPostgresMaintenanceOperations(t *testing.T) {
	st := openCompatStore(t, db.Postgres, "LACTSSL_TEST_PG_DSN")
	now := time.Now().UTC()
	domain := fmt.Sprintf("compat-pg-%d.example", now.UnixNano())
	cert := &model.Certificate{
		PrimaryDomain: domain,
		Domains:       []string{domain},
		CA:            "compat",
		CertPEM:       "cert",
		KeyPEM:        "key",
		FullchainPEM:  "chain",
		NotBefore:     now,
		NotAfter:      now.Add(time.Hour),
		Status:        "valid",
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	count, err := st.CountCertificatesForCleanup(CertCleanupFilter{Scope: "all", Domain: domain}, now)
	if err != nil {
		t.Fatalf("CountCertificatesForCleanup: %v", err)
	}
	if count != 1 {
		t.Fatalf("域名清理计数=%d，期望 1", count)
	}
	dashboard, err := st.Dashboard()
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	found := false
	for _, point := range dashboard.Trend {
		if point.Date == now.Format("2006-01-02") && point.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("Dashboard 趋势没有包含当天签发的证书")
	}
	deleted, err := st.CleanupCertificates(CertCleanupFilter{Scope: "all", Domain: domain}, now)
	if err != nil {
		t.Fatalf("CleanupCertificates: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("域名清理删除 %d 张，期望 1 张", deleted)
	}
}
