package service

import (
	"strings"
	"testing"
)

func TestValidateOutboundURL(t *testing.T) {
	valid := []string{
		"https://example.com",
		"http://127.0.0.1:8080/api",
		"https://panel.example.com:8443/path?x=1",
	}
	for _, value := range valid {
		if err := ValidateOutboundURL(value); err != nil {
			t.Fatalf("合法地址被拒绝: %s: %v", value, err)
		}
	}

	invalid := []string{
		"",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ftp://example.com",
		"http://",
		"http://user:pass@example.com",
		"https://example.com:99999",
		"//example.com",
	}
	for _, value := range invalid {
		if err := ValidateOutboundURL(value); err == nil {
			t.Fatalf("非法地址应被拒绝: %s", value)
		}
	}
}

func TestCommandEnvFiltersSecrets(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "super-secret-token")
	t.Setenv("ALICLOUD_SECRET_KEY", "super-secret-key")
	t.Setenv("LACTSSL_ENTRY", "secret-entry")
	t.Setenv("DEPLOY_TEST_KEEP", "keep-me")

	env := strings.Join(commandEnv(), "\n")
	for _, secret := range []string{"CLOUDFLARE_DNS_API_TOKEN", "ALICLOUD_SECRET_KEY", "LACTSSL_ENTRY"} {
		if strings.Contains(env, secret) {
			t.Fatalf("子进程环境不应包含 %s", secret)
		}
	}
	// 非敏感变量也默认不继承，保持最小环境。
	if strings.Contains(env, "DEPLOY_TEST_KEEP") {
		t.Fatal("子进程环境不应继承任意自定义变量")
	}
	if !strings.Contains(env, "PATH=") {
		t.Fatal("子进程环境应保留 PATH")
	}
}
