package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newCSRServiceStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "csr-private.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("csr-private-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(conn, cipher)
}

func testInternalCSRPEM(t *testing.T, domains ...string) (string, *CSRInfo) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	value := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	info, err := ParseCSRInfoForPrivateCA(value)
	if err != nil {
		t.Fatal(err)
	}
	return value, info
}

func TestParseCSRInfoForPrivateCAAllowsInternalNames(t *testing.T) {
	value, info := testInternalCSRPEM(t, "app.internal", "api.internal")
	if len(info.DNSNames) != 2 || info.PrimaryDomain != "app.internal" {
		t.Fatalf("私有 CA CSR 解析结果不正确: %+v", info)
	}
	if _, err := ParseCSRInfo(value); err == nil {
		t.Fatal("公共 CA 路径仍然应拒绝内部域名")
	}
}

func TestRunCSRRequestWithPrivateCA(t *testing.T) {
	st := newCSRServiceStore(t)
	manager := New(st)
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:      "CSR Internal CA",
		KeyType:   "ecdsa",
		ValidDays: 365,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}

	csrPEM, info := testInternalCSRPEM(t, "app.internal", "api.internal")
	expires := time.Now().UTC().Add(time.Hour)
	req := &model.CSRRequest{
		PublicID:      "csr_private_test",
		CSRPEM:        csrPEM,
		CSRHash:       info.SHA256,
		Identifiers:   info.DNSNames,
		PrimaryDomain: info.PrimaryDomain,
		CA:            PrivateCAValue(ca.ID),
		Challenge:     "private",
		Status:        model.CSRStatusPending,
		ExpiresAt:     &expires,
	}
	if err := st.CreateCSRRequest(req); err != nil {
		t.Fatal(err)
	}
	if err := manager.runCSRRequest(context.Background(), req.ID); err != nil {
		t.Fatalf("处理私有 CA CSR 申请失败: %v", err)
	}

	loaded, err := st.GetCSRRequest(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != model.CSRStatusIssued || loaded.CertificateID <= 0 {
		t.Fatalf("私有 CA 申请未成功签发: %+v", loaded)
	}
	cert, err := st.GetCertificate(loaded.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if cert.PrivateCAID != ca.ID {
		t.Fatalf("证书未关联私有 CA: %+v", cert)
	}
	if cert.KeyAvailable {
		t.Fatal("客户端 CSR 签发的证书不应保存私钥")
	}
	if len(cert.Domains) != 2 || cert.Domains[0] != "app.internal" {
		t.Fatalf("证书域名不正确: %v", cert.Domains)
	}
	leaf := parseTestCertificate(t, cert.CertPEM)
	if err := leaf.CheckSignatureFrom(parseTestCertificate(t, ca.CertPEM)); err != nil {
		t.Fatalf("证书签发链不正确: %v", err)
	}
	if len(leaf.DNSNames) != 2 {
		t.Fatalf("证书 SAN 不正确: %v", leaf.DNSNames)
	}
}
