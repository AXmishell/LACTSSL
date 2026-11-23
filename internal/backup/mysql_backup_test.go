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

func TestMySQLExportStageRestore(t *testing.T) {
	sourceDSN := os.Getenv("LACTSSL_TEST_MYSQL_DSN")
	targetDSN := os.Getenv("LACTSSL_TEST_MYSQL_RESTORE_DSN")
	if sourceDSN == "" || targetDSN == "" {
		t.Skip("LACTSSL_TEST_MYSQL_DSN / LACTSSL_TEST_MYSQL_RESTORE_DSN 未设置")
	}
	source, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: sourceDSN, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	cipher, err := util.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(source, cipher)
	if _, err := st.CreateUser("mysql-restore@example.com", "hash"); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate") {
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
	if pending.Driver != "mysql" {
		t.Fatalf("driver = %q, want mysql", pending.Driver)
	}
	if err := ApplyMySQLDatabase(targetDSN, pending.DatabasePath); err != nil {
		t.Fatal(err)
	}
	target, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: targetDSN, Migrate: false})
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
