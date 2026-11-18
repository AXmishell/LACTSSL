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

// TestSignCSRRejectsCAWithoutServerAuthEKU 验证 ACME 签发路径也遵守 CA 的
// ExtKeyUsage 约束，不能再用只允许代码签名的 CA 签发 TLS 证书。
func TestSignCSRRejectsCAWithoutServerAuthEKU(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:        "Code Signing CA",
		KeyType:     "ecdsa",
		ValidDays:   365,
		ExtKeyUsage: []string{"code_signing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateIssuingCA(ca); err == nil {
		t.Fatal("仅允许 code_signing 的 CA 不应被 ACME 端点接受")
	}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com"},
	}, key)
	csr, _ := x509.ParseCertificateRequest(csrDER)
	if _, err := SignCSR(ca, csr, []CertificateIdentifier{{Type: "dns", Value: "example.com"}}, SignCSROptions{}); err == nil {
		t.Fatal("ACME 签发路径不应绕过 CA 的 ExtKeyUsage")
	}
}

// TestSignCSREnforcesNameConstraints 验证 ACME 签发路径执行 CA 的
// NameConstraints，避免签发越界域名。
func TestSignCSREnforcesNameConstraints(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:         "Constrained CA",
		KeyType:      "ecdsa",
		ValidDays:    365,
		PermittedDNS: []string{".example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sign := func(domain string) error {
		t.Helper()
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
			Subject:  pkix.Name{CommonName: domain},
			DNSNames: []string{domain},
		}, key)
		csr, _ := x509.ParseCertificateRequest(csrDER)
		_, err := SignCSR(ca, csr, []CertificateIdentifier{{Type: "dns", Value: domain}}, SignCSROptions{})
		return err
	}
	if err := sign("www.example.com"); err != nil {
		t.Fatalf("允许子树内的域名应可签发: %v", err)
	}
	if err := sign("evil.com"); err == nil {
		t.Fatal("NameConstraints 之外域名不应签发")
	}
}

// TestIssuePrivateCertificateEnforcesNameConstraints 验证任务签发路径同样执行
// NameConstraints（两条路径共用 validateIssuingCAUsage）。
func TestIssuePrivateCertificateEnforcesNameConstraints(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:         "Constrained Task CA",
		KeyType:      "ecdsa",
		ValidDays:    365,
		PermittedDNS: []string{".example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssuePrivateCertificate(ca, PrivateCertRequest{Domains: []string{"www.example.com"}, KeyType: "ecdsa"}); err != nil {
		t.Fatalf("允许子树内的域名应可签发: %v", err)
	}
	if _, err := IssuePrivateCertificate(ca, PrivateCertRequest{Domains: []string{"evil.com"}, KeyType: "ecdsa"}); err == nil {
		t.Fatal("NameConstraints 之外域名不应签发")
	}
}
