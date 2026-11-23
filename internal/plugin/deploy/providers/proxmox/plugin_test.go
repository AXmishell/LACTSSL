package proxmox

import (
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeployProxmox(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/access/ticket":
			if err := r.ParseForm(); err != nil {
				t.Errorf("form: %v", err)
			}
			if r.Form.Get("username") != "root@pam" || r.Form.Get("password") != "secret" {
				t.Errorf("unexpected login form: %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"data":{"ticket":"PVE:root@pam:ticket","CSRFPreventionToken":"csrf"}}`))
		case "/api2/json/nodes/pve/certificates/custom":
			if r.Header.Get("Cookie") != "PVEAuthCookie=PVE:root@pam:ticket" || r.Header.Get("CSRFPreventionToken") != "csrf" {
				t.Errorf("missing auth headers")
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("form: %v", err)
			}
			if r.Form.Get("certificates") != "FULLCHAIN" || r.Form.Get("key") != "KEY" || r.Form.Get("force") != "1" {
				t.Errorf("unexpected upload form: %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"host": server.URL, "username": "root", "password": "secret", "realm": "pam", "nodes": "pve",
	}
	if err := deployProxmox(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
