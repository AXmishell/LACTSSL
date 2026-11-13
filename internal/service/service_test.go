package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestDeployLocal(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ssl", "fullchain.pem")
	keyPath := filepath.Join(dir, "ssl", "private.key")
	marker := filepath.Join(dir, "reloaded.txt")

	cert := &model.Certificate{
		FullchainPEM: "CERT-CONTENT",
		KeyPEM:       "KEY-CONTENT",
	}
	cfg := map[string]string{
		"cert_path":      certPath,
		"key_path":       keyPath,
		"reload_command": "echo reloaded > " + marker,
	}
	if err := deployLocal(cert, cfg); err != nil {
		t.Fatal(err)
	}
	certData, err := os.ReadFile(certPath)
	if err != nil || string(certData) != "CERT-CONTENT" {
		t.Fatalf("证书写入不正确: %s, %v", certData, err)
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil || string(keyData) != "KEY-CONTENT" {
		t.Fatalf("私钥写入不正确: %s, %v", keyData, err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("私钥权限不正确: %v", info.Mode().Perm())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("重载命令未执行")
	}
}

func TestNextRenewTime(t *testing.T) {
	notAfter := time.Now().UTC().Add(90 * 24 * time.Hour)
	next := NextRenewTime(notAfter, 30, true)
	if next == nil {
		t.Fatal("自动续期应返回下次执行时间")
	}
	want := notAfter.AddDate(0, 0, -30)
	if next.Sub(want) > time.Minute || want.Sub(*next) > time.Minute {
		t.Fatalf("续期时间计算不正确: %v", next)
	}
	if NextRenewTime(notAfter, 30, false) != nil {
		t.Fatal("关闭自动续期后不应安排下次执行")
	}
}

func TestCAInfoLitessl(t *testing.T) {
	ca, ok := CAInfo("litessl")
	if !ok {
		t.Fatal("未找到 LiteSSL CA")
	}
	if ca.URL != "https://acme.litessl.com/acme/v2/directory" {
		t.Fatalf("LiteSSL ACME 地址不正确: %s", ca.URL)
	}
	if !ca.EAB {
		t.Fatal("LiteSSL 需要 EAB 凭据")
	}
}

func TestNormalizeEABKey(t *testing.T) {
	got := normalizeEABKey(" ab+c/def== ")
	if got != "ab-c_def" {
		t.Fatalf("EAB 密钥归一化不正确: %s", got)
	}
}

func TestSplitConfigList(t *testing.T) {
	got := splitConfigList("a.com, b.com;\nc.com")
	if len(got) != 3 || got[0] != "a.com" || got[2] != "c.com" {
		t.Fatalf("列表解析不正确: %v", got)
	}
}

func TestNewDNSProviderUnknown(t *testing.T) {
	if _, _, err := newDNSProvider("unknown", nil); err == nil {
		t.Fatal("未知 DNS 服务商应返回错误")
	}
}

func TestACMECAValueRoundTrip(t *testing.T) {
	value := ACMECAValue(42)
	if value != "acme-ca:42" {
		t.Fatalf("unexpected value: %s", value)
	}
	id, ok := ParseACMECAValue(value)
	if !ok || id != 42 {
		t.Fatalf("parse failed: %d %v", id, ok)
	}
	for _, invalid := range []string{"letsencrypt", "acme-ca:", "acme-ca:0", "private-ca:42"} {
		if _, ok := ParseACMECAValue(invalid); ok {
			t.Fatalf("should not parse %q", invalid)
		}
	}
	def := CADefFromACMECA(model.ACMECA{ID: 7, Name: "Custom", DirectoryURL: "https://ca.example.com/dir", Protocol: "v1", EABRequired: false})
	if def.Value != "acme-ca:7" || def.Protocol != "v1" || !def.Custom || def.URL != "https://ca.example.com/dir" {
		t.Fatalf("unexpected CADef: %+v", def)
	}
}
