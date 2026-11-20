package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/ratelimit"
)

func writeRateLimitConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `data_dir: "` + filepath.Join(dir, "data") + `"
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
` + extra
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRateLimitDefaults(t *testing.T) {
	path := writeRateLimitConfig(t, "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.MaxKeys != ratelimit.DefaultMaxKeys {
		t.Fatalf("默认 max_keys 不正确: %d", cfg.RateLimit.MaxKeys)
	}
	if got := cfg.RateLimit.For(ratelimit.LoginIP); got.Max != 10 || got.Window != 5*time.Minute {
		t.Fatalf("登录 IP 默认规则不正确: %+v", got)
	}
	if got := cfg.RateLimit.For(ratelimit.ACMERequest); got.Max != 1200 || got.Window != time.Minute {
		t.Fatalf("ACME 请求默认规则不正确: %+v", got)
	}
}

func TestRateLimitOverrides(t *testing.T) {
	path := writeRateLimitConfig(t, `  rate_limit:
    max_keys: 500
    rules:
      login_ip: {max: 3, window_seconds: 60}
      login_account: {max: 7}
      api_ip: {max: 0}
`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.MaxKeys != 500 {
		t.Fatalf("max_keys 覆盖未生效: %d", cfg.RateLimit.MaxKeys)
	}
	if got := cfg.RateLimit.For(ratelimit.LoginIP); got.Max != 3 || got.Window != time.Minute {
		t.Fatalf("login_ip 覆盖未生效: %+v", got)
	}
	// 只覆盖 max 时，窗口沿用内置默认值。
	if got := cfg.RateLimit.For(ratelimit.LoginAccount); got.Max != 7 || got.Window != 15*time.Minute {
		t.Fatalf("login_account 局部覆盖未生效: %+v", got)
	}
	// max=0 表示不限制。
	if got := cfg.RateLimit.For(ratelimit.APIIP); got.Max != 0 {
		t.Fatalf("api_ip 应被显式关闭: %+v", got)
	}
}

func TestRateLimitRejectsUnknownRule(t *testing.T) {
	path := writeRateLimitConfig(t, `  rate_limit:
    rules:
      not_a_rule: {max: 1, window_seconds: 60}
`)
	if _, err := LoadFrom(path); err == nil || !strings.Contains(err.Error(), "not_a_rule") {
		t.Fatalf("未知规则应导致启动失败，实际: %v", err)
	}
}

func TestRateLimitRejectsInvalidValues(t *testing.T) {
	cases := []string{
		`  rate_limit:
    max_keys: -1
`,
		`  rate_limit:
    rules:
      login_ip: {max: -1}
`,
		`  rate_limit:
    rules:
      login_ip: {max: 10, window_seconds: 0}
`,
	}
	for _, extra := range cases {
		path := writeRateLimitConfig(t, extra)
		if _, err := LoadFrom(path); err == nil {
			t.Fatalf("非法限流配置应被拒绝:\n%s", extra)
		}
	}
}

func TestRateLimitTemplateIncludesRules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(DefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rate_limit:", "max_keys:", "rules:", "login_ip:", "acme_request:"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("生成的 config.yaml 缺少 %q:\n%s", key, data)
		}
	}
}
