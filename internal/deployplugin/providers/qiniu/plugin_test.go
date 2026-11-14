package qiniu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lactssl/internal/deployplugin"
	"lactssl/internal/model"
)

func TestQiniuAuthTokens(t *testing.T) {
	if got := qiniuQBoxToken("test-ak", "test-sk", "/sslcert"); got != "test-ak:adrpEVnUT2GWCvY1dL6d6rfc1EI=" {
		t.Fatalf("unexpected QBox token: %q", got)
	}
	got := qiniuRequestToken(
		"test-ak", "test-sk", http.MethodPut,
		"/domain/cdn.example.com/httpsconf", "api.qiniu.com",
		"application/json", "20260102T030405Z", `{"certId":"cid-123"}`,
	)
	if got != "test-ak:MXt1LtMO0NNvIWzvXacFoiQa3BY=" {
		t.Fatalf("unexpected Qiniu token: %q", got)
	}
}

func TestDeployQiniuCDN(t *testing.T) {
	var (
		mu       sync.Mutex
		uploaded map[string]string
		bindings []map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sslcert":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "QBox ") {
				t.Errorf("upload should use QBox auth, got %q", r.Header.Get("Authorization"))
			}
			var payload map[string]string
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid upload payload: %v", err)
			}
			mu.Lock()
			uploaded = payload
			mu.Unlock()
			_, _ = w.Write([]byte(`{"certID":"cid-123"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/domain/cdn.example.com/httpsconf":
			want := "Qiniu " + qiniuRequestToken(
				"test-ak", "test-sk", r.Method, r.URL.RequestURI(), r.Host,
				"application/json", r.Header.Get("X-Qiniu-Date"), string(body),
			)
			if got := r.Header.Get("Authorization"); got != want {
				t.Errorf("unexpected bind authorization: %q, want %q", got, want)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid bind payload: %v", err)
			}
			mu.Lock()
			bindings = append(bindings, payload)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldCertBase, oldDomainBase := qiniuCertBaseURL, qiniuDomainBaseURL
	qiniuCertBaseURL, qiniuDomainBaseURL = server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuDomainBaseURL = oldCertBase, oldDomainBase
	}()

	cert := &model.Certificate{
		PrimaryDomain: "cdn.example.com",
		Domains:       []string{"cdn.example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{
		"access_key": "test-ak",
		"secret_key": "test-sk",
		"domains":    "cdn.example.com",
		"cert_name":  "test-cert",
	}
	if err := deployQiniuCDN(cert, cfg); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if uploaded["name"] != "test-cert" || uploaded["common_name"] != "cdn.example.com" {
		t.Fatalf("unexpected upload payload: %v", uploaded)
	}
	if uploaded["ca"] != "FULLCHAIN" || uploaded["pri"] != "PRIVATE-KEY" {
		t.Fatalf("certificate contents not uploaded: %v", uploaded)
	}
	if len(bindings) != 1 || bindings[0]["certId"] != "cid-123" {
		t.Fatalf("unexpected bind requests: %v", bindings)
	}
}

func TestDeployQiniuCDNFallsBackToSSLize(t *testing.T) {
	var (
		mu         sync.Mutex
		sslizeBody map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sslcert":
			_, _ = w.Write([]byte(`{"certID":"cid-456"}`))
		case r.URL.Path == "/domain/cdn.example.com/httpsconf":
			_, _ = w.Write([]byte(`{"code":400302,"error":"域名未开启 HTTPS"}`))
		case r.URL.Path == "/domain/cdn.example.com/sslize":
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid sslize payload: %v", err)
			}
			mu.Lock()
			sslizeBody = payload
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldCertBase, oldDomainBase := qiniuCertBaseURL, qiniuDomainBaseURL
	qiniuCertBaseURL, qiniuDomainBaseURL = server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuDomainBaseURL = oldCertBase, oldDomainBase
	}()

	cert := &model.Certificate{
		PrimaryDomain: "cdn.example.com",
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{
		"access_key": "test-ak",
		"secret_key": "test-sk",
		"domains":    "cdn.example.com",
	}
	if err := deployQiniuCDN(cert, cfg); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sslizeBody["certId"] != "cid-456" || sslizeBody["http2Enable"] != true {
		t.Fatalf("unexpected sslize payload: %v", sslizeBody)
	}
}

func TestQiniuPluginDispatch(t *testing.T) {
	var (
		mu        sync.Mutex
		cdnBound  bool
		kodoBound bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sslcert":
			_, _ = w.Write([]byte(`{"certID":"cid-999"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/domain/img.example.com/httpsconf":
			mu.Lock()
			cdnBound = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":200}`))
		case r.Method == http.MethodPost && r.URL.Path == "/cert/bind":
			mu.Lock()
			kodoBound = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldCertBase, oldDomainBase, oldKodoBase := qiniuCertBaseURL, qiniuDomainBaseURL, qiniuKodoBaseURL
	qiniuCertBaseURL, qiniuDomainBaseURL, qiniuKodoBaseURL = server.URL, server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuDomainBaseURL, qiniuKodoBaseURL = oldCertBase, oldDomainBase, oldKodoBase
	}()

	certificate := deployplugin.Certificate{
		Domain:       "img.example.com",
		Domains:      []string{"img.example.com"},
		FullchainPEM: "FULLCHAIN",
		KeyPEM:       "PRIVATE-KEY",
	}
	auth := map[string]string{"access_key": "test-ak", "secret_key": "test-sk"}
	config := map[string]string{"domains": "img.example.com", "cert_name": "test-cert"}

	if err := (qiniuPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: config, Certificate: certificate}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if !cdnBound || kodoBound {
		t.Fatalf("empty domain_type should use CDN: cdn=%v kodo=%v", cdnBound, kodoBound)
	}
	cdnBound, kodoBound = false, false
	mu.Unlock()

	kodoConfig := map[string]string{"domains": "img.example.com", "cert_name": "test-cert", "domain_type": "kodo"}
	if err := (qiniuPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: kodoConfig, Certificate: certificate}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if cdnBound || !kodoBound {
		t.Fatalf("kodo domain_type should use Kodo: cdn=%v kodo=%v", cdnBound, kodoBound)
	}
	mu.Unlock()

	badConfig := map[string]string{"domains": "img.example.com", "domain_type": "unknown"}
	if err := (qiniuPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: badConfig, Certificate: certificate}); err == nil {
		t.Fatal("invalid domain_type should fail")
	}
}
