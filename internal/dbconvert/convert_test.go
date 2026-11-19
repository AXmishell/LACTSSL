package dbconvert

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func testCipher(t *testing.T) *util.Cipher {
	t.Helper()
	cipher, err := util.NewCipher([]byte("convert-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func seedSQLite(t *testing.T, path string) (*db.Handle, *store.Store) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, testCipher(t))
	user, err := st.CreateUser("convert@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, "JBSWY3DPEHPK3PXP", true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cert := &model.Certificate{
		PrimaryDomain: "convert.example.com",
		Domains:       []string{"convert.example.com"},
		CA:            "test",
		CertPEM:       "cert-pem",
		KeyPEM:        "key-pem",
		FullchainPEM:  "fullchain",
		NotBefore:     now,
		NotAfter:      now.Add(time.Hour),
		Status:        "valid",
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "convert-task", Domains: []string{"convert.example.com"}, CA: "test", Enabled: true}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	pool := &model.CAPool{PublicID: "convert-pool", Name: "convert-pool", Strategy: "round_robin", Enabled: true}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	return conn, st
}

func TestSQLiteToPostgresRoundTrip(t *testing.T) {
	pgDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	if pgDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, st := seedSQLite(t, sourcePath)
	defer source.Close()

	runner := NewRunner(Options{
		Source:       source,
		SourceDriver: db.SQLite,
		SourcePath:   sourcePath,
		TargetDriver: db.Postgres,
		TargetDSN:    pgDSN,
		Force:        true,
		DataDir:      dir,
		Cipher:       testCipher(t),
		Version:      "test",
	})
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("SQLite -> PG 失败: %v", err)
	}
	source.Unfreeze()
	if report.Rows == 0 {
		t.Fatal("转换行数为 0")
	}
	if report.BackupPath == "" {
		t.Fatal("未生成备份")
	}

	pg, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: pgDSN, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	pgStore := store.New(pg, testCipher(t))
	loaded, err := pgStore.GetCertificate(1)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrimaryDomain != "convert.example.com" || !loaded.KeyAvailable {
		t.Fatalf("PG 证书内容不一致: %+v", loaded)
	}
	if _, err := pgStore.GetUser(); err != nil {
		t.Fatal(err)
	}

	// 反向转换：PG -> 新的 SQLite 文件。
	backPath := filepath.Join(dir, "roundtrip.db")
	backRunner := NewRunner(Options{
		Source:       pg,
		SourceDriver: db.Postgres,
		SourceDSN:    pgDSN,
		TargetDriver: db.SQLite,
		TargetPath:   backPath,
		Force:        false,
		DataDir:      dir,
		Cipher:       testCipher(t),
		Version:      "test",
	})
	backReport, err := backRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("PG -> SQLite 失败: %v", err)
	}
	if backReport.Rows != report.Rows {
		t.Fatalf("往返行数不一致: %d != %d", backReport.Rows, report.Rows)
	}
	back, err := db.Open(backPath)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	backStore := store.New(back, testCipher(t))
	cert, err := backStore.GetCertificate(1)
	if err != nil {
		t.Fatal(err)
	}
	if cert.PrimaryDomain != "convert.example.com" {
		t.Fatalf("往返证书不一致: %+v", cert)
	}
	if _, err := backStore.GetUser(); err != nil {
		t.Fatal(err)
	}
	_ = st
}

func TestCopyInterruptionUnfreezesSource(t *testing.T) {
	pgDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	if pgDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, _ := seedSQLite(t, sourcePath)
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := NewRunner(Options{
		Source:       source,
		SourceDriver: db.SQLite,
		SourcePath:   sourcePath,
		TargetDriver: db.Postgres,
		TargetDSN:    pgDSN,
		Force:        true,
		DataDir:      dir,
		Cipher:       testCipher(t),
		Progress: func(p Progress) {
			if p.Phase == PhaseCopy && p.TableIndex == 2 {
				cancel()
			}
		},
	})
	if _, err := runner.Run(ctx); err == nil {
		t.Fatal("取消复制应返回错误")
	}
	if source.Gate().Frozen() {
		t.Fatal("复制中断后应解冻源库")
	}
	if err := source.QueryRow(`SELECT COUNT(*) FROM users`).Scan(new(int)); err != nil {
		t.Fatalf("源库应仍可读: %v", err)
	}
}

func TestSwitchFailureUnfreezesSource(t *testing.T) {
	pgDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	if pgDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, _ := seedSQLite(t, sourcePath)
	defer source.Close()
	// 配置路径指向目录，RewriteDatabase 必然失败。
	manager := NewManager(source, db.SQLite, sourcePath, "", dir, dir, testCipher(t), "test")
	maintenance := true
	manager.OnMaintenance = func(active bool) { maintenance = active }
	if _, err := manager.Start(StartRequest{TargetDriver: db.Postgres, TargetDSN: pgDSN, Force: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.CurrentStatus()
		if status.State != nil && status.State.Phase == PhaseFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	status := manager.CurrentStatus()
	if status.State == nil || status.State.Phase != PhaseFailed {
		t.Fatalf("切换失败后状态应为 failed: %+v", status.State)
	}
	if source.Gate().Frozen() {
		t.Fatal("切换失败后应解冻源库")
	}
	if maintenance {
		t.Fatal("切换失败后应退出维护模式")
	}
}

func TestPreflightRejectsNonEmptyTarget(t *testing.T) {
	pgDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	if pgDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, _ := seedSQLite(t, sourcePath)
	defer source.Close()
	target, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: pgDSN, Migrate: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(`CREATE TABLE IF NOT EXISTS preflight_marker(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	runner := NewRunner(Options{
		Source:       source,
		SourceDriver: db.SQLite,
		SourcePath:   sourcePath,
		TargetDriver: db.Postgres,
		TargetDSN:    pgDSN,
		Force:        false,
		DataDir:      dir,
		Cipher:       testCipher(t),
	})
	if _, err := runner.Preflight(context.Background()); err == nil {
		t.Fatal("非空目标库应被拒绝")
	}
}
