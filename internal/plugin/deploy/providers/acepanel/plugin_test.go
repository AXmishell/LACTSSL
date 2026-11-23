package acepanel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeploySite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=1, Signature=") {
			t.Errorf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/cert/cert/upload":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["cert"] != "FULLCHAIN" || payload["key"] != "KEY" {
				t.Errorf("unexpected upload payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"id":9}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/website":
			_, _ = w.Write([]byte(`{"code":200,"data":{"items":[{"id":3,"name":"demo","domains":["example.com"]},{"id":4,"name":"other","domains":["other.com"]}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/home/system_info":
			_, _ = w.Write([]byte(`{"code":200,"data":{"panel_version":"3.3.2"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/cert/cert/9/deploy":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			ids, _ := payload["website_ids"].([]any)
			if len(ids) != 1 || ids[0] != float64(3) || payload["enable_https"] != true {
				t.Errorf("unexpected deploy payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":null}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"panel_url":    server.URL,
		"token_id":     "1",
		"access_token": "token",
		"enable_https": "true",
	}
	if err := deploySite(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestAcePanelPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/website" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[{"id":3,"name":"demo","domains":["example.com"]},{"id":4,"name":"other","domains":["other.com","www.other.com"]}]}}`))
	}))
	defer server.Close()

	result, err := (sitePlugin{}).Plan(context.Background(), deploy.NativePlanRequest{
		Auth: map[string]string{"panel_url": server.URL, "token_id": "1", "access_token": "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Field != "websites" || !result.Multiple || len(result.Objects) != 2 {
		t.Fatalf("unexpected plan: %+v", result)
	}
	if result.Objects[0].Value != "demo" || len(result.Objects[0].Domains) != 2 {
		t.Fatalf("unexpected object: %+v", result.Objects[0])
	}
}

func TestDeployPanel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"code":200,"data":{"https":false,"title":"AcePanel"}}`))
		case http.MethodPost:
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["title"] != "AcePanel" || payload["https"] != true || payload["acme"] != false {
				t.Errorf("unexpected setting payload: %v", payload)
			}
			if payload["cert"] != "FULLCHAIN" || payload["key"] != "KEY" {
				t.Errorf("certificate missing: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg := map[string]string{"panel_url": server.URL, "token_id": "1", "access_token": "token"}
	if err := deployPanel(&deploy.NativeCertificate{FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	if !versionAtLeast("3.3.2", "3.3.2") || !versionAtLeast("3.4", "3.3.2") || versionAtLeast("3.3.1", "3.3.2") {
		t.Fatal("version comparison failed")
	}
}
