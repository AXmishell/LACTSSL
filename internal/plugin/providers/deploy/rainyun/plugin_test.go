package rainyun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployRainyun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/product/sslcenter/cert-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("missing api key")
		}
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["cert"] != "FULLCHAIN" || payload["key"] != "KEY" {
			t.Errorf("unexpected payload: %v", payload)
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"ok"}`))
	}))
	defer server.Close()
	old := rainyunBaseURL
	rainyunBaseURL = server.URL
	t.Cleanup(func() { rainyunBaseURL = old })

	cert := &deploy.NativeCertificate{FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployRainyun(cert, map[string]string{"api_key": "key", "cert_id": "cert-1"}); err != nil {
		t.Fatal(err)
	}
}
