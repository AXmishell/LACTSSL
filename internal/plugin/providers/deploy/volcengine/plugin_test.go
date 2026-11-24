package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployVolcengineCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-Date") == "" {
			t.Errorf("missing signature headers")
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch r.URL.Query().Get("Action") {
		case "AddCertificate":
			if payload["Certificate"] != "FULLCHAIN" || payload["PrivateKey"] != "KEY" {
				t.Errorf("certificate missing: %v", payload)
			}
			_, _ = w.Write([]byte(`{"Result":{"CertId":"cert-1"}}`))
		case "UpdateCdnConfig":
			https, _ := payload["HTTPS"].(map[string]any)
			info, _ := https["CertInfo"].(map[string]any)
			if payload["Domain"] != "example.com" || info["CertId"] != "cert-1" || https["Switch"] != true {
				t.Errorf("unexpected update payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"Result":{}}`))
		default:
			t.Errorf("unexpected action: %s", r.URL.Query().Get("Action"))
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	old := volcengineBaseURL
	volcengineBaseURL = server.URL
	t.Cleanup(func() { volcengineBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployVolcengineCDN(cert, map[string]string{"access_key_id": "ak", "secret_access_key": "sk"}); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateCertificate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "AddCertificate" {
			_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"Duplicate","Message":"证书已存在，ID为 cert-9。"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"Result":{}}`))
	}))
	defer server.Close()
	old := volcengineBaseURL
	volcengineBaseURL = server.URL
	t.Cleanup(func() { volcengineBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployVolcengineCDN(cert, map[string]string{"access_key_id": "ak", "secret_access_key": "sk"}); err != nil {
		t.Fatal(err)
	}
}

// 部署节点中的 region 应覆盖凭据中的默认值。
func TestDeployVolcengineRegionFromDeployConfig(t *testing.T) {
	var scopes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopes = append(scopes, r.Header.Get("Authorization"))
		if r.URL.Query().Get("Action") == "AddCertificate" {
			_, _ = w.Write([]byte(`{"Result":{"CertId":"cert-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"Result":{}}`))
	}))
	defer server.Close()
	old := volcengineBaseURL
	volcengineBaseURL = server.URL
	t.Cleanup(func() { volcengineBaseURL = old })

	err := (volcenginePlugin{}).Deploy(context.Background(), deploy.NativeRequest{
		Auth:   map[string]string{"access_key_id": "ak", "secret_access_key": "sk"},
		Config: map[string]string{"region": "cn-shanghai"},
		Certificate: deploy.NativeCertificate{
			Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) == 0 {
		t.Fatal("no signed requests")
	}
	for _, auth := range scopes {
		if !strings.Contains(auth, "/cn-shanghai/cdn/request") {
			t.Fatalf("region from deploy config not used: %q", auth)
		}
	}
}

func TestDeployVolcengineDCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch r.URL.Query().Get("Action") {
		case "AddCertificate":
			_, _ = w.Write([]byte(`{"Result":{"CertId":"cert-dcdn"}}`))
		case "CreateCertBind":
			domains, _ := payload["DomainNames"].([]any)
			if payload["CertId"] != "cert-dcdn" || len(domains) != 1 || domains[0] != "example.com" {
				t.Errorf("unexpected bind payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"Result":{}}`))
		default:
			t.Errorf("unexpected action: %s", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()
	oldCDN, oldDCDN := volcengineBaseURL, volcengineDCDNBaseURL
	volcengineBaseURL, volcengineDCDNBaseURL = server.URL, server.URL
	t.Cleanup(func() { volcengineBaseURL, volcengineDCDNBaseURL = oldCDN, oldDCDN })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployVolcengineDCDN(cert, map[string]string{"access_key_id": "ak", "secret_access_key": "sk"}); err != nil {
		t.Fatal(err)
	}
}
