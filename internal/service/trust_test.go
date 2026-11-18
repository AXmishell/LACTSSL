package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestTrustedPEMsIncludesImportedAndPrivateCAs(t *testing.T) {
	m := newTestManager(t)
	privateCA, err := GeneratePrivateCA(PrivateCARequest{Name: "Internal Root", CommonName: "Internal Root CA", KeyType: "ecdsa", ValidDays: 30})
	if err != nil {
		t.Fatalf("生成私有 CA 失败: %v", err)
	}
	if err := m.Store.CreatePrivateCA(privateCA); err != nil {
		t.Fatalf("保存私有 CA 失败: %v", err)
	}
	imported := &model.TrustedCA{
		Name:        "Imported Root",
		Subject:     "CN=Imported Root",
		Issuer:      "CN=Imported Root",
		Fingerprint: "TESTFINGERPRINT",
		CertPEM:     privateCA.CertPEM,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
	}
	if err := m.Store.CreateTrustedCAs([]*model.TrustedCA{imported}); err != nil {
		t.Fatalf("保存导入 CA 失败: %v", err)
	}

	pems := m.TrustedPEMs()
	if len(pems) != 2 {
		t.Fatalf("信任链应包含导入 CA 与私有 CA，实际 %d", len(pems))
	}
	dir := t.TempDir()
	path, err := m.SyncTrustBundle(dir)
	if err != nil {
		t.Fatalf("同步信任链失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), strings.TrimSpace(privateCA.CertPEM)) {
		t.Fatal("信任链文件未包含内部 CA 证书")
	}
	if filepath.Base(path) != "trusted-ca-bundle.pem" {
		t.Fatalf("信任链文件名不正确: %s", path)
	}
}
