package lecdn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeployLeCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/prod-api/login":
			_, _ = w.Write([]byte(`{"code":200,"data":{"token":"token-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/prod-api/certificate":
			if r.Header.Get("Authorization") != "Bearer token-1" {
				t.Errorf("missing token")
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"items":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/prod-api/certificate":
			_, _ = w.Write([]byte(`{"code":200,"data":{"id":8}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/prod-api/site/3/domain_name":
			_, _ = w.Write([]byte(`{"code":200,"data":[{"id":9,"domain_name":"example.com"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/prod-api/site/3/domain_name/certificate":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["certificate_id"] != float64(8) || payload["domain_name_id"] != float64(9) {
				t.Errorf("unexpected deploy payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"url": server.URL, "username": "admin", "password": "secret", "site_id": "3"}
	if err := deployLeCDN(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
