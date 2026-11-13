package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
)

// selfSignedPEM 用于生成测试证书链。
func selfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(123456789),
		Subject: pkix.Name{
			CommonName:   "example.com",
			Organization: []string{"LACTSSL Test"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              []string{"example.com", "www.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestAnalyzePEM(t *testing.T) {
	analysis, err := analyzePEM(selfSignedPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	if analysis.PublicKeyAlgorithm != "ECDSA" || analysis.KeyBits != 256 {
		t.Fatalf("公钥信息不正确: %+v", analysis)
	}
	if len(analysis.DNSNames) != 2 {
		t.Fatalf("SAN 数量不正确: %v", analysis.DNSNames)
	}
	if analysis.DaysLeft < 80 {
		t.Fatalf("剩余天数计算不正确: %d", analysis.DaysLeft)
	}
	if analysis.Score >= 100 {
		t.Fatal("自签名证书不应得到满分")
	}
	found := false
	for _, issue := range analysis.Issues {
		if issue == "证书为自签名，浏览器可能不信任" {
			found = true
		}
	}
	if !found {
		t.Fatalf("未识别出自签名问题: %v", analysis.Issues)
	}
}

func TestParsePEMChainInvalid(t *testing.T) {
	if _, err := parsePEMChain("not a pem"); err == nil {
		t.Fatal("无效内容应返回错误")
	}
}

func TestCleanupCertificatesEndpoint(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	certToken := secondaryVerifyToken(t, router, session, secondaryActionCertificateWrite, "password1234", "")

	now := time.Now().UTC()
	expired := &model.Certificate{PrimaryDomain: "expired.example.com", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(-time.Minute)}
	valid := &model.Certificate{PrimaryDomain: "valid.example.com", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour)}
	for _, cert := range []*model.Certificate{expired, valid} {
		if err := st.CreateCertificate(cert); err != nil {
			t.Fatal(err)
		}
	}

	w := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/certificates/cleanup/preview",
		`{"scope":"expired","expired_days":1}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count":0`) {
		t.Fatalf("过期天数预览不正确: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetCertificate(expired.ID); err != nil {
		t.Fatalf("预览不应删除证书: %v", err)
	}

	w = sessionRequest(t, router, http.MethodPost, "/secret-entry/api/certificates/cleanup/preview",
		`{"scope":"expired"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count":1`) {
		t.Fatalf("清理预览不正确: %d %s", w.Code, w.Body.String())
	}

	w = sessionRequest(t, router, http.MethodPost, "/secret-entry/api/certificates/cleanup",
		`{"scope":"expired"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: certToken})
	if w.Code != http.StatusOK {
		t.Fatalf("清理过期证书失败: %d %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"deleted":1`) {
		t.Fatalf("清理数量不正确: %s", body)
	}
	if _, err := st.GetCertificate(expired.ID); err == nil {
		t.Fatal("过期证书未被删除")
	}
	if _, err := st.GetCertificate(valid.ID); err != nil {
		t.Fatalf("有效证书不应被删除: %v", err)
	}

	w = sessionRequest(t, router, http.MethodPost, "/secret-entry/api/certificates/cleanup",
		`{"scope":"all"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: certToken})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Fatalf("清理全部证书失败: %d %s", w.Code, w.Body.String())
	}
	if certs, err := st.ListCertificates(); err != nil || len(certs) != 0 {
		t.Fatalf("清理全部证书后仍有记录: %v %+v", err, certs)
	}
}
