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

func TestDeployQiniuKodo(t *testing.T) {
	var (
		mu       sync.Mutex
		uploaded map[string]string
		bindings []map[string]string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sslcert":
			if got, want := r.Header.Get("Authorization"), "QBox "+qiniuQBoxToken("test-ak", "test-sk", "/sslcert"); got != want {
				t.Errorf("unexpected upload authorization: %q, want %q", got, want)
			}
			var payload map[string]string
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid upload payload: %v", err)
			}
			mu.Lock()
			uploaded = payload
			mu.Unlock()
			_, _ = w.Write([]byte(`{"certID":"cid-123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/cert/bind":
			if got, want := r.Header.Get("Authorization"), "QBox "+qiniuQBoxToken("test-ak", "test-sk", "/cert/bind"); got != want {
				t.Errorf("unexpected bind authorization: %q, want %q", got, want)
			}
			var payload map[string]string
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

	oldCertBase, oldKodoBase := qiniuCertBaseURL, qiniuKodoBaseURL
	qiniuCertBaseURL, qiniuKodoBaseURL = server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuKodoBaseURL = oldCertBase, oldKodoBase
	}()

	cert := &model.Certificate{
		PrimaryDomain: "img.example.com",
		Domains:       []string{"img.example.com", "static.example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{
		"access_key": "test-ak",
		"secret_key": "test-sk",
		"domains":    "img.example.com, static.example.com",
		"cert_name":  "test-cert",
	}
	if err := deployQiniuKodo(cert, cfg); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if uploaded["name"] != "test-cert" || uploaded["common_name"] != "img.example.com" {
		t.Fatalf("unexpected upload payload: %v", uploaded)
	}
	if uploaded["ca"] != "FULLCHAIN" || uploaded["pri"] != "PRIVATE-KEY" {
		t.Fatalf("certificate contents not uploaded: %v", uploaded)
	}
	if len(bindings) != 2 {
		t.Fatalf("unexpected bind requests: %v", bindings)
	}
	for index, domain := range []string{"img.example.com", "static.example.com"} {
		if bindings[index]["certid"] != "cid-123" || bindings[index]["domain"] != domain {
			t.Fatalf("unexpected bind request %d: %v", index, bindings[index])
		}
	}
}

func TestDeployQiniuKodoAcceptsSingleDomain(t *testing.T) {
	var (
		mu       sync.Mutex
		bindings []map[string]string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/sslcert" {
			_, _ = w.Write([]byte(`{"certID":"cid-456"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		bindings = append(bindings, payload)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer server.Close()

	oldCertBase, oldKodoBase := qiniuCertBaseURL, qiniuKodoBaseURL
	qiniuCertBaseURL, qiniuKodoBaseURL = server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuKodoBaseURL = oldCertBase, oldKodoBase
	}()

	cert := &model.Certificate{
		PrimaryDomain: "img.example.com",
		Domains:       []string{"img.example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	if err := deployQiniuKodo(cert, map[string]string{
		"access_key": "test-ak",
		"secret_key": "test-sk",
		"domain":     "origin.example.com",
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bindings) != 1 || bindings[0]["domain"] != "origin.example.com" {
		t.Fatalf("unexpected bind requests: %v", bindings)
	}
}

func TestDeployQiniuKodoCDNDomainHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/sslcert" {
			_, _ = w.Write([]byte(`{"certID":"cid-789"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":400302,"error":"domaintype is cdn"}`))
	}))
	defer server.Close()

	oldCertBase, oldKodoBase := qiniuCertBaseURL, qiniuKodoBaseURL
	qiniuCertBaseURL, qiniuKodoBaseURL = server.URL, server.URL
	defer func() {
		qiniuCertBaseURL, qiniuKodoBaseURL = oldCertBase, oldKodoBase
	}()

	cert := &model.Certificate{
		PrimaryDomain: "img.example.com",
		Domains:       []string{"img.example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	err := deployQiniuKodo(cert, map[string]string{
		"access_key": "test-ak",
		"secret_key": "test-sk",
		"domains":    "img.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "CDN") {
		t.Fatalf("expected CDN domain hint, got %v", err)
	}
}
