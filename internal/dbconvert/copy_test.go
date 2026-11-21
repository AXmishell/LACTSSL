package dbconvert

import (
	"context"
	"path/filepath"
	"testing"

	"lactssl/internal/db"
)

// TestRowHashIgnoresColumnOrder 验证行哈希只取决于列名和值，与 SELECT * 的列顺序无关。
func TestRowHashIgnoresColumnOrder(t *testing.T) {
	first := []cell{
		{value: "session-id", valid: true},
		{value: int64(7), valid: true},
	}
	second := []cell{
		{value: int64(7), valid: true},
		{value: "session-id", valid: true},
	}
	var a, b hashAccumulator
	a.addRow(first, columnOrder([]string{"id", "user_id"}))
	b.addRow(second, columnOrder([]string{"user_id", "id"}))
	if a.String() != b.String() {
		t.Fatalf("列顺序不同导致哈希不一致: %s != %s", a.String(), b.String())
	}
}

// TestCopyTableAcceptsDifferentColumnOrder 复现旧库用 ALTER TABLE 追加列后
// 与目标库列顺序不一致的场景，复制后的校验和仍应一致。
func TestCopyTableAcceptsDifferentColumnOrder(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	targetPath := filepath.Join(dir, "target.db")

	source, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: targetPath})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	// 源库：strong_auth 是后加的列，物理顺序在最后。
	if _, err := source.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		strong_auth INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	// 目标库：新结构，strong_auth 在中间。
	if _, err := target.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		strong_auth INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(
		`INSERT INTO sessions(id, user_id, created_at, strong_auth) VALUES (?, ?, ?, ?)`,
		"session-1", 1, "2026-11-17 01:44:31.94651773 +0000 UTC", 0,
	); err != nil {
		t.Fatal(err)
	}

	spec := db.TableSpec{Name: "sessions", PrimaryKey: []string{"id"}}
	result, err := copyTable(context.Background(), source, target, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := scanTable(context.Background(), target, spec, result.columns)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Rows != result.Rows {
		t.Fatalf("行数不一致: 源 %d，目标 %d", result.Rows, actual.Rows)
	}
	if actual.Hash != result.Hash {
		t.Fatalf("列顺序不同导致校验失败: 源 %s，目标 %s", result.Hash, actual.Hash)
	}
}
