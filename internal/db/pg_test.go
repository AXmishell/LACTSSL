package db

import (
	"os"
	"testing"
)

func TestRebindDollar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM t WHERE a=? AND b=?", "SELECT * FROM t WHERE a=$1 AND b=$2"},
		{"SELECT '?' , \"?\", a FROM t WHERE b=?", "SELECT '?' , \"?\", a FROM t WHERE b=$1"},
		{"SELECT $$?$$, $tag$?$tag$, ?", "SELECT $$?$$, $tag$?$tag$, $1"},
		{"-- ?\nSELECT ?", "-- ?\nSELECT $1"},
	}
	for _, c := range cases {
		if got := Postgres.Rebind(c.in); got != c.want {
			t.Fatalf("rebind %q => %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPostgresSchema(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	conn, err := OpenWithOptions(Options{Driver: Postgres, DSN: dsn, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	version, err := conn.SchemaVersion()
	if err != nil || version != schemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, spec := range DataTables() {
		var exists bool
		if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=?)`, spec.Name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("缺少表 %s", spec.Name)
		}
	}
}

func TestPostgresSingleInstanceLock(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	first, err := OpenWithOptions(Options{Driver: Postgres, DSN: dsn, Migrate: true, SingleInstance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := OpenWithOptions(Options{Driver: Postgres, DSN: dsn, Migrate: true, SingleInstance: true}); err == nil {
		t.Fatal("第二个实例应被 advisory lock 拒绝")
	}
}
