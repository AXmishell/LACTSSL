package baidu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deploy "lactssl/internal/plugin/deploy"
)

func TestDeployBaiduCDN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v2/example.com/certificates" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "bce-auth-v1/ak/") {
			t.Errorf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("x-bce-date") == "" {
			t.Errorf("missing x-bce-date")
		}
		var payload struct {
			Certificate map[string]string `json:"certificate"`
			HTTPSEnable string            `json:"httpsEnable"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Certificate["certServerData"] != "FULLCHAIN" || payload.Certificate["certPrivateData"] != "KEY" || payload.HTTPSEnable != "ON" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		_, _ = w.Write([]byte(`{"certId":"cert-1"}`))
	}))
	defer server.Close()
	old := baiduCDNBaseURL
	baiduCDNBaseURL = server.URL
	t.Cleanup(func() { baiduCDNBaseURL = old })

	cert := &deploy.NativeCertificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployBaiduCDN(cert, map[string]string{"access_key": "ak", "secret_key": "sk"}); err != nil {
		t.Fatal(err)
	}
}

func TestBaiduValidation(t *testing.T) {
	if err := deployBaiduCDN(&deploy.NativeCertificate{}, map[string]string{}); err == nil {
		t.Fatal("expected key error")
	}
}
