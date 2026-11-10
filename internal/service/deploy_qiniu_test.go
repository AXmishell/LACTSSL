package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
