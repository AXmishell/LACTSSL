package service

import (
	"crypto/x509"
	"testing"
	"time"
)

func TestPrivateCAValueRoundTrip(t *testing.T) {
	value := PrivateCAValue(42)
	if value != "private-ca:42" {
		t.Fatalf("私有 CA 引用格式不正确: %s", value)
	}
	id, ok := ParsePrivateCAValue(value)
	if !ok || id != 42 {
		t.Fatalf("解析私有 CA 引用失败: %d %v", id, ok)
	}
	if _, ok := ParsePrivateCAValue("letsencrypt"); ok {
		t.Fatal("公共 CA 不应被识别为私有 CA")
	}
	if _, ok := ParsePrivateCAValue("private-ca:0"); ok {
		t.Fatal("无效的私有 CA ID 应返回 false")
	}
}

func TestIssuePrivateCertificate(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:       "Leaf Root CA",
		CommonName: "Leaf Root CA",
		KeyType:    "ecdsa",
		ValidDays:  365,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := IssuePrivateCertificate(ca, PrivateCertRequest{
		Domains:   []string{"example.com", "www.example.com"},
		KeyType:   "ecdsa",
		ValidDays: 90,
	})
	if err != nil {
		t.Fatalf("使用私有 CA 签发证书失败: %v", err)
	}
	leaf := parseTestCertificate(t, result.CertPEM)
	caCert := parseTestCertificate(t, ca.CertPEM)
	if leaf.IsCA {
		t.Fatal("终端证书不应标记为 CA")
	}
	if len(leaf.DNSNames) != 2 || leaf.DNSNames[0] != "example.com" {
		t.Fatalf("证书 SAN 不正确: %v", leaf.DNSNames)
	}
	if err := leaf.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("终端证书签名校验失败: %v", err)
	}
	if leaf.NotAfter.After(caCert.NotAfter) {
		t.Fatalf("终端证书有效期超过 CA: %v > %v", leaf.NotAfter, caCert.NotAfter)
	}
	if leaf.NotAfter.Before(time.Now().Add(80 * 24 * time.Hour)) {
		t.Fatalf("终端证书有效期不正确: %v", leaf.NotAfter)
	}
	if result.Issuer != "Leaf Root CA" {
		t.Fatalf("证书颁发者不正确: %s", result.Issuer)
	}
	privateKey, err := parsePrivateKey(result.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !publicKeysEqual(leaf.PublicKey, publicKey(privateKey)) {
		t.Fatal("终端证书与私钥不匹配")
	}
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			return
		}
	}
	t.Fatal("终端证书缺少 serverAuth 用途")
}

func TestIssuePrivateCertificateRejectsRestrictiveCA(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:        "Client Only CA",
		KeyType:     "ecdsa",
		ValidDays:   365,
		ExtKeyUsage: []string{"client_auth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssuePrivateCertificate(ca, PrivateCertRequest{Domains: []string{"example.com"}, KeyType: "ecdsa"}); err == nil {
		t.Fatal("仅允许 clientAuth 的 CA 不应签发服务器证书")
	}
}
