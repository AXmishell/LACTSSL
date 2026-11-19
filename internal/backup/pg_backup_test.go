package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func TestPostgresRestore(t *testing.T) {
	sourceDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	targetDSN := os.Getenv("LACTSSL_TEST_PG_RESTORE_DSN")
	if sourceDSN == "" || targetDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN / LACTSSL_TEST_PG_RESTORE_DSN 未设置")
	}
	source, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: sourceDSN, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	cipher, err := util.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(source, cipher)
	if _, err := st.CreateUser("restore@example.com", "hash"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	manager := New(source, dataDir, "", "", "test")
	archive, _, cleanup, err := manager.Export("restore-password")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stage(file, filepath.Base(archive), "restore-password"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	pending, err := LoadPending(dataDir)
	if err != nil || pending == nil {
		t.Fatalf("pending: %v %+v", err, pending)
	}
	if err := ApplyPostgresDatabase(targetDSN, pending.DatabasePath); err != nil {
		t.Fatal(err)
	}
	target, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: targetDSN, Migrate: false})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var count int64
	if err := target.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("恢复后用户表为空")
	}
}

func TestPostgresExportAndStage(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	if err := st.SetSetting("backup.pg", "keep-me"); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("security:\n  salt: 0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(conn, dataDir, "", configPath, "test")
	info, err := manager.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.DatabaseSize <= 0 {
		t.Fatal("PostgreSQL 数据库大小应大于 0")
	}
	archive, filename, cleanup, err := manager.Export("backup-password")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasSuffix(filename, ".zip") {
		t.Fatalf("文件名异常: %s", filename)
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := manager.Stage(file, filepath.Base(archive), "backup-password"); err != nil {
		t.Fatalf("Stage 失败: %v", err)
	}
	pending, err := LoadPending(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.Driver != "postgres" {
		t.Fatalf("待恢复状态异常: %+v", pending)
	}
	if err := manager.CancelPending(); err != nil {
		t.Fatal(err)
	}
}
