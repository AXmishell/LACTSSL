package plesk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployPlesk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login_up.php":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("login form: %v", err)
			}
			if r.FormValue("login_name") != "admin" || r.FormValue("passwd") != "secret" {
				t.Errorf("unexpected login: %v", r.MultipartForm.Value)
			}
			http.SetCookie(w, &http.Cookie{Name: "PLESKSESSID", Value: "session-1"})
			w.WriteHeader(http.StatusSeeOther)
		case "/api/v2/domains":
			user, pass, ok := r.BasicAuth()
			if !ok || user != "admin" || pass != "secret" {
				t.Errorf("missing basic auth")
			}
			_, _ = w.Write([]byte(`[{"id":2,"name":"example.com"}]`))
		case "/modules/sslit/index.php/index/certificate/id/2":
			if r.Header.Get("Cookie") != "PLESKSESSID=session-1" {
				t.Errorf("missing session cookie")
			}
			_, _ = w.Write([]byte(`<meta name="forgery_protection_token" content="token-1">`))
		case "/modules/sslit/index.php/index/upload/":
			if r.Header.Get("Cookie") != "PLESKSESSID=session-1" || r.Header.Get("X-Forgery-Protection-Token") != "token-1" {
				t.Errorf("missing upload auth headers")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("upload form: %v", err)
			}
			if r.FormValue("id") != "2" || r.FormValue("fileName") != "cert.pem" {
				t.Errorf("unexpected upload fields: %v", r.MultipartForm.Value)
			}
			file, _, err := r.FormFile("fileContent")
			if err != nil {
				t.Errorf("fileContent: %v", err)
			} else {
				data := make([]byte, 1024)
				n, _ := file.Read(data)
				if !strings.Contains(string(data[:n]), "FULLCHAIN") || !strings.Contains(string(data[:n]), "KEY") {
					t.Errorf("unexpected one pem: %q", data[:n])
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"url": server.URL, "username": "admin", "password": "secret"}
	if err := deployPlesk(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPleskUploadResponse(t *testing.T) {
	if err := checkPleskUploadResponse([]byte(`{"status":"success"}`)); err != nil {
		t.Fatalf("success: %v", err)
	}
	if err := checkPleskUploadResponse(nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	for _, raw := range []string{`{"status":"failed"}`, `<html>login</html>`, `{}`} {
		if err := checkPleskUploadResponse([]byte(raw)); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestPleskLoginFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`login failed`))
	}))
	defer server.Close()
	err := deployPlesk(&deploy.NativeCertificate{}, map[string]string{"url": server.URL, "username": "admin", "password": "bad"})
	if err == nil || !strings.Contains(err.Error(), "登录") {
		t.Fatalf("err = %v", err)
	}
}
