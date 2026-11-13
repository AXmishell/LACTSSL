package service

import (
	"errors"
	"net/url"
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
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.200/latest/meta-data/",
		"http://[fd00:ec2::254]/latest/meta-data/",
		"http://0.0.0.0/",
		"http://224.0.0.1/",
	}
	for _, value := range invalid {
		if err := ValidateOutboundURL(value); err == nil {
			t.Fatalf("非法地址应被拒绝: %s", value)
		}
	}
}

func TestValidateWebhookURLRequiresHTTPSForPublicHost(t *testing.T) {
	valid := []string{
		"https://example.com/hook",
		"http://127.0.0.1:9000/hook",
		"http://10.0.0.5/hook",
		"http://localhost:9000/hook",
		"http://nas:9000/hook",
	}
	for _, value := range valid {
		if err := ValidateWebhookURL(value); err != nil {
			t.Fatalf("合法 webhook 被拒绝: %s: %v", value, err)
		}
	}
	if err := ValidateWebhookURL("http://example.com/hook"); err == nil {
		t.Fatal("公网 http webhook 应被拒绝")
	}
	if err := ValidateWebhookURL("http://169.254.169.254/hook"); err == nil {
		t.Fatal("元数据地址应被拒绝")
	}
}

func TestSanitizeErrorTextRedactsURLSecrets(t *testing.T) {
	inputs := []string{
		`Post "https://api.telegram.org/botTOKEN/sendMessage": dial tcp: timeout`,
		`Post "https://gotify.example.com/message?token=SECRET": connection refused`,
		`Get "https://sctapi.ftqq.com/SENDKEY.send": no such host`,
	}
	for _, input := range inputs {
		output := SanitizeErrorText(input)
		for _, secret := range []string{"TOKEN", "SECRET", "SENDKEY"} {
			if strings.Contains(output, secret) {
				t.Fatalf("错误文本未脱敏: %q -> %q", input, output)
			}
		}
	}

	urlErr := &url.Error{Op: "Post", URL: "https://api.telegram.org/botTOKEN/sendMessage", Err: errors.New("boom")}
	if output := SafeError(urlErr); strings.Contains(output, "TOKEN") {
		t.Fatalf("url.Error 未脱敏: %s", output)
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
