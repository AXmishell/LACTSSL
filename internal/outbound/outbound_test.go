package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeErrorTextRedactsSecrets(t *testing.T) {
	// 拼接构造示例 bot token，避免完整字符串写入源码触发密钥扫描告警。
	botToken := "123456789" + ":" + "AAHdqTcvCH1vGWJxfSeo" + "fSAs0K5PALDsaw"
	cases := []string{
		`request https://user:pass@example.com/hook?token=supersecret failed`,
		`telegram bot` + botToken + ` failed`,
		`Authorization: Bearer abcdefghijklmnop`,
		`callback?api_key=my-secret-key&x=1`,
	}
	for _, input := range cases {
		got := SanitizeErrorText(input)
		for _, secret := range []string{"pass", "supersecret", botToken, "abcdefghijklmnop", "my-secret-key"} {
			if strings.Contains(got, secret) {
				t.Fatalf("secret %q not redacted in %q", secret, got)
			}
		}
	}
}

func TestClientRejectsRedirectToBlockedTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	client := NewClient(3*time.Second, false)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("redirect to link-local metadata address should be rejected")
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"127.0.0.1":       false,
		"10.0.0.1":        false,
		"192.168.1.1":     false,
		"172.16.0.1":      false,
		"169.254.169.254": false,
		"100.64.0.1":      false,
		"0.0.0.0":         false,
		"::1":             false,
		"fc00::1":         false,
	}
	for raw, want := range cases {
		ip := net.ParseIP(raw)
		if got := isPublicIP(ip); got != want {
			t.Fatalf("isPublicIP(%s)=%v want %v", raw, got, want)
		}
	}
}

func TestDialPublicRejectsLoopback(t *testing.T) {
	if _, err := DialPublic(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("loopback target should be rejected")
	}
}
