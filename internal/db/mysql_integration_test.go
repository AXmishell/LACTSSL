package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

// TestMySQLIntegration 需要真实的 MySQL 5.7+ 实例：
//
//	LACTSSL_TEST_MYSQL_DSN='root:testpass@tcp(127.0.0.1:33306)/lactssl' go test ./internal/db -run TestMySQLIntegration -v
//
// 测试会清空目标数据库中的业务表，请只指向专用测试库。
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 LACTSSL_TEST_MYSQL_DSN，跳过 MySQL 集成测试")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: true, SingleInstance: true})
	if err != nil {
		t.Fatalf("打开 MySQL 失败: %v", err)
	}
	defer conn.Close()
	if conn.Dialect() != db.MySQL {
		t.Fatalf("方言异常: %s", conn.Dialect())
	}
	version, err := conn.SchemaVersion()
	if err != nil || version != db.CurrentSchemaVersion() {
		t.Fatalf("结构版本异常: %d (%v)", version, err)
	}
	cleanupMySQL(t, conn)

	st := store.New(conn, nil)
	if err := st.SetSetting("integration.mysql", "ok"); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	if value, err := st.GetSetting("integration.mysql"); err != nil || value != "ok" {
		t.Fatalf("读取设置失败: %q (%v)", value, err)
	}
	if err := st.SetSetting("integration.mysql", "updated"); err != nil {
		t.Fatalf("更新设置失败: %v", err)
	}
	if value, err := st.GetSetting("integration.mysql"); err != nil || value != "updated" {
		t.Fatalf("更新设置结果异常: %q (%v)", value, err)
	}
	if err := st.DeleteSetting("integration.mysql"); err != nil {
		t.Fatalf("删除设置失败: %v", err)
	}

	if _, err := st.CreateFirstUser("mysql-first@example.com", "hash"); err != nil {
		t.Fatalf("创建首个用户失败: %v", err)
	}
	if _, err := st.CreateFirstUser("mysql-second@example.com", "hash"); err == nil {
		t.Fatal("已有用户后不应再次初始化")
	}
	user, err := st.CreateUser("mysql-integration@example.com", "hash")
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	session := &model.Session{
		ID: "mysql-integration-session", UserID: user.ID,
		CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := st.CreateSession(session); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	// ON CONFLICT DO NOTHING 必须能在 MySQL 下重复执行。
	if err := st.CreateSession(session); err != nil {
		t.Fatalf("重复创建会话失败: %v", err)
	}

	st.AddLog("integration", 1, "info", "mysql integration log")
	logs, total, err := st.ListLogsFiltered(store.LogFilter{Query: "INTEGRATION"}, 10, 0)
	if err != nil || total == 0 || len(logs) == 0 {
		t.Fatalf("日志查询失败: total=%d len=%d (%v)", total, len(logs), err)
	}
	if _, err := st.Dashboard(); err != nil {
		t.Fatalf("仪表盘查询失败: %v", err)
	}

	pool := &model.CAPool{PublicID: "mysql-pool", Name: "mysql-pool", Strategy: "round_robin", Enabled: true}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatalf("创建 CA 池失败: %v", err)
	}
	first, err := st.NextCAPoolCursor(pool.ID)
	if err != nil || first != 0 {
		t.Fatalf("游标第一次分配异常: %d (%v)", first, err)
	}
	second, err := st.NextCAPoolCursor(pool.ID)
	if err != nil || second != 1 {
		t.Fatalf("游标第二次分配异常: %d (%v)", second, err)
	}
	loadedPool, err := st.GetCAPool(pool.ID)
	if err != nil || loadedPool.Cursor != 2 {
		t.Fatalf("读取 CA 池游标异常: %+v (%v)", loadedPool, err)
	}
	if pools, err := st.ListCAPools(); err != nil || len(pools) != 1 {
		t.Fatalf("列出 CA 池异常: %+v (%v)", pools, err)
	}

	request := func(publicID string) *model.CSRRequest {
		return &model.CSRRequest{
			PublicID: publicID, CSRPEM: "pem", CSRHash: "hash",
			Identifiers: []string{"example.com"}, PrimaryDomain: "example.com",
			CA: "letsencrypt", Challenge: "dns01", Status: "pending",
			DNSRoutesHash: "routes",
		}
	}
	if err := st.CreateCSRRequest(request("mysql-csr-1")); err != nil {
		t.Fatalf("创建 CSR 申请失败: %v", err)
	}
	err = st.CreateCSRRequest(request("mysql-csr-2"))
	if err == nil || !store.CSRRequestExists(err) {
		t.Fatalf("在途申请唯一索引未生效: %v", err)
	}
	if _, err := conn.Exec(`UPDATE certificate_requests SET status='failed' WHERE public_id=?`, "mysql-csr-1"); err != nil {
		t.Fatalf("更新申请状态失败: %v", err)
	}
	if err := st.CreateCSRRequest(request("mysql-csr-3")); err != nil {
		t.Fatalf("终态后重新创建申请失败: %v", err)
	}
}

// TestMySQLUpgradeRepairsLegacySchema 验证从 schemaVersion 23 升级时，
// 历史上被写成 VARCHAR(2048) 的文本列会恢复为 LONGTEXT，不透明列改为二进制排序规则。
func TestMySQLUpgradeRepairsLegacySchema(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 LACTSSL_TEST_MYSQL_DSN，跳过 MySQL 升级测试")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Migrate(); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DELETE FROM tasks`,
		`DELETE FROM logs`,
		`ALTER TABLE tasks MODIFY COLUMN pipeline VARCHAR(2048) NOT NULL`,
		`ALTER TABLE logs MODIFY COLUMN fields VARCHAR(2048) NOT NULL`,
		`ALTER TABLE logs DROP COLUMN request_id`,
		`ALTER TABLE acme_orders MODIFY COLUMN public_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL`,
		`INSERT INTO logs(category,source,ref_type,ref_id,level,message,fields,created_at)
			VALUES('operation','compat','',0,'info','upgrade','',UTC_TIMESTAMP(6))`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			_ = conn.Close()
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := conn.SetSchemaVersion(23); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	_ = conn.Close()

	conn, err = db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	checks := []struct {
		table, column string
		wantType      string
		wantCollation string
		wantNullable  string
	}{
		{"tasks", "pipeline", "longtext", "", ""},
		{"logs", "fields", "longtext", "", ""},
		{"logs", "request_id", "longtext", "", "NO"},
		{"acme_orders", "public_id", "varchar(64)", "ascii_bin", ""},
	}
	for _, check := range checks {
		var columnType, collation, nullable string
		if err := conn.QueryRow(`SELECT column_type,COALESCE(collation_name,''),is_nullable FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, check.table, check.column).
			Scan(&columnType, &collation, &nullable); err != nil {
			t.Fatalf("%s.%s: %v", check.table, check.column, err)
		}
		if columnType != check.wantType {
			t.Fatalf("%s.%s 类型=%s，期望 %s", check.table, check.column, columnType, check.wantType)
		}
		if check.wantCollation != "" && collation != check.wantCollation {
			t.Fatalf("%s.%s 排序规则=%s，期望 %s", check.table, check.column, collation, check.wantCollation)
		}
		if check.wantNullable != "" && nullable != check.wantNullable {
			t.Fatalf("%s.%s is_nullable=%s，期望 %s", check.table, check.column, nullable, check.wantNullable)
		}
	}
}

func cleanupMySQL(t *testing.T, conn *db.Handle) {
	t.Helper()
	pinned, err := conn.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	rows, err := pinned.QueryContext(context.Background(), `SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = rows.Close()
	if _, err := pinned.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS=0`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pinned.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS=1`) }()
	for _, name := range names {
		if _, err := pinned.ExecContext(context.Background(), `DROP TABLE IF EXISTS `+conn.Dialect().QuoteIdent(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
}
