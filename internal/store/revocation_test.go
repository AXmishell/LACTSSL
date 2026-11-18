package store

import (
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// TestRevocationSurvivesCertificateDeletion 验证吊销记录独立于证书行：
// 删除已吊销证书后，CRL/OCSP 仍能查到该序列号。
func TestRevocationSurvivesCertificateDeletion(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	now := time.Now().UTC()
	cert := &model.Certificate{
		PrimaryDomain: "revoked.example.com",
		Domains:       []string{"revoked.example.com"},
		PrivateCAID:   7,
		Serial:        "ABCDEF",
		CertPEM:       "CERT",
		KeyPEM:        "KEY",
		FullchainPEM:  "CHAIN",
		NotBefore:     now.Add(-time.Hour),
		NotAfter:      now.Add(24 * time.Hour),
		Status:        "valid",
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeCertificate(cert.ID, 1, now); err != nil {
		t.Fatal(err)
	}

	entries, err := st.ListRevocations(7)
	if err != nil || len(entries) != 1 || entries[0].Serial != "ABCDEF" {
		t.Fatalf("吊销记录应已写入: %+v, %v", entries, err)
	}
	if entry, found, err := st.GetRevocation(7, "abcdef"); err != nil || !found || entry.Reason != 1 {
		t.Fatalf("应按大小写不敏感查询到吊销记录: %+v, %v, %v", entry, found, err)
	}

	if err := st.DeleteCertificate(cert.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCertificateBySerial(7, "ABCDEF"); err == nil {
		t.Fatal("证书行应已删除")
	}
	entries, err = st.ListRevocations(7)
	if err != nil || len(entries) != 1 {
		t.Fatalf("删除证书后吊销记录必须保留: %+v, %v", entries, err)
	}
	if _, found, err := st.GetRevocation(7, "ABCDEF"); err != nil || !found {
		t.Fatalf("删除证书后仍应按序列号查到吊销记录: %v, %v", found, err)
	}
}
