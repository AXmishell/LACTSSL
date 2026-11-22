package db

import (
	"strings"
	"testing"
)

func TestPostgresCLIEnvURL(t *testing.T) {
	safe, env := PostgresCLIEnv("postgres://user:p%40ss@db.example:5432/lactssl?sslmode=verify-full")
	if strings.Contains(safe, "p%40ss") || strings.Contains(safe, "p@ss") {
		t.Fatalf("DSN 仍包含密码: %q", safe)
	}
	if !strings.Contains(safe, "postgres://user@db.example:5432/lactssl") || !strings.Contains(safe, "sslmode=verify-full") {
		t.Fatalf("DSN 结构被破坏: %q", safe)
	}
	if len(env) != 1 || env[0] != "PGPASSWORD=p@ss" {
		t.Fatalf("unexpected env: %#v", env)
	}
}

func TestPostgresCLIEnvKeyword(t *testing.T) {
	cases := []struct {
		dsn      string
		wantSafe string
		wantPass string
	}{
		{"host=db user=u password='p ass' dbname=lactssl", "host=db user=u dbname=lactssl", "p ass"},
		{"host=db password=secret user=u", "host=db user=u", "secret"},
		{"host=db user=u", "host=db user=u", ""},
	}
	for _, tc := range cases {
		safe, env := PostgresCLIEnv(tc.dsn)
		if tc.wantPass != "" && strings.EqualFold(safe, tc.dsn) {
			t.Fatalf("密码未从 DSN 移除: %q", safe)
		}
		if tc.wantPass != "" && strings.Join(strings.Fields(safe), " ") != tc.wantSafe {
			t.Fatalf("DSN 结构被破坏: %q, want %q", safe, tc.wantSafe)
		}
		if tc.wantPass == "" {
			if len(env) != 0 {
				t.Fatalf("无密码时不应设置 PGPASSWORD: %#v", env)
			}
			continue
		}
		if len(env) != 1 || env[0] != "PGPASSWORD="+tc.wantPass {
			t.Fatalf("unexpected env for %q: %#v", tc.dsn, env)
		}
	}
}
