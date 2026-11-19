package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const editableTestConfig = `# top comment
server:
  # server comment
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
  public_url: "https://old.example.com"
  session_persistence: false
data_dir: "data"
database:
  path: "custom.db"
custom_unknown:
  keep: true
`

func writeEditableTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareConfigUpdatePreservesCommentsAndReportsChanges(t *testing.T) {
	path := writeEditableTestConfig(t, editableTestConfig)
	revision, err := ConfigRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	publicURL := "https://new.example.com/"
	proxies := []string{"10.0.0.0/8", "127.0.0.1", "10.0.0.0/8"}
	port := 9090
	prepared, err := PrepareConfigUpdate(path, SetupConfigPatch{
		Server:         &SetupServerPatch{Port: &port},
		TrustedProxies: &proxies,
		PublicURL:      &publicURL,
	}, revision)
	if err != nil {
		t.Fatal(err)
	}
	content := string(prepared.Next)
	for _, want := range []string{
		"# top comment",
		"# server comment",
		"custom_unknown:",
		"keep: true",
		"public_url: https://new.example.com",
		"port: 9090",
		"10.0.0.0/8",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("更新后的配置缺少 %q:\n%s", want, content)
		}
	}
	if strings.Count(content, "10.0.0.0/8") != 1 {
		t.Fatalf("可信代理应去重:\n%s", content)
	}
	for _, want := range []string{"server.port", "trusted_proxies", "public_url"} {
		if !containsString(prepared.ChangedFields, want) {
			t.Fatalf("changed_fields 缺少 %q: %v", want, prepared.ChangedFields)
		}
	}
	if containsString(prepared.ChangedFields, "security.jwt_secret") {
		t.Fatalf("不应记录未修改的密钥字段: %v", prepared.ChangedFields)
	}
}

func TestPrepareConfigUpdateRejectsRevisionConflict(t *testing.T) {
	path := writeEditableTestConfig(t, editableTestConfig)
	_, err := PrepareConfigUpdate(path, SetupConfigPatch{}, "sha256:deadbeef")
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("旧版本保存应返回冲突: %v", err)
	}
}

func TestNormalizeTrustedProxies(t *testing.T) {
	values, err := NormalizeTrustedProxies([]string{" 127.0.0.1 ", "10.0.0.1/8", "::ffff:192.0.2.1", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "10.0.0.0/8", "192.0.2.1"}
	if len(values) != len(want) {
		t.Fatalf("规范化结果不正确: %v", values)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Fatalf("第 %d 项应为 %s，实际 %s", index, want[index], values[index])
		}
	}
	for _, invalid := range []string{"", "0.0.0.0/0", "::/0", "not-an-ip", "10.0.0.0/33"} {
		if _, err := NormalizeTrustedProxy(invalid); err == nil {
			t.Fatalf("应拒绝非法可信代理 %q", invalid)
		}
	}
}

func TestConfigBackupRestoreKeepsSecretsAndPaths(t *testing.T) {
	path := writeEditableTestConfig(t, editableTestConfig)
	dataDir := filepath.Dir(path)

	backup, err := CreateConfigBackup(path, dataDir, "manual", "before edit", "admin@example.com", "test")
	if err != nil {
		t.Fatal(err)
	}
	if backup.ID == "" || backup.SaltFingerprint == "" {
		t.Fatalf("快照元数据不完整: %+v", backup)
	}
	rawMeta, err := os.ReadFile(filepath.Join(ConfigBackupDir(dataDir), backup.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawMeta), "ssssssssssssssssssssssssssssssss") {
		t.Fatal("快照元数据不应包含 salt 明文")
	}

	publicURL := "https://changed.example.com"
	if _, err := UpdateEditableConfig(path, dataDir, SetupConfigPatch{PublicURL: &publicURL}, "", "before-save", "", "admin@example.com", "test"); err != nil {
		t.Fatal(err)
	}
	changed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changed), "https://changed.example.com") {
		t.Fatalf("更新未生效:\n%s", changed)
	}

	result, err := RestoreConfigBackup(path, dataDir, backup.ID, "admin@example.com", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Noop || len(result.ChangedFields) == 0 {
		t.Fatalf("恢复应产生变更: %+v", result)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(restored)
	for _, want := range []string{
		"https://old.example.com",
		`jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"`,
		`salt: "ssssssssssssssssssssssssssssssss"`,
		`data_dir: "data"`,
		`path: "custom.db"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("恢复后的配置缺少 %q:\n%s", want, content)
		}
	}

	infos, err := ListConfigBackups(path, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) == 0 {
		t.Fatal("快照列表不应为空")
	}
	if err := DeleteConfigBackup(dataDir, backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteConfigBackup(dataDir, backup.ID); !errors.Is(err, ErrConfigBackupNotFound) {
		t.Fatalf("重复删除应返回不存在: %v", err)
	}
}

func TestConfigBackupRejectsInvalidID(t *testing.T) {
	dataDir := t.TempDir()
	if err := DeleteConfigBackup(dataDir, "../../etc/passwd"); !errors.Is(err, ErrConfigBackupNotFound) {
		t.Fatalf("非法 ID 应被拒绝: %v", err)
	}
	if _, _, err := LoadConfigBackup(filepath.Join(dataDir, "config.yaml"), dataDir, "../config"); !errors.Is(err, ErrConfigBackupNotFound) {
		t.Fatalf("非法 ID 读取应被拒绝: %v", err)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestValidateFileRejectsUnsafeTrustedProxy(t *testing.T) {
	path := writeEditableTestConfig(t, `server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["0.0.0.0/0"]
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`)
	if err := ValidateFile(path); err == nil {
		t.Fatal("ValidateFile 应拒绝 /0 可信代理")
	}
}
