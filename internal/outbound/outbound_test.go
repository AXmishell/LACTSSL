package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeErrorTextRedactsSecrets(t *testing.T) {
	cases := []string{
		`request https://user:pass@example.com/hook?token=supersecret failed`,
		`telegram bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw failed`,
		`Authorization: Bearer abcdefghijklmnop`,
		`callback?api_key=my-secret-key&x=1`,
	}
	for _, input := range cases {
		got := SanitizeErrorText(input)
		for _, secret := range []string{"pass", "supersecret", "AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", "abcdefghijklmnop", "my-secret-key"} {
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
