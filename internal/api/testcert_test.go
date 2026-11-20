package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// testCertificateChainPEM 生成一张自签名证书，供部署测试使用。
// 部署路径现在会校验 FullchainPEM 只包含 PEM CERTIFICATE 块。
func testCertificateChainPEM(t *testing.T) (certPEM string, fullchainPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "deploy.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"deploy.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "CERTIFICATE", Bytes: der}
	value := string(pem.EncodeToMemory(block))
	return value, value
}
