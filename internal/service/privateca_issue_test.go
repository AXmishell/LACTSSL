package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
	"time"
)

func TestSignCSRIssuesLeaf(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com", "www.example.com"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := SignCSR(ca, csr, []CertificateIdentifier{
		{Type: "dns", Value: "example.com"},
		{Type: "dns", Value: "www.example.com"},
	}, SignCSROptions{
		NotBefore: time.Now().UTC().Add(-time.Minute),
		NotAfter:  time.Now().UTC().AddDate(0, 0, 90),
		ChainPEM:  ca.CertPEM,
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	leaf := parseTestCertificate(t, issued.CertPEM)
	caCert := parseTestCertificate(t, ca.CertPEM)
	if err := leaf.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("叶子证书签名链不正确: %v", err)
	}
	if leaf.IsCA {
		t.Fatal("叶子证书不应是 CA")
	}
	if len(leaf.DNSNames) != 2 || leaf.DNSNames[0] != "example.com" {
		t.Fatalf("SAN 不正确: %v", leaf.DNSNames)
	}
	if leaf.Subject.CommonName != "example.com" {
		t.Fatalf("CN 不正确: %s", leaf.Subject.CommonName)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("扩展密钥用途不正确: %v", leaf.ExtKeyUsage)
	}
	if leaf.NotAfter.After(caCert.NotAfter) {
		t.Fatal("叶子证书有效期超过 CA")
	}
	if issued.Serial == "" || issued.FullchainPEM == issued.CertPEM {
		t.Fatal("签发结果缺少序列号或证书链")
	}
	if err := ValidateIssuingCA(ca); err != nil {
		t.Fatalf("私有 CA 应可用于签发: %v", err)
	}
}

func TestSignCSRRejectsIdentifierMismatch(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com"},
	}, key)
	csr, _ := x509.ParseCertificateRequest(csrDER)
	_, err = SignCSR(ca, csr, []CertificateIdentifier{{Type: "dns", Value: "other.example.com"}}, SignCSROptions{})
	if err == nil {
		t.Fatal("CSR 与订单标识不一致时应拒绝签发")
	}
}

func TestSignCSRRejectsInvalidCSR(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignCSR(ca, nil, nil, SignCSROptions{}); err == nil {
		t.Fatal("空 CSR 应返回错误")
	}
}

func TestSignCSRRSALeafKeyUsage(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "rsa.example.com"},
		DNSNames: []string{"rsa.example.com"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, _ := x509.ParseCertificateRequest(csrDER)
	issued, err := SignCSR(ca, csr, []CertificateIdentifier{{Type: "dns", Value: "rsa.example.com"}}, SignCSROptions{})
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseTestCertificate(t, issued.CertPEM)
	if leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Fatalf("RSA 叶子证书应包含 KeyEncipherment: %v", leaf.KeyUsage)
	}
}
