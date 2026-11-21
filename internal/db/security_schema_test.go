package db

import (
	"path/filepath"
	"testing"
)

// TestSecurityEventsSMTPColumnMigration 验证从 schemaVersion 19 升级到 20 时
// 能为已有 security_events 表补上 smtp_snapshot 列。
func TestSecurityEventsSMTPColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security-migrate.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`ALTER TABLE security_events DROP COLUMN smtp_snapshot`); err != nil {
		_ = conn.Close()
		t.Skipf("当前 SQLite 不支持 DROP COLUMN，跳过迁移演练: %v", err)
	}
	if err := conn.SetSchemaVersion(19); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exists, err := hasColumn(conn, "security_events", "smtp_snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("升级后缺少 smtp_snapshot 列")
	}
	version, err := conn.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version < 20 {
		t.Fatalf("升级后结构版本不正确: %d", version)
	}
}
