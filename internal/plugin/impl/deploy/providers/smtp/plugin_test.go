package smtp

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

func TestBuildArchive(t *testing.T) {
	cert := &deploy.Certificate{
		Domain:       "example.com",
		CertPEM:      "CERT",
		FullchainPEM: "CERT\n-----END CERTIFICATE-----\nCHAIN\n-----END CERTIFICATE-----",
		KeyPEM:       "KEY",
	}
	data, filename, err := buildArchive(cert)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "cert-example.com.zip" {
		t.Fatalf("filename = %q", filename)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, want := range []string{"cert.pem", "fullchain.pem", "privkey.pem", "chain.pem"} {
		if !names[want] {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	cert := &deploy.Certificate{Domain: "example.com", NotAfter: "2027-01-01"}
	message, err := buildMessage("LACTSSL <noreply@example.com>", []string{"a@example.com", "b@example.com"}, "测试", cert, "cert.zip", []byte("zipdata"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	for _, want := range []string{"To: a@example.com, b@example.com", "multipart/mixed", "application/zip", "cert.zip"} {
		if !strings.Contains(text, want) {
			t.Fatalf("message missing %q", want)
		}
	}
}

func TestValidation(t *testing.T) {
	cert := &deploy.Certificate{Domain: "example.com"}
	cases := []map[string]string{
		{"from": "a@example.com"},
		{"host": "smtp.example.com"},
		{"host": "smtp.example.com", "from": "a@example.com"},
		{"host": "smtp.example.com", "from": "a@example.com", "to": "b@example.com", "port": "bad"},
		{"host": "smtp.example.com", "from": "a@example.com", "to": "b@example.com", "tls": "bad"},
	}
	for index, cfg := range cases {
		if err := deploySMTP(cert, cfg); err == nil {
			t.Fatalf("case %d: expected error", index)
		}
	}
}

func TestChainPEM(t *testing.T) {
	chain := util.ChainPEM("LEAF\n-----END CERTIFICATE-----\nINTERMEDIATE\n-----END CERTIFICATE-----")
	if !strings.Contains(chain, "INTERMEDIATE") || strings.Contains(chain, "LEAF") {
		t.Fatalf("chain = %q", chain)
	}
}
