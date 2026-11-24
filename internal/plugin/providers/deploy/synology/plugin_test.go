package synology

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeploySynology(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/webapi/query.cgi":
			_, _ = w.Write([]byte(`{"success":true,"data":{"SYNO.API.Auth":{"path":"auth.cgi","maxVersion":6}}}`))
		case "/webapi/auth.cgi":
			if err := r.ParseForm(); err != nil {
				t.Errorf("form: %v", err)
			}
			switch r.Form.Get("method") {
			case "login":
				if r.Form.Get("account") != "admin" || r.Form.Get("passwd") != "secret" {
					t.Errorf("unexpected login form: %v", r.Form)
				}
				if r.Form.Get("otp_code") == "" {
					t.Errorf("missing otp code")
				}
				if r.URL.RawQuery != "" {
					t.Errorf("login must not put credentials in query: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"sid":"sid-1","synotoken":"token-1"}}`))
			case "logout":
				if r.URL.RawQuery != "" {
					t.Errorf("logout must not put _sid in query: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"success":true}`))
			}
		case "/webapi/entry.cgi":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				if err := r.ParseForm(); err != nil {
					t.Errorf("form: %v", err)
				}
			}
			if r.FormValue("api") == "SYNO.Core.Certificate" {
				if r.Header.Get("X-SYNO-TOKEN") != "token-1" {
					t.Errorf("missing syno token")
				}
				if r.FormValue("id") != "cert-1" || r.FormValue("desc") != "demo" || r.FormValue("as_default") != "true" {
					t.Errorf("unexpected import fields: %v", r.MultipartForm.Value)
				}
				if r.FormValue("_sid") != "sid-1" || r.FormValue("SynoToken") != "token-1" {
					t.Errorf("import session fields missing: %v", r.MultipartForm.Value)
				}
				if r.URL.RawQuery != "" {
					t.Errorf("import must not put session in query: %s", r.URL.RawQuery)
				}
				if _, _, err := r.FormFile("key"); err != nil {
					t.Errorf("missing key file: %v", err)
				}
				if _, _, err := r.FormFile("cert"); err != nil {
					t.Errorf("missing cert file: %v", err)
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"restart_httpd":true}}`))
				return
			}
			if r.Form.Get("api") != "SYNO.Core.Certificate.CRT" || r.Form.Get("_sid") != "sid-1" {
				t.Errorf("unexpected list form: %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"certificates":[{"id":"cert-1","desc":"demo","is_default":true,"subject":{"common_name":"example.com"}}]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"panel_url":   server.URL,
		"username":    "admin",
		"password":    "secret",
		"otp_secret":  "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"certificate": "demo",
	}
	if err := deploySynology(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCertificates(t *testing.T) {
	matched := certificate{ID: "1", Desc: "demo"}
	matched.Subject.CommonName = "example.com"
	unmatched := certificate{ID: "2", Desc: "other"}
	unmatched.Subject.CommonName = "other.example"
	list := []certificate{matched, unmatched}
	cert := &deploy.NativeCertificate{Domain: "example.com"}

	if got := resolveCertificates(list, "demo", false, cert); len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("by desc = %+v", got)
	}
	if got := resolveCertificates(list, "missing", false, cert); len(got) != 0 {
		t.Fatalf("missing = %+v", got)
	}
	if got := resolveCertificates(list, "new", true, cert); len(got) != 1 || got[0].Desc != "new" {
		t.Fatalf("create = %+v", got)
	}
	if got := resolveCertificates(list, "", false, cert); len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("domain match = %+v", got)
	}
}

func TestSynologyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/webapi/query.cgi" {
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":403}}`))
	}))
	defer server.Close()
	cfg := map[string]string{"panel_url": server.URL, "username": "admin", "password": "secret"}
	err := deploySynology(&deploy.NativeCertificate{}, cfg)
	if err == nil || !strings.Contains(err.Error(), "OTP") && !strings.Contains(err.Error(), "二次") {
		t.Fatalf("err = %v", err)
	}
}
