package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesTasksPipelineColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL DEFAULT '', next_run_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("打开旧数据库失败: %v", err)
	}
	defer conn.Close()

	rows, err := conn.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "pipeline" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("旧数据库未补充 pipeline 字段")
	}
}

func TestOpenCreatesACMETables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, table := range []string{
		"acme_endpoints",
		"acme_accounts",
		"acme_eab_credentials",
		"acme_orders",
		"acme_authorizations",
		"acme_challenges",
	} {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
}
