package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testCertificate(t *testing.T, commonName string) (*x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{commonName},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestBuildFullchainPEMNormalizes(t *testing.T) {
	leaf, leafPEM := testCertificate(t, "leaf.example.com")
	issuer, issuerPEM := testCertificate(t, "issuer.example.com")

	chain, err := BuildFullchainPEM(leaf, append(append([]byte{}, leafPEM...), issuerPEM...))
	if err != nil {
		t.Fatalf("合法链应通过: %v", err)
	}
	if !strings.HasPrefix(chain, string(leafPEM)) {
		t.Fatal("leaf 应位于完整链首位")
	}
	if strings.Count(chain, "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("完整链应包含 2 张证书: %s", chain)
	}
	// 重复证书应去重。
	chain, err = BuildFullchainPEM(leaf, issuerPEM, issuerPEM, leafPEM)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(chain, "BEGIN CERTIFICATE") != 2 {
		t.Fatal("重复证书应被去重")
	}
	_ = issuer
}

func TestBuildFullchainPEMRejectsTrailingContent(t *testing.T) {
	leaf, leafPEM := testCertificate(t, "leaf.example.com")
	cases := [][]byte{
		append(append([]byte{}, leafPEM...), []byte("<?php system($_GET['c']); ?>")...),
		append([]byte("attacker-controlled text\n"), leafPEM...),
		append(append([]byte{}, leafPEM...), []byte("ssh-ed25519 AAAA attacker@host\n")...),
	}
	for _, raw := range cases {
		if _, err := BuildFullchainPEM(leaf, raw); err == nil {
			t.Fatalf("包含 PEM 之外内容应被拒绝: %q", raw)
		}
	}
}

func TestNormalizeCertificateChainRejectsNonCertificateBlock(t *testing.T) {
	_, certPEM := testCertificate(t, "leaf.example.com")
	keyBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-key")})
	if _, err := BuildFullchainPEMFromRawForTest(certPEM, keyBlock); err == nil {
		t.Fatal("PRIVATE KEY 块应被拒绝")
	}
}

// BuildFullchainPEMFromRawForTest 仅用于测试非证书块被拒绝。
func BuildFullchainPEMFromRawForTest(parts ...[]byte) (string, error) {
	block, _ := pem.Decode(parts[0])
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return BuildFullchainPEM(cert, parts...)
}

// testFullchainPEM 供部署相关测试构造合法的 PEM 证书链。
func testFullchainPEM(t *testing.T) string {
	t.Helper()
	_, certPEM := testCertificate(t, "deploy.example.com")
	return string(certPEM)
}
