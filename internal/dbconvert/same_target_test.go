package dbconvert

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lactssl/internal/db"
)

func TestSameSQLiteFilePathDetection(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := os.WriteFile(source, []byte(sqliteFileMagic), 0o600); err != nil {
		t.Fatal(err)
	}

	same, err := sameSQLiteFile(source, source)
	if err != nil || !same {
		t.Fatalf("同一路径应判定为同一个文件: same=%v err=%v", same, err)
	}

	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, []byte(sqliteFileMagic), 0o600); err != nil {
		t.Fatal(err)
	}
	same, err = sameSQLiteFile(source, target)
	if err != nil || same {
		t.Fatalf("不同文件不应判定为同一个文件: same=%v err=%v", same, err)
	}

	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
	same, err = sameSQLiteFile(source, link)
	if err != nil || !same {
		t.Fatalf("符号链接应判定为同一个文件: same=%v err=%v", same, err)
	}

	hard := filepath.Join(dir, "hard.db")
	if err := os.Link(source, hard); err != nil {
		t.Skipf("当前环境不支持硬链接: %v", err)
	}
	same, err = sameSQLiteFile(source, hard)
	if err != nil || !same {
		t.Fatalf("硬链接应判定为同一个文件: same=%v err=%v", same, err)
	}
}

func TestValidateTargetSameDriver(t *testing.T) {
	t.Setenv("LACTSSL_DATABASE_DSN", "")
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := os.WriteFile(source, []byte(sqliteFileMagic), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{SourceDriver: db.SQLite, SourcePath: source}
	target := filepath.Join(dir, "target.db")

	req := StartRequest{TargetDriver: db.SQLite, TargetPath: target}
	if err := manager.validateTarget(req); err == nil {
		t.Fatal("未显式允许时同构迁移应被拒绝")
	}

	req.AllowSameDriver = true
	if err := manager.validateTarget(req); err != nil {
		t.Fatalf("显式允许且目标不同时应通过: %v", err)
	}

	req.Force = true
	if err := manager.validateTarget(req); err == nil {
		t.Fatal("同构迁移启用 force 应被拒绝")
	}
	req.Force = false

	req.TargetPath = source
	if err := manager.validateTarget(req); err == nil {
		t.Fatal("目标与源为同一 SQLite 文件时应被拒绝")
	}
}

func TestEnsureDistinctPostgresTarget(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	source, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := ensureDistinctTarget(context.Background(), source, target, db.Postgres, db.Postgres, "", ""); err == nil {
		t.Fatal("同一个 PostgreSQL 数据库作为目标时应被拒绝")
	}
}
