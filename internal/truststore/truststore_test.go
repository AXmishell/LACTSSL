package truststore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeTestCertificate(t *testing.T, template, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	if parent == nil {
		parent = template
	}
	if parentKey == nil {
		parentKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("创建测试证书失败: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析测试证书失败: %v", err)
	}
	return cert
}

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成测试私钥失败: %v", err)
	}
	return key
}

func caTemplate(cn string, notAfter time.Time) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"LACTSSL Test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func TestParseCAPEMAndTrustChain(t *testing.T) {
	caKey := testKey(t)
	ca := makeTestCertificate(t, caTemplate("Internal Root CA", time.Now().Add(24*time.Hour)), nil, caKey, nil)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))

	parsed, err := ParseCAPEM(caPEM)
	if err != nil {
		t.Fatalf("解析 CA 失败: %v", err)
	}
	if len(parsed) != 1 || !parsed[0].IsCA {
		t.Fatalf("解析结果不正确: %+v", parsed)
	}
	item := CAFromCertificate(parsed[0], "")
	if item.Name != "Internal Root CA" || item.Fingerprint == "" || item.CertPEM == "" {
		t.Fatalf("CA 记录字段不完整: %+v", item)
	}
	if !strings.Contains(item.Subject, "Internal Root CA") {
		t.Fatalf("主题不正确: %s", item.Subject)
	}

	// 由该内部 CA 签发的服务端证书应能通过合并后的根证书池校验。
	leafKey := testKey(t)
	leaf := makeTestCertificate(t, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "internal.example.com"},
		DNSNames:     []string{"internal.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, leafKey, caKey)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     CertPool([]string{caPEM}),
		DNSName:   "internal.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("内部 CA 签发的证书应受信任: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     x509.NewCertPool(),
		DNSName:   "internal.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Fatal("空根证书池不应信任内部证书")
	}

	bundle := BundlePEM([]string{caPEM})
	if !strings.Contains(string(bundle), "BEGIN CERTIFICATE") {
		t.Fatal("信任链应包含 PEM 证书")
	}
	path, err := SyncBundle(t.TempDir(), []string{caPEM})
	if err != nil {
		t.Fatalf("写入信任链失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取信任链失败: %v", err)
	}
	if !strings.Contains(string(data), caPEM) {
		t.Fatal("信任链文件未包含导入的 CA")
	}
	if filepath.Base(path) != BundleFileName {
		t.Fatalf("信任链文件名不正确: %s", path)
	}
}

func TestParseCAPEMRejectsLeafAndExpired(t *testing.T) {
	key := testKey(t)
	leafKey := testKey(t)
	ca := makeTestCertificate(t, caTemplate("Root CA", time.Now().Add(24*time.Hour)), nil, key, nil)
	leaf := makeTestCertificate(t, &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "leaf.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}, ca, leafKey, key)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	if _, err := ParseCAPEM(string(leafPEM)); err == nil {
		t.Fatal("普通叶证书不应被当作 CA 导入")
	}

	expiredKey := testKey(t)
	expired := makeTestCertificate(t, caTemplate("Expired CA", time.Now().Add(-time.Hour)), nil, expiredKey, nil)
	expiredPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: expired.Raw})
	if _, err := ParseCAPEM(string(expiredPEM)); err == nil {
		t.Fatal("过期 CA 不应被导入")
	}

	if _, err := ParseCAPEM(""); err == nil {
		t.Fatal("空内容应报错")
	}
	if _, err := ParseCAPEM("not a certificate"); err == nil {
		t.Fatal("无效 PEM 应报错")
	}
}
