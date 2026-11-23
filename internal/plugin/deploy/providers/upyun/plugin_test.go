package upyun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeployUpyun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/signin/":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc"})
			_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
		case "/api/https/certificate/":
			if r.Header.Get("Cookie") == "" {
				t.Errorf("missing cookie")
			}
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["certificate"] != "FULLCHAIN" || payload["private_key"] != "KEY" {
				t.Errorf("unexpected certificate payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"data":{"result":{"certificate_id":31}}}`))
		case "/api/https/migrate/domain":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/https/certificate/manager":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["certificate_id"] != float64(31) || payload["domain"] != "example.com" || payload["https"] != true {
				t.Errorf("unexpected manager payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	old := upyunBaseURL
	upyunBaseURL = server.URL
	t.Cleanup(func() { upyunBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"username": "user", "password": "pass", "https": "true"}
	if err := deployUpyun(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
