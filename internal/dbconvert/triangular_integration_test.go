package dbconvert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

// TestTriangularSwitchIntegration 验证 SQLite、PostgreSQL 18、MySQL 5.7+
// 六个跨驱动方向的完整切换链路：
//
//	SQLite → PG → MySQL → SQLite
//	SQLite → MySQL → PG → SQLite
//
// 需要本机提供 pg_dump/pg_restore、mysqldump/mysql，以及两个可写的测试库：
//
//	LACTSSL_TEST_PG_DSN       postgres://lactssl:pass@127.0.0.1:5432/lactssl_p1?sslmode=disable
//	LACTSSL_TEST_PG_DSN_B     postgres://lactssl:pass@127.0.0.1:5432/lactssl_p2?sslmode=disable
//	LACTSSL_TEST_MYSQL_DSN    mysql://lactssl:pass@127.0.0.1:33306/lactssl_m1?tls=disable
//	LACTSSL_TEST_MYSQL_DSN_B  mysql://lactssl:pass@127.0.0.1:33306/lactssl_m2?tls=disable
//
// 测试会用 Force 清空目标库，请只指向专用测试库。
func TestTriangularSwitchIntegration(t *testing.T) {
	pgA := strings.TrimSpace(os.Getenv("LACTSSL_TEST_PG_DSN"))
	pgB := strings.TrimSpace(os.Getenv("LACTSSL_TEST_PG_DSN_B"))
	mysqlA := strings.TrimSpace(os.Getenv("LACTSSL_TEST_MYSQL_DSN"))
	mysqlB := strings.TrimSpace(os.Getenv("LACTSSL_TEST_MYSQL_DSN_B"))
	if pgA == "" || pgB == "" || mysqlA == "" || mysqlB == "" {
		t.Skip("未设置三角切换集成测试所需的 PG/MySQL DSN")
	}
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	seed := filepath.Join(dir, "seed.db")
	out1 := filepath.Join(dir, "out1.db")
	out2 := filepath.Join(dir, "out2.db")
	cipher, err := util.NewCipher([]byte(strings.Repeat("triangular-switch-", 4)))
	if err != nil {
		t.Fatal(err)
	}

	seedDatabase(t, seed, cipher)
	expected := snapshotDatabase(t, seed, db.SQLite, "", cipher)

	type step struct {
		name       string
		from, to   db.Dialect
		fromPath   string
		fromDSN    string
		toPath     string
		toDSN      string
		verifyPath string
		verifyDSN  string
	}
	steps := []step{
		{name: "SQLite→PostgreSQL", from: db.SQLite, to: db.Postgres, fromPath: seed, toDSN: pgA, verifyDSN: pgA},
		{name: "PostgreSQL→MySQL", from: db.Postgres, to: db.MySQL, fromDSN: pgA, toDSN: mysqlA, verifyDSN: mysqlA},
		{name: "MySQL→SQLite", from: db.MySQL, to: db.SQLite, fromDSN: mysqlA, toPath: out1, verifyPath: out1},
		{name: "SQLite→MySQL", from: db.SQLite, to: db.MySQL, fromPath: out1, toDSN: mysqlB, verifyDSN: mysqlB},
		{name: "MySQL→PostgreSQL", from: db.MySQL, to: db.Postgres, fromDSN: mysqlB, toDSN: pgB, verifyDSN: pgB},
		{name: "PostgreSQL→SQLite", from: db.Postgres, to: db.SQLite, fromDSN: pgB, toPath: out2, verifyPath: out2},
	}
	for _, item := range steps {
		item := item
		t.Run(item.name, func(t *testing.T) {
			runner := NewRunner(Options{
				SourceDriver: item.from,
				SourcePath:   item.fromPath,
				SourceDSN:    item.fromDSN,
				TargetDriver: item.to,
				TargetPath:   item.toPath,
				TargetDSN:    item.toDSN,
				Force:        true,
				DataDir:      dataDir,
				Cipher:       cipher,
			})
			report, err := runner.Run(context.Background())
			if err != nil {
				t.Fatalf("切换失败: %v (phase=%s)", err, report.Phase)
			}
			if report.Phase != PhaseSwitching {
				t.Fatalf("切换没有进入 switching 阶段: %s", report.Phase)
			}
			if report.BackupPath == "" {
				t.Fatal("切换前备份路径为空")
			}
			if entries, err := os.ReadDir(report.BackupPath); err != nil || len(entries) == 0 {
				t.Fatalf("切换前备份目录为空或不可读: %s (%v)", report.BackupPath, err)
			}
			if report.Rows == 0 || len(report.Tables) != len(db.DataTables()) {
				t.Fatalf("复制结果异常: rows=%d tables=%d", report.Rows, len(report.Tables))
			}
			actual := snapshotDatabase(t, item.verifyPath, item.to, item.verifyDSN, cipher)
			if actual != expected {
				t.Fatalf("切换后数据不一致:\n期望 %+v\n实际 %+v", expected, actual)
			}
			if item.to.IsMySQL() {
				assertMySQLGeneratedIndex(t, item.verifyDSN)
			}
		})
	}

	// 同构迁移补充验证：SQLite → SQLite。
	t.Run("SQLite→SQLite", func(t *testing.T) {
		target := filepath.Join(dir, "out3.db")
		runner := NewRunner(Options{
			SourceDriver:    db.SQLite,
			SourcePath:      out2,
			TargetDriver:    db.SQLite,
			TargetPath:      target,
			AllowSameDriver: true,
			DataDir:         dataDir,
			Cipher:          cipher,
		})
		report, err := runner.Run(context.Background())
		if err != nil {
			t.Fatalf("SQLite→SQLite 切换失败: %v (phase=%s)", err, report.Phase)
		}
		if actual := snapshotDatabase(t, target, db.SQLite, "", cipher); actual != expected {
			t.Fatalf("SQLite→SQLite 数据不一致:\n期望 %+v\n实际 %+v", expected, actual)
		}
	})

	// 同构迁移补充验证：PostgreSQL → PostgreSQL。
	t.Run("PostgreSQL→PostgreSQL", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("LACTSSL_TEST_PG_DSN_SAME"))
		if dsn == "" {
			t.Skip("未设置 LACTSSL_TEST_PG_DSN_SAME")
		}
		resetTarget(t, db.Postgres, dsn)
		runner := NewRunner(Options{
			SourceDriver:    db.Postgres,
			SourceDSN:       pgB,
			TargetDriver:    db.Postgres,
			TargetDSN:       dsn,
			AllowSameDriver: true,
			DataDir:         dataDir,
			Cipher:          cipher,
		})
		report, err := runner.Run(context.Background())
		if err != nil {
			t.Fatalf("PG→PG 切换失败: %v (phase=%s)", err, report.Phase)
		}
		if actual := snapshotDatabase(t, "", db.Postgres, dsn, cipher); actual != expected {
			t.Fatalf("PG→PG 数据不一致:\n期望 %+v\n实际 %+v", expected, actual)
		}
	})

	// 同构迁移补充验证：MySQL → MySQL。
	t.Run("MySQL→MySQL", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("LACTSSL_TEST_MYSQL_DSN_SAME"))
		if dsn == "" {
			t.Skip("未设置 LACTSSL_TEST_MYSQL_DSN_SAME")
		}
		resetTarget(t, db.MySQL, dsn)
		runner := NewRunner(Options{
			SourceDriver:    db.MySQL,
			SourceDSN:       mysqlB,
			TargetDriver:    db.MySQL,
			TargetDSN:       dsn,
			AllowSameDriver: true,
			DataDir:         dataDir,
			Cipher:          cipher,
		})
		report, err := runner.Run(context.Background())
		if err != nil {
			t.Fatalf("MySQL→MySQL 切换失败: %v (phase=%s)", err, report.Phase)
		}
		if actual := snapshotDatabase(t, "", db.MySQL, dsn, cipher); actual != expected {
			t.Fatalf("MySQL→MySQL 数据不一致:\n期望 %+v\n实际 %+v", expected, actual)
		}
	})
}

type triSnapshot struct {
	Marker        string
	Secret        string
	Email         string
	Users         int64
	DNSName       string
	DNSToken      string
	Pools         int64
	PoolName      string
	PoolItem      string
	Monitors      int64
	MonitorTarget string
	CSRs          int64
	CSRStatus     string
	CSRIdentifier string
	Logs          int64
}

func (s triSnapshot) String() string {
	return fmt.Sprintf("marker=%s users=%d dns=%s/%s pools=%d/%s/%s monitors=%d/%s csrs=%d/%s/%s logs=%d",
		s.Marker, s.Users, s.DNSName, s.DNSToken, s.Pools, s.PoolName, s.PoolItem,
		s.Monitors, s.MonitorTarget, s.CSRs, s.CSRStatus, s.CSRIdentifier, s.Logs)
}

func seedDatabase(t *testing.T, path string, cipher *util.Cipher) {
	t.Helper()
	conn := openTestDB(t, db.SQLite, path, "")
	defer conn.Close()
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	if _, err := st.CreateFirstUser("triangular@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("tri.marker", "triangular"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecret("tri.secret", "hidden-value"); err != nil {
		t.Fatal(err)
	}
	account := &model.DNSAccount{
		Name: "tri-dns", Provider: "manual",
		Config: map[string]string{"token": "dns-secret"},
		Remark: "triangular",
	}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	pool := &model.CAPool{
		PublicID: "tri-pool", Name: "tri-pool", Strategy: "round_robin", Enabled: true,
		Items: []model.CAPoolItem{{CAValue: "letsencrypt", Priority: 1, Weight: 1, Enabled: true}},
	}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	monitor := &model.Monitor{Name: "tri-monitor", Target: "https://example.com", IntervalHour: 24, Enabled: true}
	if err := st.CreateMonitor(monitor); err != nil {
		t.Fatal(err)
	}
	request := &model.CSRRequest{
		PublicID: "tri-csr", CSRPEM: "pem", CSRHash: "hash",
		Identifiers:   []string{"example.com", "www.example.com"},
		PrimaryDomain: "example.com", CA: "letsencrypt", Challenge: "dns01",
		Status: "pending", DNSRoutesHash: "routes",
	}
	if err := st.CreateCSRRequest(request); err != nil {
		t.Fatal(err)
	}
	st.AddLog("triangular", 1, "info", "triangular switch marker log")
}

func snapshotDatabase(t *testing.T, path string, driver db.Dialect, dsn string, cipher *util.Cipher) triSnapshot {
	t.Helper()
	conn := openTestDB(t, driver, path, dsn)
	defer conn.Close()
	st := store.New(conn, cipher)
	var snapshot triSnapshot
	snapshot.Marker, _ = st.GetSetting("tri.marker")
	snapshot.Secret, _ = st.GetSecret("tri.secret")
	var err error
	if snapshot.Users, err = countRows(conn, "users"); err != nil {
		t.Fatal(err)
	}
	user, err := st.GetUserByEmail("triangular@example.com")
	if err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	snapshot.Email = user.Email

	accounts, err := st.ListDNSAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) > 0 {
		snapshot.DNSName = accounts[0].Name
		snapshot.DNSToken = accounts[0].Config["token"]
	}

	pools, err := st.ListCAPools()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Pools = int64(len(pools))
	if len(pools) > 0 {
		snapshot.PoolName = pools[0].Name
		if len(pools[0].Items) > 0 {
			snapshot.PoolItem = pools[0].Items[0].CAValue
		}
	}

	monitors, err := st.ListMonitors()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Monitors = int64(len(monitors))
	if len(monitors) > 0 {
		snapshot.MonitorTarget = monitors[0].Target
	}

	if snapshot.CSRs, err = countRows(conn, "certificate_requests"); err != nil {
		t.Fatal(err)
	}
	request, err := st.GetCSRRequestByPublicID("tri-csr")
	if err != nil {
		t.Fatalf("读取 CSR 失败: %v", err)
	}
	snapshot.CSRStatus = request.Status
	if len(request.Identifiers) > 0 {
		snapshot.CSRIdentifier = request.Identifiers[1]
	}

	_, total, err := st.ListLogsFiltered(store.LogFilter{Query: "TRIANGULAR"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Logs = total

	version, err := conn.SchemaVersion()
	if err != nil || version != db.CurrentSchemaVersion() {
		t.Fatalf("结构版本异常: %d (%v)", version, err)
	}
	return snapshot
}

// resetTarget 清空同构迁移的目标库；同构迁移本身禁止使用 Force。
func resetTarget(t *testing.T, driver db.Dialect, dsn string) {
	t.Helper()
	conn := openTestDB(t, driver, "", dsn)
	defer conn.Close()
	if err := clearTarget(context.Background(), conn); err != nil {
		t.Fatalf("清空同构目标库失败: %v", err)
	}
}

func openTestDB(t *testing.T, driver db.Dialect, path, dsn string) *db.Handle {
	t.Helper()
	conn, err := db.OpenWithOptions(db.Options{Driver: driver, Path: path, DSN: dsn, Migrate: false, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func countRows(conn *db.Handle, table string) (int64, error) {
	var count int64
	err := conn.QueryRow(`SELECT COUNT(*) FROM ` + conn.Dialect().QuoteIdent(table)).Scan(&count)
	return count, err
}

func assertMySQLGeneratedIndex(t *testing.T, dsn string) {
	t.Helper()
	conn := openTestDB(t, db.MySQL, "", dsn)
	defer conn.Close()
	var generated, indexes int64
	if err := conn.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='certificate_requests'
		AND column_name='active_request_key' AND extra LIKE '%GENERATED%'`).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema=DATABASE() AND table_name='certificate_requests'
		AND index_name='idx_cert_request_active_hash'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if generated != 1 || indexes != 1 {
		t.Fatalf("CSR 生成列唯一索引缺失: generated=%d indexes=%d", generated, indexes)
	}
}
