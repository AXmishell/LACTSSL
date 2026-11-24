package aliyuncloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployDCDNAndESA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		if r.Form.Get("Signature") == "" {
			t.Errorf("missing signature")
		}
		switch r.Form.Get("Action") {
		case "SetDcdnDomainSSLCertificate":
			if r.Form.Get("DomainName") != "example.com" || r.Form.Get("SSLPub") != "FULLCHAIN" {
				t.Errorf("unexpected dcdn params: %v", r.Form)
			}
		case "SetCertificate":
			if r.Form.Get("SiteId") != "123" || r.Form.Get("Type") != "upload" || r.Form.Get("PrivateKey") != "KEY" {
				t.Errorf("unexpected esa params: %v", r.Form)
			}
		default:
			t.Errorf("unexpected action: %s", r.Form.Get("Action"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	oldDCDN, oldESA := aliyunDCDNEndpoint, aliyunESAEndpoint
	aliyunDCDNEndpoint, aliyunESAEndpoint = server.URL, server.URL
	t.Cleanup(func() { aliyunDCDNEndpoint, aliyunESAEndpoint = oldDCDN, oldESA })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployDCDN(cert, map[string]string{"access_key_id": "ak", "access_key_secret": "sk"}); err != nil {
		t.Fatal(err)
	}
	if err := deployESA(cert, map[string]string{"access_key_id": "ak", "access_key_secret": "sk", "site_id": "123"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeployWAF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		switch r.Form.Get("Action") {
		case "UploadUserCertificate":
			_, _ = w.Write([]byte(`{"CertId":123}`))
		case "DescribeInstance":
			_, _ = w.Write([]byte(`{"InstanceId":"inst-1"}`))
		case "DescribeDomainDetail":
			_, _ = w.Write([]byte(`{"Listen":{"HttpsPorts":[443],"CertId":"old"},"Redirect":{"AllBackends":[{"Address":"1.1.1.1"}]}}`))
		case "ModifyDomain":
			var listen map[string]any
			var redirect map[string]any
			_ = json.Unmarshal([]byte(r.Form.Get("Listen")), &listen)
			_ = json.Unmarshal([]byte(r.Form.Get("Redirect")), &redirect)
			if listen["CertId"] != "123-cn-hangzhou" {
				t.Errorf("listen cert = %v", listen["CertId"])
			}
			if redirect["Backends"] == nil {
				t.Errorf("redirect backends missing: %v", redirect)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected action: %s", r.Form.Get("Action"))
		}
	}))
	defer server.Close()
	oldCAS, oldWAF := aliyunCASEndpoint, aliyunWAFEndpoint
	aliyunCASEndpoint, aliyunWAFEndpoint = server.URL, server.URL
	t.Cleanup(func() { aliyunCASEndpoint, aliyunWAFEndpoint = oldCAS, oldWAF })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	err := deployWAF(cert, map[string]string{
		"access_key_id": "ak", "access_key_secret": "sk", "region": "cn-hangzhou",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestESAValidation(t *testing.T) {
	err := deployESA(&deploy.NativeCertificate{}, map[string]string{"access_key_id": "ak", "access_key_secret": "sk"})
	if err == nil || !strings.Contains(err.Error(), "站点") {
		t.Fatalf("err = %v", err)
	}
}
