package panels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeployBaotaDockerSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mod/docker/com/set_ssl" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		if r.Form.Get("siteName") != "docker-site" || r.Form.Get("key") != "PRIVATE-KEY" || r.Form.Get("csr") != "FULLCHAIN" {
			t.Errorf("unexpected form: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"status":true}`))
	}))
	defer server.Close()
	cert := &deploy.NativeCertificate{FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cfg := map[string]string{"panel_url": server.URL, "api_secret": "secret", "target": "docker", "sites": "docker-site"}
	if err := deployBaotaDockerSite(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeploySafelinePanel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/open/cert":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["type"] != float64(2) {
				t.Errorf("unexpected cert payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"err":null,"data":12}`))
		case "/api/open/system":
			if r.Method != http.MethodPut {
				t.Errorf("unexpected method: %s", r.Method)
			}
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["cert_id"] != float64(12) {
				t.Errorf("unexpected system payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"err":null,"data":null}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cert := &deploy.NativeCertificate{FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"panel_url": server.URL, "api_token": "token"}
	if err := deploySafelinePanel(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeploySafelinePortal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/open/portal":
			_, _ = w.Write([]byte(`{"err":null,"data":{"cert_id":5}}`))
		case "/api/open/cert":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["id"] != float64(5) {
				t.Errorf("expected update of cert 5: %v", payload)
			}
			_, _ = w.Write([]byte(`{"err":null,"data":5}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cert := &deploy.NativeCertificate{FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"panel_url": server.URL, "api_token": "token"}
	if err := deploySafelinePortal(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
