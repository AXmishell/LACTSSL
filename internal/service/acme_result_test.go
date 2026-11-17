package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
)

func makeTestCertificate(t *testing.T, dnsNames []string, ips []net.IP) (*x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     dnsNames,
		IPAddresses:  ips,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certcrypto.PEMEncode(key)
}

func TestValidateACMEResult(t *testing.T) {
	cert, keyPEM := makeTestCertificate(t, []string{"example.com"}, nil)

	result := &ACMEResult{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		KeyPEM:  string(keyPEM),
	}
	if err := validateACMEResult([]string{"example.com"}, result); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if err := validateACMEResult([]string{"other.example"}, result); err == nil {
		t.Fatal("domain mismatch should be rejected")
	}

	otherCert, _ := makeTestCertificate(t, []string{"example.com"}, nil)
	result.CertPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherCert.Raw}))
	if err := validateACMEResult([]string{"example.com"}, result); err == nil {
		t.Fatal("certificate/private key mismatch should be rejected")
	}
}

func TestIssuePrivateCertificateSeparatesIP(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{Name: "Test CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	result, err := IssuePrivateCertificate(ca, PrivateCertRequest{
		Domains:   []string{"example.com", "127.0.0.1"},
		KeyType:   "ecdsa",
		ValidDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := parseFirstCertificate(result.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "example.com" {
		t.Fatalf("DNS SAN 不正确: %v", leaf.DNSNames)
	}
	if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("IP SAN 不正确: %v", leaf.IPAddresses)
	}
	if leaf.Subject.CommonName != "example.com" {
		t.Fatalf("CN 应优先使用域名: %s", leaf.Subject.CommonName)
	}
}
