package npm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployNPMWithTOTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/tokens":
			_, _ = w.Write([]byte(`{"requires_2fa":true,"challenge_token":"challenge"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/tokens/2fa":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["challenge_token"] != "challenge" || payload["code"] == "" {
				t.Errorf("unexpected 2fa payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"token":"access-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/nginx/certificates":
			if r.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("missing bearer token")
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/nginx/certificates":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["provider"] != "other" || payload["nice_name"] != "lactssl_example-com" {
				t.Errorf("unexpected create payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"id":5,"provider":"other","nice_name":"lactssl_example-com"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/nginx/certificates/5/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			file, _, err := r.FormFile("certificate")
			if err != nil {
				t.Errorf("certificate file: %v", err)
			} else {
				data, _ := io.ReadAll(file)
				if string(data) != "FULLCHAIN" {
					t.Errorf("certificate content = %q", data)
				}
			}
			keyFile, _, err := r.FormFile("certificate_key")
			if err != nil {
				t.Errorf("key file: %v", err)
			} else {
				data, _ := io.ReadAll(keyFile)
				if string(data) != "KEY" {
					t.Errorf("key content = %q", data)
				}
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/nginx/proxy-hosts":
			_, _ = w.Write([]byte(`[{"id":7,"domain_names":["example.com"],"certificate_id":0},{"id":8,"domain_names":["other.com"]}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/nginx/proxy-hosts/7":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["certificate_id"] != float64(5) {
				t.Errorf("unexpected assign payload: %v", payload)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"panel_url":   server.URL,
		"email":       "admin@example.com",
		"password":    "secret",
		"totp_secret": "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
	}
	if err := deployNPM(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveHostsByDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nginx/proxy-hosts" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":7,"domain_names":["example.com","www.example.com"]},{"id":8,"domain_names":["other.com"]}]`))
	}))
	defer server.Close()
	c := &npmClient{base: server.URL, http: server.Client(), token: "t"}
	hosts, err := c.resolveHosts("example.com", &deploy.Certificate{Domain: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].ID != 7 {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestValidation(t *testing.T) {
	if err := deployNPM(&deploy.Certificate{}, map[string]string{"panel_url": "not-a-url", "email": "a", "password": "b"}); err == nil {
		t.Fatal("expected URL error")
	}
	if err := deployNPM(&deploy.Certificate{}, map[string]string{"panel_url": "http://127.0.0.1:1"}); err == nil || !strings.Contains(err.Error(), "邮箱") {
		t.Fatalf("unexpected error: %v", err)
	}
}
