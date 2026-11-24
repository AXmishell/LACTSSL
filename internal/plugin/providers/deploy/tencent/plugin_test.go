package tencent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployTencentCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch r.Header.Get("X-TC-Action") {
		case "UploadCertificate":
			if payload["CertificatePublicKey"] != "FULLCHAIN" || payload["CertificatePrivateKey"] != "KEY" {
				t.Errorf("certificate missing: %v", payload)
			}
			_, _ = w.Write([]byte(`{"Response":{"CertificateId":"cert-1","RequestId":"r1"}}`))
		case "DeployCertificateInstance":
			instances, _ := payload["InstanceIdList"].([]any)
			if payload["CertificateId"] != "cert-1" || payload["ResourceType"] != "cdn" || len(instances) != 1 || instances[0] != "example.com|on" {
				t.Errorf("unexpected deploy payload: %v", payload)
			}
			_, _ = w.Write([]byte(`{"Response":{"RequestId":"r2"}}`))
		default:
			t.Errorf("unexpected action: %s", r.Header.Get("X-TC-Action"))
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	old := tencentSSLBaseURL
	tencentSSLBaseURL = server.URL
	t.Cleanup(func() { tencentSSLBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployTencent("cdn", cert, map[string]string{"secret_id": "id", "secret_key": "key"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeployTencentCOS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if r.Header.Get("X-TC-Action") == "UploadCertificate" {
			_, _ = w.Write([]byte(`{"Response":{"CertificateId":"cert-2","RequestId":"r1"}}`))
			return
		}
		instances, _ := payload["InstanceIdList"].([]any)
		if payload["ResourceType"] != "cos" || len(instances) != 1 || instances[0] != "ap-guangzhou|bucket-1|cdn.example.com" {
			t.Errorf("unexpected cos payload: %v", payload)
		}
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"r2"}}`))
	}))
	defer server.Close()
	old := tencentSSLBaseURL
	tencentSSLBaseURL = server.URL
	t.Cleanup(func() { tencentSSLBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "cdn.example.com", Domains: []string{"cdn.example.com"}, FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{"secret_id": "id", "secret_key": "key", "bucket": "bucket-1", "region": "ap-guangzhou"}
	if err := deployTencent("cos", cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestTencentValidation(t *testing.T) {
	if err := deployTencent("cdn", &deploy.NativeCertificate{}, map[string]string{}); err == nil {
		t.Fatal("expected key error")
	}
}

func TestDeployTencentWAF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") == "UploadCertificate" {
			_, _ = w.Write([]byte(`{"Response":{"CertificateId":"cert-3","RequestId":"r1"}}`))
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["ResourceType"] != "waf" {
			t.Errorf("resource type = %v", payload["ResourceType"])
		}
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"r2"}}`))
	}))
	defer server.Close()
	old := tencentSSLBaseURL
	tencentSSLBaseURL = server.URL
	t.Cleanup(func() { tencentSSLBaseURL = old })
	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployTencent("waf", cert, map[string]string{"secret_id": "id", "secret_key": "key"}); err != nil {
		t.Fatal(err)
	}
}
