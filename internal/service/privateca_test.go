package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// TestImportWeakPrivateCARejected 验证导入低于现代安全强度的 CA 私钥会被拒绝。
func TestImportWeakPrivateCARejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Weak CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if _, err := ParseImportedPrivateCA("Weak CA", certPEM, keyPEM); err == nil {
		t.Fatal("1024 位 RSA CA 私钥应被拒绝")
	}
}

func TestGeneratePrivateCA(t *testing.T) {
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:         "Internal Root CA",
		CommonName:   "LACTSSL Internal Root CA",
		Organization: "LACTSSL",
		Country:      "CN",
		KeyType:      "ecdsa",
		ValidDays:    365,
	})
	if err != nil {
		t.Fatalf("生成私有 CA 失败: %v", err)
	}
	if ca.Source != "generated" || ca.KeyType != "ecdsa" {
		t.Fatalf("私有 CA 元数据不正确: %+v", ca)
	}
	if ca.CAType != "root" || ca.PathLen != 1 {
		t.Fatalf("根 CA 类型或路径长度不正确: %s/%d", ca.CAType, ca.PathLen)
	}
	if ca.KeyPEM == "" || ca.CertPEM == "" {
		t.Fatal("私有 CA 证书或私钥为空")
	}

	block, _ := pem.Decode([]byte(ca.CertPEM))
	if block == nil {
		t.Fatal("无法解析生成的证书 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("解析生成的证书失败: %v", err)
	}
	if !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatal("生成的证书不是可签发证书的 CA")
	}
	if cert.Subject.CommonName != "LACTSSL Internal Root CA" {
		t.Fatalf("通用名称不正确: %s", cert.Subject.CommonName)
	}
	if len(cert.Subject.Country) != 1 || cert.Subject.Country[0] != "CN" {
		t.Fatalf("国家代码不正确: %v", cert.Subject.Country)
	}
	if cert.NotAfter.Before(time.Now().Add(360 * 24 * time.Hour)) {
		t.Fatalf("有效期不正确: %v", cert.NotAfter)
	}

	privateKey, err := parsePrivateKey(ca.KeyPEM)
	if err != nil {
		t.Fatalf("解析生成的私钥失败: %v", err)
	}
	if !publicKeysEqual(cert.PublicKey, publicKey(privateKey)) {
		t.Fatal("生成的证书与私钥不匹配")
	}
}

func TestParseImportedPrivateCA(t *testing.T) {
	generated, err := GeneratePrivateCA(PrivateCARequest{Name: "Imported CA", KeyType: "rsa2048", ValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImportedPrivateCA("外部根 CA", generated.CertPEM, generated.KeyPEM)
	if err != nil {
		t.Fatalf("导入私有 CA 失败: %v", err)
	}
	if imported.Source != "imported" {
		t.Fatalf("导入来源标记不正确: %s", imported.Source)
	}
	if imported.KeyType != "rsa2048" {
		t.Fatalf("导入私钥类型不正确: %s", imported.KeyType)
	}
	if imported.CommonName != "Imported CA" {
		t.Fatalf("导入通用名称不正确: %s", imported.CommonName)
	}
	if imported.CAType != "root" || imported.PathLen != 1 {
		t.Fatalf("导入根 CA 类型或路径长度不正确: %s/%d", imported.CAType, imported.PathLen)
	}
}

func TestParseImportedIntermediateCA(t *testing.T) {
	root, err := GeneratePrivateCA(PrivateCARequest{Name: "Root CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	childPathLen := 0
	child, err := GeneratePrivateCA(PrivateCARequest{
		Name:          "Issuing CA",
		KeyType:       "ecdsa",
		ValidDays:     30,
		ParentCertPEM: root.CertPEM,
		ParentKeyPEM:  root.KeyPEM,
		PathLen:       &childPathLen,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImportedPrivateCA("Imported Issuing CA", child.CertPEM, child.KeyPEM)
	if err != nil {
		t.Fatalf("导入中间 CA 失败: %v", err)
	}
	if imported.CAType != "intermediate" || imported.PathLen != 0 {
		t.Fatalf("导入中间 CA 类型或路径长度不正确: %s/%d", imported.CAType, imported.PathLen)
	}
	if err := ValidateChildCA(imported.CertPEM, root.CertPEM); err != nil {
		t.Fatalf("校验中间 CA 与上级 CA 关系失败: %v", err)
	}
	other, err := GeneratePrivateCA(PrivateCARequest{Name: "Other Root", KeyType: "ecdsa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChildCA(imported.CertPEM, other.CertPEM); err == nil {
		t.Fatal("中间 CA 与不匹配的上级 CA 应校验失败")
	}
}

func TestParseImportedPrivateCASelectsMatchingCertFromChain(t *testing.T) {
	first, err := GeneratePrivateCA(PrivateCARequest{Name: "CA One", KeyType: "ecdsa"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePrivateCA(PrivateCARequest{Name: "CA Two", KeyType: "ecdsa"})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImportedPrivateCA("Chain", first.CertPEM+second.CertPEM, second.KeyPEM)
	if err != nil {
		t.Fatalf("从证书链中导入匹配的 CA 失败: %v", err)
	}
	if imported.CommonName != "CA Two" {
		t.Fatalf("应选择与私钥匹配的证书，实际为: %s", imported.CommonName)
	}
}

func TestParseImportedPrivateCARejectsMismatch(t *testing.T) {
	first, err := GeneratePrivateCA(PrivateCARequest{Name: "CA One", KeyType: "ecdsa"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePrivateCA(PrivateCARequest{Name: "CA Two", KeyType: "ecdsa"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseImportedPrivateCA("Mismatch", first.CertPEM, second.KeyPEM); err == nil {
		t.Fatal("证书与私钥不匹配时应返回错误")
	}
}

func TestGeneratePrivateCAValidatesParams(t *testing.T) {
	if _, err := GeneratePrivateCA(PrivateCARequest{Name: "", KeyType: "ecdsa"}); err == nil {
		t.Fatal("缺少名称时应返回错误")
	}
	if _, err := GeneratePrivateCA(PrivateCARequest{Name: "CA", Country: "CHN", KeyType: "ecdsa"}); err == nil {
		t.Fatal("国家代码不是两位字母时应返回错误")
	}
	if _, err := GeneratePrivateCA(PrivateCARequest{Name: "CA", KeyType: "dsa"}); err == nil {
		t.Fatal("不支持的私钥类型应返回错误")
	}
	if _, err := GeneratePrivateCA(PrivateCARequest{Name: "CA", KeyType: "ecdsa", ValidDays: 40000}); err == nil {
		t.Fatal("有效期超出范围时应返回错误")
	}
}

func TestGeneratePrivateCAAdvancedOptions(t *testing.T) {
	pathLen := 0
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:               "Advanced CA",
		CommonName:         "Advanced Internal CA",
		Organization:       "LACTSSL",
		OrganizationalUnit: "Platform",
		Country:            "US",
		State:              "California",
		Locality:           "San Francisco",
		Email:              "pki@example.com",
		SerialNumber:       "0x10",
		KeyType:            "ecdsa",
		ValidDays:          30,
		PathLen:            &pathLen,
		KeyUsage:           []string{"digital_signature", "key_cert_sign", "crl_sign"},
		ExtKeyUsage:        []string{"server_auth", "client_auth"},
		DNSNames:           []string{"ca.internal.example.com"},
		IPAddresses:        []string{"10.0.0.1"},
		PermittedDNS:       []string{".internal.example.com"},
		ExcludedDNS:        []string{".evil.internal"},
		PermittedIPs:       []string{"10.0.0.0/8"},
		ExcludedIPs:        []string{"10.1.0.0/16"},
		CRLURLs:            []string{"https://pki.example.com/crl"},
		OCSPURLs:           []string{"https://pki.example.com/ocsp"},
		IssuerURL:          "https://pki.example.com/ca.crt",
		PolicyOIDs:         []string{"1.3.6.1.4.1.99999.1"},
		CPSURL:             "https://pki.example.com/cps",
	})
	if err != nil {
		t.Fatalf("生成高级私有 CA 失败: %v", err)
	}
	cert := parseTestCertificate(t, ca.CertPEM)
	if got := cert.SerialNumber.Text(16); got != "10" {
		t.Fatalf("自定义序列号未生效: %s", got)
	}
	if !cert.MaxPathLenZero || cert.MaxPathLen != 0 {
		t.Fatalf("路径长度约束不正确: %d/%v", cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.Subject.Province[0] != "California" || cert.Subject.Locality[0] != "San Francisco" {
		t.Fatalf("省/市主题字段不正确: %v %v", cert.Subject.Province, cert.Subject.Locality)
	}
	if cert.Subject.OrganizationalUnit[0] != "Platform" {
		t.Fatalf("组织单位不正确: %v", cert.Subject.OrganizationalUnit)
	}
	if len(cert.EmailAddresses) != 1 || cert.EmailAddresses[0] != "pki@example.com" {
		t.Fatalf("电子邮件 SAN 不正确: %v", cert.EmailAddresses)
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.KeyUsage&x509.KeyUsageCRLSign == 0 {
		t.Fatalf("密钥用法不正确: %v", cert.KeyUsage)
	}
	if len(cert.ExtKeyUsage) != 2 {
		t.Fatalf("扩展密钥用法不正确: %v", cert.ExtKeyUsage)
	}
	if len(cert.DNSNames) != 1 || len(cert.IPAddresses) != 1 {
		t.Fatalf("SAN 不正确: %v %v", cert.DNSNames, cert.IPAddresses)
	}
	if len(cert.PermittedDNSDomains) != 1 || len(cert.ExcludedIPRanges) != 1 {
		t.Fatalf("名称约束不正确: %v %v", cert.PermittedDNSDomains, cert.ExcludedIPRanges)
	}
	if len(cert.CRLDistributionPoints) != 1 || len(cert.OCSPServer) != 1 || len(cert.IssuingCertificateURL) != 1 {
		t.Fatalf("PKI 分发地址不正确: %v %v %v", cert.CRLDistributionPoints, cert.OCSPServer, cert.IssuingCertificateURL)
	}
	foundPolicy := false
	for _, extension := range cert.Extensions {
		if extension.Id.Equal(oidCertificatePolicies) {
			foundPolicy = true
			break
		}
	}
	if !foundPolicy {
		t.Fatal("证书策略扩展未写入")
	}
}

func TestGeneratePrivateCASubCA(t *testing.T) {
	rootPathLen := 1
	root, err := GeneratePrivateCA(PrivateCARequest{
		Name:       "Root CA",
		CommonName: "LACTSSL Root CA",
		KeyType:    "ecdsa",
		ValidDays:  365,
		PathLen:    &rootPathLen,
	})
	if err != nil {
		t.Fatal(err)
	}
	childPathLen := 1
	child, err := GeneratePrivateCA(PrivateCARequest{
		Name:          "Issuing CA",
		CommonName:    "LACTSSL Issuing CA",
		KeyType:       "ecdsa",
		ValidDays:     30,
		ParentID:      7,
		ParentCertPEM: root.CertPEM,
		ParentKeyPEM:  root.KeyPEM,
		PathLen:       &childPathLen,
	})
	if err != nil {
		t.Fatalf("生成下级 CA 失败: %v", err)
	}
	rootCert := parseTestCertificate(t, root.CertPEM)
	childCert := parseTestCertificate(t, child.CertPEM)
	if child.ParentID != 7 {
		t.Fatalf("上级 CA 关联不正确: %d", child.ParentID)
	}
	if child.CAType != "intermediate" || child.PathLen != 0 {
		t.Fatalf("下级 CA 类型或路径长度不正确: %s/%d", child.CAType, child.PathLen)
	}
	if childCert.Issuer.CommonName != rootCert.Subject.CommonName {
		t.Fatalf("下级 CA 颁发者不正确: %s", childCert.Issuer.CommonName)
	}
	if childCert.Subject.CommonName != "LACTSSL Issuing CA" {
		t.Fatalf("下级 CA 主题不正确: %s", childCert.Subject.CommonName)
	}
	if err := childCert.CheckSignatureFrom(rootCert); err != nil {
		t.Fatalf("下级 CA 签名校验失败: %v", err)
	}
	if !bytesEqual(childCert.AuthorityKeyId, rootCert.SubjectKeyId) {
		t.Fatalf("AKI 未指向上级 CA: %v != %v", childCert.AuthorityKeyId, rootCert.SubjectKeyId)
	}
	if !childCert.MaxPathLenZero || childCert.MaxPathLen != 0 {
		t.Fatalf("下级 CA 路径长度未收敛到上级限制: %d/%v", childCert.MaxPathLen, childCert.MaxPathLenZero)
	}
	if childCert.NotAfter.After(rootCert.NotAfter) {
		t.Fatalf("下级 CA 有效期超过上级 CA: %v > %v", childCert.NotAfter, rootCert.NotAfter)
	}
}

func parseTestCertificate(t *testing.T, value string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		t.Fatal("无法解析证书 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func bytesEqual(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
