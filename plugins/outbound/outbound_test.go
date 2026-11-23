package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", true},
		{"100.100.100.200", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"::1", false},
		{"fd00:ec2::254", true},
		{"::ffff:169.254.169.254", true},
		{"::169.254.169.254", true},
		{"64:ff9b::a9fe:a9fe", true},
		// 普通公网/私网 IPv6 不能被末 32 位误判为 0.0.0.0/8。
		{"2606:4700:4700::1111", false},
		{"2001:4860:4860::8888", false},
		{"fd00::1", false},
		{"2606:4700:4700::6810:84e5", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", tc.ip)
		}
		if got := isBlockedIP(ip); got != tc.blocked {
			t.Errorf("isBlockedIP(%s)=%v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

func TestValidateServiceURL(t *testing.T) {
	if err := ValidateServiceURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback http should be allowed: %v", err)
	}
	if err := ValidateServiceURL("http://localhost:8080"); err != nil {
		t.Fatalf("localhost http should be allowed: %v", err)
	}
	if err := ValidateServiceURL("http://198.51.100.1:8080"); err == nil {
		t.Fatal("public http should require https")
	}
	if err := ValidateServiceURL("https://example.com/path"); err != nil {
		t.Fatalf("public https should be allowed: %v", err)
	}
	if err := ValidateServiceURL("http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("metadata http should be blocked")
	}
}

func TestValidateWebhookURL(t *testing.T) {
	if err := ValidateWebhookURL("https://example.com/hook"); err != nil {
		t.Fatalf("public https webhook should be allowed: %v", err)
	}
	if err := ValidateWebhookURL("http://example.com/hook"); err == nil {
		t.Fatal("public http webhook should be rejected")
	}
	if err := ValidateWebhookURL("http://127.0.0.1:9000/hook"); err != nil {
		t.Fatalf("loopback http webhook should be allowed: %v", err)
	}
}

func TestProxyTargetValidationRejectsBlockedTarget(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProxyTarget(req.URL); err == nil {
		t.Fatal("proxy target validation must reject metadata addresses")
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	var jumped atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jumped.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := NewClient(5*time.Second, false)
	resp, err := client.Get(source.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", resp.StatusCode)
	}
	if jumped.Load() != 0 {
		t.Fatal("redirect target must not be contacted")
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL("https://user:pass@example.com/path?token=secret#frag")
	if strings.Contains(got, "secret") || strings.Contains(got, "user") || strings.Contains(got, "pass") {
		t.Fatalf("redacted URL still contains secrets: %s", got)
	}
}

func TestDialSafeBlocksMetadata(t *testing.T) {
	if _, err := DialSafe(context.Background(), "tcp", "169.254.169.254:80"); err == nil {
		t.Fatal("metadata address must be blocked")
	}
}

func TestValidateURLRejectsEmptyHostAndRedactsSecrets(t *testing.T) {
	if err := ValidateURL("https://:8443/"); err == nil {
		t.Fatal("empty hostname should be rejected")
	}
	err := ValidateURL("https://user:pass@example.com/x?token=SECRET")
	if err == nil {
		t.Fatal("userinfo URL should be rejected")
	}
	for _, secret := range []string{"pass", "SECRET", "user"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error leaked %q: %s", secret, err)
		}
	}
}

func TestValidateServiceURLRequiresHTTPSForResolvedPublicHost(t *testing.T) {
	old := lookupIPAddr
	defer func() { lookupIPAddr = old }()
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.5")}}, nil
	}
	if err := ValidateServiceURL("http://svc.internal"); err == nil {
		t.Fatal("public-resolving .internal host must require HTTPS")
	}
	if err := ValidateWebhookURL("http://svc.internal/hook"); err == nil {
		t.Fatal("public-resolving webhook host must require HTTPS")
	}
}

func TestValidateWebhookURLRejectsPublicIPv6HTTP(t *testing.T) {
	if err := ValidateWebhookURL("http://[2606:4700:4700::6810:84e5]/hook"); err == nil {
		t.Fatal("public IPv6 literal must require HTTPS")
	}
	if err := ValidateWebhookURL("http://[fd00::1]/hook"); err != nil {
		t.Fatalf("private IPv6 HTTP should be allowed: %v", err)
	}
	if err := ValidateWebhookURL("http://[::1]/hook"); err != nil {
		t.Fatalf("loopback IPv6 HTTP should be allowed: %v", err)
	}
}

func TestValidateServiceURLLocalHTTPStillAllowed(t *testing.T) {
	old := lookupIPAddr
	defer func() { lookupIPAddr = old }()
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	}
	for _, raw := range []string{"http://svc.internal", "http://intranet", "http://[fd00::1]"} {
		if err := ValidateServiceURL(raw); err != nil {
			t.Fatalf("%s should be allowed: %v", raw, err)
		}
	}
}

func TestValidateServiceURLRedactsToken(t *testing.T) {
	err := ValidateServiceURL("http://example.com/path?token=SECRET")
	if err == nil {
		t.Fatal("public HTTP should be rejected")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaked token: %s", err)
	}
}
