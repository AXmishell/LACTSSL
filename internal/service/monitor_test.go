package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// TestVerifyPeerChainRejectsSelfSigned 验证监控不会把自签证书当作受信任证书。
func TestVerifyPeerChainRejectsSelfSigned(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "monitor.example.com"},
		DNSNames:     []string{"monitor.example.com"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
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
	if err := cert.VerifyHostname("monitor.example.com"); err != nil {
		t.Fatalf("测试证书应匹配域名: %v", err)
	}
	if err := verifyPeerChain(cert, nil, "monitor.example.com"); err == nil {
		t.Fatal("自签证书不应通过信任链校验")
	}
}
