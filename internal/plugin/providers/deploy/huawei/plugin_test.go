package huawei

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployHuaweiCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/cdn/domains/example.com/configs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("enterprise_project_id") != "all" {
			t.Errorf("missing project query: %s", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=ak") {
			t.Errorf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		configs, _ := payload["configs"].(map[string]any)
		https, _ := configs["https"].(map[string]any)
		if https["certificate_value"] != "FULLCHAIN" || https["private_key"] != "KEY" || https["https_status"] != "on" {
			t.Errorf("unexpected payload: %v", payload)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	old := huaweiCDNBaseURL
	huaweiCDNBaseURL = server.URL
	t.Cleanup(func() { huaweiCDNBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"access_key_id": "ak", "secret_access_key": "sk", "enterprise_project_id": "all",
	}
	if err := deployHuaweiCDN(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
