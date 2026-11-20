package dbconvert

import (
	"os"
	"path/filepath"
	"testing"

	"lactssl/internal/db"
)

func writeSQLiteLike(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, append([]byte(sqliteFileMagic), make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupSourceRemovesSQLiteAndSidecars(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, "lactssl.db")
	writeSQLiteLike(t, dbPath)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.WriteFile(dbPath+suffix, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := &State{
		FromDriver: db.SQLite.String(),
		ToDriver:   db.Postgres.String(),
		FromPath:   dbPath,
	}
	if err := CleanupSource(dataDir, state); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("%s 应已删除，err=%v", dbPath+suffix, err)
		}
	}
}

func TestCleanupSourceRejectsPathOutsideDataDir(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.db")
	writeSQLiteLike(t, outside)
	state := &State{
		FromDriver: db.SQLite.String(),
		ToDriver:   db.Postgres.String(),
		FromPath:   outside,
	}
	if err := CleanupSource(dataDir, state); err == nil {
		t.Fatal("数据目录外的路径应被拒绝")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("数据目录外的文件不应被删除: %v", err)
	}
}

func TestCleanupSourceRejectsNonSQLiteFile(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "important.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &State{
		FromDriver: db.SQLite.String(),
		ToDriver:   db.Postgres.String(),
		FromPath:   dbPath,
	}
	if err := CleanupSource(dataDir, state); err == nil {
		t.Fatal("非 SQLite 文件应被拒绝")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("非 SQLite 文件不应被删除: %v", err)
	}
}

func TestCleanupSourceSkipsNonSQLiteSource(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "lactssl.db")
	writeSQLiteLike(t, dbPath)
	state := &State{
		FromDriver: db.Postgres.String(),
		ToDriver:   db.SQLite.String(),
		FromPath:   dbPath,
	}
	if err := CleanupSource(dataDir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("非 SQLite 源不应删除文件: %v", err)
	}
}

func TestCleanupSourceSQLiteToSQLite(t *testing.T) {
	dataDir := t.TempDir()
	source := filepath.Join(dataDir, "source.db")
	target := filepath.Join(dataDir, "target.db")
	writeSQLiteLike(t, source)
	writeSQLiteLike(t, target)
	state := &State{
		FromDriver: db.SQLite.String(),
		ToDriver:   db.SQLite.String(),
		FromPath:   source,
		ToPath:     target,
	}
	if err := CleanupSource(dataDir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("旧 SQLite 文件应已删除: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("目标 SQLite 文件不应被删除: %v", err)
	}
}

func TestCleanupSourceRefusesSameSQLiteTarget(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "lactssl.db")
	writeSQLiteLike(t, path)
	state := &State{
		FromDriver: db.SQLite.String(),
		ToDriver:   db.SQLite.String(),
		FromPath:   path,
		ToPath:     path,
	}
	if err := CleanupSource(dataDir, state); err == nil {
		t.Fatal("源与目标为同一文件时应拒绝删除")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件不应被删除: %v", err)
	}
}

func TestCleanupSourceKeepsPostgresSource(t *testing.T) {
	dataDir := t.TempDir()
	state := &State{
		FromDriver: db.Postgres.String(),
		ToDriver:   db.Postgres.String(),
	}
	if err := CleanupSource(dataDir, state); err != nil {
		t.Fatal(err)
	}
}
