package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"net"
	"strings"
	"testing"
)

func createTestCSR(t *testing.T, key any, cn string, dnsNames []string, ips []net.IP, extra []pkix.Extension) string {
	t.Helper()
	template := &x509.CertificateRequest{
		Subject:         pkix.Name{CommonName: cn},
		DNSNames:        dnsNames,
		IPAddresses:     ips,
		ExtraExtensions: extra,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestParseCSRInfo(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	value := createTestCSR(t, key, "example.com", []string{"example.com", "www.example.com"}, nil, nil)
	info, err := ParseCSRInfo(value)
	if err != nil {
		t.Fatalf("有效 CSR 被拒绝: %v", err)
	}
	if info.PrimaryDomain != "example.com" {
		t.Fatalf("主域名不正确: %s", info.PrimaryDomain)
	}
	if len(info.DNSNames) != 2 || info.DNSNames[1] != "www.example.com" {
		t.Fatalf("SAN 解析不正确: %v", info.DNSNames)
	}
	if info.SHA256 == "" || !strings.Contains(info.PEM, "BEGIN CERTIFICATE REQUEST") {
		t.Fatal("CSR 指纹或 PEM 未生成")
	}
}

func TestParseCSRInfoRejectsInvalidCSR(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	// CN 不在 SAN 中：lego 会把 CN 当作域名提交，必须提前拒绝。
	cnNotInSAN := createTestCSR(t, key, "example.com", []string{"www.example.com"}, nil, nil)
	if _, err := ParseCSRInfo(cnNotInSAN); err == nil {
		t.Fatal("CN 不在 SAN 中时应拒绝")
	}

	// 大写域名不是规范形式，服务端无法修改已签名 CSR。
	uppercase := createTestCSR(t, key, "", []string{"Example.com"}, nil, nil)
	if _, err := ParseCSRInfo(uppercase); err == nil {
		t.Fatal("大写域名应拒绝")
	}

	// 内部域名不能向公共 CA 申请。
	internal := createTestCSR(t, key, "", []string{"db.internal"}, nil, nil)
	if _, err := ParseCSRInfo(internal); err == nil {
		t.Fatal("内部域名应拒绝")
	}

	// 第一阶段不支持 IP 证书。
	ipCSR := createTestCSR(t, key, "", []string{"example.com"}, []net.IP{net.ParseIP("192.0.2.1")}, nil)
	if _, err := ParseCSRInfo(ipCSR); err == nil {
		t.Fatal("IP SAN 应拒绝")
	}

	// CA:TRUE 的 CSR 不能作为终端证书申请。
	value, _ := asn1.Marshal(struct {
		IsCA bool `asn1:"optional"`
	}{IsCA: true})
	caCSR := createTestCSR(t, key, "", []string{"example.com"}, nil, []pkix.Extension{{
		Id:    asn1.ObjectIdentifier{2, 5, 29, 19},
		Value: value,
	}})
	if _, err := ParseCSRInfo(caCSR); err == nil {
		t.Fatal("CA:TRUE CSR 应拒绝")
	}

	// RSA 1024 低于公共 CA 的最低要求。
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	weak := createTestCSR(t, weakKey, "", []string{"example.com"}, nil, nil)
	if _, err := ParseCSRInfo(weak); err == nil {
		t.Fatal("弱 RSA 密钥应拒绝")
	}
}

func TestValidateCSRDomains(t *testing.T) {
	allowed := ParseAllowedDomains("example.com, *.api.example.com")
	if err := ValidateCSRDomains([]string{"example.com", "www.example.com", "a.b.api.example.com"}, allowed, false); err != nil {
		t.Fatalf("允许列表内域名被拒绝: %v", err)
	}
	if err := ValidateCSRDomains([]string{"other.com"}, allowed, false); err == nil {
		t.Fatal("允许列表外域名应拒绝")
	}
	if err := ValidateCSRDomains([]string{"*.example.com"}, allowed, false); err != nil {
		t.Fatalf("裸域允许列表应允许通配符: %v", err)
	}
	if err := ValidateCSRDomains([]string{"example.com"}, nil, false); err == nil {
		t.Fatal("空允许列表且未开启 allowAll 时应默认拒绝")
	}
	if err := ValidateCSRDomains([]string{"example.com"}, nil, true); err != nil {
		t.Fatalf("显式 allowAll 时应放行: %v", err)
	}
}

func TestValidateCSRResult(t *testing.T) {
	cert, keyPEM := makeTestCertificate(t, []string{"example.com", "www.example.com"}, nil)
	key, err := parsePrivateKey(string(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	csrValue := createTestCSR(t, key, "example.com", []string{"example.com", "www.example.com"}, nil, nil)
	info, err := ParseCSRInfo(csrValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCSRResult(info.CSR, cert); err != nil {
		t.Fatalf("匹配的证书被拒绝: %v", err)
	}

	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err := validateCSRResult(&x509.CertificateRequest{PublicKey: &otherKey.PublicKey, DNSNames: []string{"example.com", "www.example.com"}}, cert); err == nil {
		t.Fatal("公钥不匹配的证书应拒绝")
	}
	extra, _ := makeTestCertificate(t, []string{"example.com", "www.example.com", "extra.example.com"}, nil)
	if err := validateCSRResult(info.CSR, extra); err == nil {
		t.Fatal("包含额外 SAN 的证书应拒绝")
	}
}

func TestParseCSRInfoAcceptsBase64DER(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.CertificateRequest{Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if _, err := ParseCSRInfo(string(encoded)); err != nil {
		t.Fatalf("PEM CSR 解析失败: %v", err)
	}
	if _, err := ParseCSRInfo(base64.StdEncoding.EncodeToString(der)); err != nil {
		t.Fatalf("base64 DER CSR 解析失败: %v", err)
	}
	if _, err := ParseCSRInfo("not-a-csr"); err == nil {
		t.Fatal("非法 CSR 内容不应被接受")
	}
}
