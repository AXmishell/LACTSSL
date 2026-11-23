package lucky

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployLucky(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("openToken") != "token" {
			t.Errorf("missing open token")
		}
		if r.URL.Path != "/safe/api/ssl" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"ret":0,"list":[{"Key":"cert-1","Remark":"demo","CertsInfo":{"Domains":["example.com"]}}]}`))
		case http.MethodPut:
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			cert, _ := base64.StdEncoding.DecodeString(payload["CertBase64"].(string))
			key, _ := base64.StdEncoding.DecodeString(payload["KeyBase64"].(string))
			if string(cert) != "FULLCHAIN" || string(key) != "KEY" {
				t.Errorf("unexpected certificate payload")
			}
			if payload["Key"] != "cert-1" || payload["Remark"] != "demo" {
				t.Errorf("unexpected payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"url": server.URL, "safe_path": "safe", "open_token": "token"}
	if err := deployLucky(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDomainsOf(t *testing.T) {
	item := certificate{CertsInfo: []byte(`[{"Domains":["a.com"]},{"Domains":["b.com"]}]`)}
	got := domainsOf(item)
	if len(got) != 2 || got[0] != "a.com" || got[1] != "b.com" {
		t.Fatalf("domains = %v", got)
	}
}
