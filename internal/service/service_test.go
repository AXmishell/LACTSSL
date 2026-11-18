package service

import (
	"context"
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
	if err := deployLocal(context.Background(), nil, cert, cfg); err != nil {
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

// TestRunCommandTimeoutKillsProcessGroup 验证超时会结束整个进程组，
// 而不是只杀掉 sh 让子进程继续写文件。
func TestRunCommandTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "leaked.txt")
	script := "sh -c 'sleep 3; echo leaked > " + marker + "' & wait"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := runCommand(ctx, script); err == nil {
		t.Fatal("超时命令应返回错误")
	}
	// 给被清理的子进程一点时间；若进程组未被结束，3 秒后 marker 会出现。
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("超时后子进程仍继续运行并写入了文件")
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

// TestNextRenewTimeShortLived 覆盖证书寿命小于 renewDays 的场景。
// 例如 6 天短周期证书 + 默认 30 天提前量，不能排到 1 小时后导致每小时重复签发。
func TestNextRenewTimeShortLived(t *testing.T) {
	now := time.Now().UTC()
	notAfter := now.Add(6 * 24 * time.Hour)
	next := NextRenewTime(notAfter, 30, true)
	if next == nil {
		t.Fatal("短寿命证书也应安排续期")
	}
	want := notAfter.Add(-2 * 24 * time.Hour) // 寿命的 2/3 处
	if next.Sub(want) > time.Minute || want.Sub(*next) > time.Minute {
		t.Fatalf("短寿命证书续期时间不正确: want %v, got %v", want, *next)
	}
	if time.Until(*next) < 23*time.Hour {
		t.Fatalf("续期时间过近，可能造成重复签发: %v", time.Until(*next))
	}

	// renewDays 恰好等于寿命时同样不能排到 1 小时后。
	equal := NextRenewTime(now.Add(30*24*time.Hour), 30, true)
	if equal == nil || time.Until(*equal) < 24*time.Hour {
		t.Fatalf("renewDays 等于寿命时的续期时间不正确: %v", equal)
	}

	// 已过期证书应立即（至少 1 分钟后）重试，而不是进入忙循环。
	expired := NextRenewTime(now.Add(-48*time.Hour), 30, true)
	if expired == nil {
		t.Fatal("过期证书也应安排续期")
	}
	if d := time.Until(*expired); d < 30*time.Second || d > 2*time.Minute {
		t.Fatalf("过期证书续期时间不正确: %v", d)
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
