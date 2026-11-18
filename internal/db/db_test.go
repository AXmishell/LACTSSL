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

func TestOpenCreatesCSRRequestTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "csr.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var name string
	if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='certificate_requests'`).Scan(&name); err != nil {
		t.Fatalf("缺少 certificate_requests 表: %v", err)
	}
	rows, err := conn.Query(`PRAGMA table_info(certificates)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			cid        int
			column     string
			colType    string
			notNull    int
			defaultVal any
			primaryKey int
		)
		if err := rows.Scan(&cid, &column, &colType, &notNull, &defaultVal, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if column == "csr_request_id" {
			found = true
		}
	}
	if !found {
		t.Fatal("certificates 表缺少 csr_request_id 字段")
	}
}

func TestOpenMigratesLegacyCertificatesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-certificates.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE certificates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL DEFAULT 0,
			not_after DATETIME
		)`,
		`PRAGMA user_version = 5`,
	}
	for _, stmt := range stmts {
		if _, err := legacy.Exec(stmt); err != nil {
			_ = legacy.Close()
			t.Fatalf("准备旧证书表失败: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("打开旧数据库失败: %v", err)
	}
	defer conn.Close()
	var column string
	if err := conn.QueryRow(`SELECT name FROM pragma_table_info('certificates') WHERE name='csr_request_id'`).Scan(&column); err != nil {
		t.Fatalf("旧证书表未补充 csr_request_id: %v", err)
	}
}

func TestOpenMigratesLegacyLogsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-logs.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			ref_id INTEGER NOT NULL DEFAULT 0,
			level TEXT NOT NULL DEFAULT 'info',
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE system_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			component TEXT NOT NULL DEFAULT 'app',
			message TEXT NOT NULL,
			fields TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`INSERT INTO logs(source,ref_id,level,message,created_at) VALUES('task',7,'success','ok',CURRENT_TIMESTAMP)`,
		`INSERT INTO system_logs(level,component,message,fields,created_at) VALUES('warn','service','slow','{"task_id":7}',CURRENT_TIMESTAMP)`,
		`PRAGMA user_version = 4`,
	}
	for _, stmt := range stmts {
		if _, err := legacy.Exec(stmt); err != nil {
			_ = legacy.Close()
			t.Fatalf("准备旧库失败: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("打开旧库失败: %v", err)
	}
	defer conn.Close()

	for _, column := range []string{"category", "ref_type", "fields", "request_id"} {
		var found bool
		rows, err := conn.Query(`PRAGMA table_info(logs)`)
		if err != nil {
			t.Fatal(err)
		}
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
				rows.Close()
				t.Fatal(err)
			}
			if name == column {
				found = true
			}
		}
		rows.Close()
		if !found {
			t.Fatalf("旧库未补充 logs.%s", column)
		}
	}

	var category string
	if err := conn.QueryRow(`SELECT category FROM logs WHERE source='task'`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if category != "operation" {
		t.Fatalf("旧业务日志分类不正确: %q", category)
	}

	for _, index := range []string{"idx_logs_category_created", "idx_logs_category_source_created", "idx_logs_category_level_created", "idx_logs_category_id", "idx_logs_ref"} {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("缺少索引 %s: %v", index, err)
		}
	}
}
