package dbconvert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

// TestSQLiteToMySQLIntegration 需要真实 MySQL 5.7+ 实例：
//
//	LACTSSL_TEST_MYSQL_CONVERT_DSN='root:testpass@tcp(127.0.0.1:33306)/lactssl_convert' go test ./internal/dbconvert -run TestSQLiteToMySQLIntegration -v
//
// 测试会清空目标数据库中的业务表，请只指向专用测试库。
func TestSQLiteToMySQLIntegration(t *testing.T) {
	dsn := mysqlTestDSN(t)
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: sourcePath, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(source, nil)
	if _, err := st.CreateFirstUser("convert@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("convert.marker", "sqlite"); err != nil {
		t.Fatal(err)
	}
	pool := &model.CAPool{PublicID: "convert-pool", Name: "convert-pool", Strategy: "round_robin", Enabled: true}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	request := &model.CSRRequest{
		PublicID: "convert-csr", CSRPEM: "pem", CSRHash: "hash",
		Identifiers: []string{"example.com"}, PrimaryDomain: "example.com",
		CA: "letsencrypt", Challenge: "dns01", Status: "pending", DNSRoutesHash: "routes",
	}
	if err := st.CreateCSRRequest(request); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	target, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: false, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	resetMySQLTarget(t, target)
	target.Close()

	runner := NewRunner(Options{
		SourceDriver:    db.SQLite,
		SourcePath:      sourcePath,
		TargetDriver:    db.MySQL,
		TargetDSN:       dsn,
		Force:           true,
		DataDir:         dir,
		AllowSameDriver: false,
	})
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("切换失败: %v (phase=%s)", err, report.Phase)
	}
	if report.Phase != PhaseSwitching {
		t.Fatalf("切换阶段异常: %s", report.Phase)
	}

	converted, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: false})
	if err != nil {
		t.Fatal(err)
	}
	defer converted.Close()
	convertedStore := store.New(converted, nil)
	if value, err := convertedStore.GetSetting("convert.marker"); err != nil || value != "sqlite" {
		t.Fatalf("目标设置异常: %q (%v)", value, err)
	}
	if _, err := convertedStore.GetCSRRequestByPublicID("convert-csr"); err != nil {
		t.Fatalf("目标 CSR 申请缺失: %v", err)
	}
	var generated, indexes int64
	if err := converted.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='certificate_requests'
		AND column_name='active_request_key' AND extra LIKE '%GENERATED%'`).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if err := converted.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema=DATABASE() AND table_name='certificate_requests'
		AND index_name='idx_cert_request_active_hash'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if generated != 1 || indexes != 1 {
		t.Fatalf("CSR 生成列唯一索引缺失: generated=%d indexes=%d", generated, indexes)
	}
}

func TestMySQLToSQLiteCopyIntegration(t *testing.T) {
	dsn := mysqlTestDSN(t)
	source, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(); err != nil {
		t.Fatal(err)
	}
	resetMySQLTarget(t, source)
	if err := source.Migrate(); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	st := store.New(source, nil)
	if _, err := st.CreateFirstUser("mysql-source@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("convert.marker", "mysql"); err != nil {
		t.Fatal(err)
	}
	pool := &model.CAPool{PublicID: "mysql-source-pool", Name: "mysql-source-pool", Strategy: "round_robin", Enabled: true}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	request := &model.CSRRequest{
		PublicID: "mysql-source-csr", CSRPEM: "pem", CSRHash: "hash",
		Identifiers: []string{"example.com"}, PrimaryDomain: "example.com",
		CA: "letsencrypt", Challenge: "dns01", Status: "pending", DNSRoutesHash: "routes",
	}
	if err := st.CreateCSRRequest(request); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: targetPath, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, spec := range db.DataTables() {
		result, err := copyTable(context.Background(), source, target, spec, nil)
		if err != nil {
			t.Fatalf("复制 %s 失败: %v", spec.Name, err)
		}
		actual, err := scanTable(context.Background(), target, spec, result.columns)
		if err != nil {
			t.Fatalf("校验 %s 失败: %v", spec.Name, err)
		}
		if actual.Rows != result.Rows || actual.Hash != result.Hash {
			t.Fatalf("表 %s 校验不一致: 源 %d/%s 目标 %d/%s", spec.Name, result.Rows, result.Hash, actual.Rows, actual.Hash)
		}
	}
	targetStore := store.New(target, nil)
	if value, err := targetStore.GetSetting("convert.marker"); err != nil || value != "mysql" {
		t.Fatalf("目标设置异常: %q (%v)", value, err)
	}
}

func mysqlTestDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LACTSSL_TEST_MYSQL_CONVERT_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("LACTSSL_TEST_MYSQL_DSN"))
	}
	if dsn == "" {
		t.Skip("未设置 LACTSSL_TEST_MYSQL_CONVERT_DSN / LACTSSL_TEST_MYSQL_DSN，跳过 MySQL 切换集成测试")
	}
	return dsn
}

func resetMySQLTarget(t *testing.T, conn *db.Handle) {
	t.Helper()
	pinned, err := conn.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	rows, err := pinned.QueryContext(context.Background(), `SELECT table_name FROM information_schema.tables
		WHERE table_schema=DATABASE() AND table_type='BASE TABLE'`)
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
}
