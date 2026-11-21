package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteDatabasePreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "# keep me\n" +
		"data_dir: " + dir + "\n" +
		"database:\n" +
		"  driver: sqlite\n" +
		"  path: " + filepath.Join(dir, "old.db") + "\n" +
		"security:\n  salt: \"" + strings.Repeat("a", 64) + "\"\n  jwt_secret: \"" + strings.Repeat("b", 64) + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := RewriteDatabase(path, "postgres", "postgres://user:pass@host/db?sslmode=disable", "")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("缺少旧配置备份")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# keep me") || !strings.Contains(text, "driver: postgres") ||
		!strings.Contains(text, "postgres://user:pass@host/db") {
		t.Fatalf("配置改写结果异常:\n%s", text)
	}
	settings, err := DatabaseFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Driver != "postgres" || settings.DSN == "" {
		t.Fatalf("解析结果异常: %+v", settings)
	}
}

func TestRewriteDatabaseMySQL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "data_dir: " + dir + "\n" +
		"database:\n" +
		"  driver: sqlite\n" +
		"  path: " + filepath.Join(dir, "old.db") + "\n" +
		"security:\n  salt: \"" + strings.Repeat("a", 64) + "\"\n  jwt_secret: \"" + strings.Repeat("b", 64) + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := "mysql://user:pass@127.0.0.1:3306/lactssl?tls=preferred"
	if _, err := RewriteDatabase(path, "mysql", dsn, ""); err != nil {
		t.Fatal(err)
	}
	settings, err := DatabaseFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Driver != "mysql" || settings.DSN != dsn {
		t.Fatalf("MySQL 配置解析异常: %+v", settings)
	}
}

func TestRewriteDatabaseRejectsInvalidCombination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("security:\n  salt: \""+strings.Repeat("a", 64)+"\"\n  jwt_secret: \""+strings.Repeat("b", 64)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RewriteDatabase(path, "postgres", "", ""); err == nil {
		t.Fatal("postgres 缺少 DSN 应被拒绝")
	}
}
