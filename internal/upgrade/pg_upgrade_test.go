package upgrade

import (
	"context"
	"os"
	"strings"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func TestPostgresStatusAndOptimize(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	conn, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte(strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	dir := t.TempDir()
	manager, err := New(Options{
		DB:         conn,
		Store:      st,
		DataDir:    dir,
		DBPath:     "",
		AppVersion: "test",
		Auto:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Database.Tables == 0 || status.Database.JournalMode != "wal" {
		t.Fatalf("PostgreSQL 统计异常: %+v", status.Database)
	}
	result, err := manager.Optimize(context.Background(), OptimizeOptions{Analyze: true, Checkpoint: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Before.FileSize == 0 {
		t.Fatalf("优化前大小异常: %+v", result)
	}
}
