package cdnfly

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployCDNfly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "key" || r.Header.Get("api-secret") != "secret" {
			t.Errorf("missing api credentials")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sites":
			_, _ = w.Write([]byte(`{"code":0,"data":[{"id":3,"domain":"example.com www.example.com"},{"id":4,"domain":"new.com"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sites/3":
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":3,"domain":"example.com","https_listen":{"cert":8}}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/certs/8":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["cert"] != "FULLCHAIN" || payload["key"] != "KEY" {
				t.Errorf("unexpected cert payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sites/4":
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":4,"domain":"new.com"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/certs":
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":12}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sites":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			listen, _ := payload["https_listen"].(map[string]any)
			if payload["id"] != float64(4) || listen["cert"] != float64(12) {
				t.Errorf("unexpected site payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":0}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"url": server.URL, "api_key": "key", "api_secret": "secret",
	}
	if err := deployCDNfly(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeployCDNflyCertIDsOnlySkipsSiteResolution(t *testing.T) {
	sitesCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sites" {
			sitesCalled = true
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/v1/certs/8" {
			_, _ = w.Write([]byte(`{"code":0}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		http.NotFound(w, r)
	}))
	defer server.Close()

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"url": server.URL, "api_key": "key", "api_secret": "secret", "cert_ids": "8",
	}
	if err := deployCDNfly(cert, cfg); err != nil {
		t.Fatal(err)
	}
	if sitesCalled {
		t.Fatal("cert_ids-only deployment must not resolve/update sites")
	}
}

func TestParseCertID(t *testing.T) {
	if id := parseCertID([]byte(`{"cert":8}`)); id != 8 {
		t.Fatalf("id = %d", id)
	}
	if id := parseCertID([]byte(`"{\"cert\":9}"`)); id != 9 {
		t.Fatalf("string id = %d", id)
	}
}
