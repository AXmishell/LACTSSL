package ucloud

import (
	"net/http"
	"net/http/httptest"
	"testing"

	deploy "lactssl/providers/api/deploy"
)

func TestDeployUCloudCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		if r.Form.Get("Signature") == "" || r.Form.Get("PublicKey") != "public" {
			t.Errorf("missing signature: %v", r.Form)
		}
		switch r.Form.Get("Action") {
		case "UploadNormalCertificate":
			if r.Form.Get("SslPublicKey") == "" || r.Form.Get("SslMD5") == "" {
				t.Errorf("certificate params missing")
			}
			_, _ = w.Write([]byte(`{"RetCode":0,"CertificateID":5}`))
		case "GetUcdnDomainConfig":
			_, _ = w.Write([]byte(`{"RetCode":0,"DomainList":[{"Domain":"example.com","DomainId":"ucdn-1","HttpsStatusCn":"disable","HttpsStatusAbroad":"disable"}]}`))
		case "UpdateUcdnDomainHttpsConfigV2":
			if r.Form.Get("DomainId") != "ucdn-1" || r.Form.Get("CertId") != "5" || r.Form.Get("HttpsStatusCn") != "enable" {
				t.Errorf("unexpected update params: %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"RetCode":0}`))
		default:
			t.Errorf("unexpected action: %s", r.Form.Get("Action"))
		}
	}))
	defer server.Close()
	old := ucloudBaseURL
	ucloudBaseURL = server.URL
	t.Cleanup(func() { ucloudBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"public_key": "public", "private_key": "private", "project_id": "project",
	}
	if err := deployUCloudCDN(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
