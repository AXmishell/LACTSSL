package aliyunoss

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployAliyunOSS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "cname&comp=add" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "OSS ak:") {
			t.Errorf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-MD5") == "" {
			t.Errorf("missing content md5")
		}
		body, _ := io.ReadAll(r.Body)
		var config bucketCnameConfiguration
		if err := xml.Unmarshal(body, &config); err != nil {
			t.Fatalf("xml: %v", err)
		}
		if config.Cname.Domain != "example.com" || config.Cname.CertificateConfiguration.Certificate != "FULLCHAIN" {
			t.Errorf("unexpected xml: %+v", config)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	oldScheme, oldHost := aliyunOSSScheme, aliyunOSSHost
	aliyunOSSScheme = "http"
	aliyunOSSHost = func(bucket, region string) string { return strings.TrimPrefix(server.URL, "http://") }
	t.Cleanup(func() { aliyunOSSScheme, aliyunOSSHost = oldScheme, oldHost })

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	cfg := map[string]string{
		"access_key_id": "ak", "access_key_secret": "sk", "region": "oss-cn-hangzhou",
		"bucket": "demo", "domains": "example.com",
	}
	if err := deployAliyunOSS(cert, cfg); err != nil {
		t.Fatal(err)
	}
}
