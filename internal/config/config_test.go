package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LACTSSL_CONFIG", "LACTSSL_DATA", "LACTSSL_ADDR", "LACTSSL_PORT", "LACTSSL_ENTRY", "LACTSSL_PUBLIC_URL", "LACTSSL_TRUSTED_PROXIES", "LACTSSL_ACME_ENABLED", "LACTSSL_ACME_ADDR", "LACTSSL_ACME_BASE_URL", "LACTSSL_ACME_TLS_CERT", "LACTSSL_ACME_TLS_KEY", "LACTSSL_ACME_MTLS_ENABLED", "LACTSSL_ACME_MTLS_CLIENT_CA"} {
		t.Setenv(key, "")
	}
}

func TestLoadCreatesConfigAndMigratesLegacySecret(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config", "config.yaml")
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(dataDir, "jwt.secret"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	t.Setenv("LACTSSL_DATA", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("默认监听地址不正确: %s", cfg.Addr)
	}
	if string(cfg.JWTSecret) != legacy || string(cfg.EncryptionKey) != legacy {
		t.Fatal("旧版密钥未迁移到配置文件")
	}
	if cfg.DBPath != filepath.Join(dataDir, "lactssl.db") {
		t.Fatalf("数据库路径不正确: %s", cfg.DBPath)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限不正确: %v", info.Mode().Perm())
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), legacy) {
		t.Fatal("配置文件未写入迁移后的密钥")
	}
}

func TestLoadFromYAMLAndEnvOverride(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: 9090
  trusted_proxies: ["127.0.0.1"]
data_dir: %q
security:
  entry: "from-file"
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Fatalf("YAML 监听地址未生效: %s", cfg.Addr)
	}
	if cfg.Entry != "from-file" {
		t.Fatalf("YAML 安全入口未生效: %s", cfg.Entry)
	}
	if string(cfg.JWTSecret) != "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj" || string(cfg.EncryptionKey) != "ssssssssssssssssssssssssssssssss" {
		t.Fatal("YAML 密钥未生效")
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("YAML 可信代理未生效: %v", cfg.TrustedProxies)
	}

	t.Setenv("LACTSSL_ADDR", ":9999")
	t.Setenv("LACTSSL_PORT", "7777")
	t.Setenv("LACTSSL_ENTRY", "from-env")
	t.Setenv("LACTSSL_TRUSTED_PROXIES", "10.0.0.0/8, ::1")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9999" {
		t.Fatalf("环境变量监听地址未覆盖 YAML: %s", cfg.Addr)
	}
	if cfg.Entry != "from-env" {
		t.Fatalf("环境变量安全入口未覆盖 YAML: %s", cfg.Entry)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "10.0.0.0/8" || cfg.TrustedProxies[1] != "::1" {
		t.Fatalf("环境变量可信代理未覆盖 YAML: %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsWeakSecrets(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "short"
  salt: "%s"
`, filepath.Join(dir, "data"), strings.Repeat("s", 32))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	if _, err := Load(); err == nil {
		t.Fatal("过短的 jwt_secret 应被拒绝")
	}
}

func TestLoadACMEConfig(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000/"
  tls:
    cert: "/tmp/acme.crt"
    key: "/tmp/acme.key"
  mtls:
    enabled: false
    client_ca: ""
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ACME.Enabled || cfg.ACME.Addr != ":14000" {
		t.Fatalf("ACME 监听配置未生效: %+v", cfg.ACME)
	}
	if cfg.ACME.BaseURL != "https://pki.example.com:14000" {
		t.Fatalf("ACME base_url 未规范化: %s", cfg.ACME.BaseURL)
	}
	if cfg.ACME.TLSCert != "/tmp/acme.crt" || cfg.ACME.TLSKey != "/tmp/acme.key" {
		t.Fatalf("ACME TLS 配置未生效: %+v", cfg.ACME)
	}

	t.Setenv("LACTSSL_ACME_ADDR", "127.0.0.1:15000")
	t.Setenv("LACTSSL_ACME_BASE_URL", "https://acme.example.com/")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.Addr != "127.0.0.1:15000" || cfg.ACME.BaseURL != "https://acme.example.com" {
		t.Fatalf("ACME 环境变量未覆盖: %+v", cfg.ACME)
	}
}

func TestLoadACMERejectsIncompleteTLS(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
acme:
  enabled: true
  addr: ":14000"
  tls:
    cert: "/tmp/acme.crt"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	if _, err := Load(); err == nil {
		t.Fatal("TLS 证书和私钥只配置一项时应报错")
	}
}

// TestLoadPublicURL 验证对外访问地址的读取与环境变量覆盖。
func TestLoadPublicURL(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
  public_url: "https://ssl.example.com/"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	if cfg, err := Load(); err != nil {
		t.Fatal(err)
	} else if cfg.PublicURL != "https://ssl.example.com" {
		t.Fatalf("public_url 未正确归一化: %q", cfg.PublicURL)
	}

	t.Setenv("LACTSSL_PUBLIC_URL", "https://override.example.com")
	if cfg, err := Load(); err != nil {
		t.Fatal(err)
	} else if cfg.PublicURL != "https://override.example.com" {
		t.Fatalf("环境变量未覆盖 public_url: %q", cfg.PublicURL)
	}

	t.Setenv("LACTSSL_PUBLIC_URL", "ftp://invalid")
	if _, err := Load(); err == nil {
		t.Fatal("非法 public_url 应返回错误")
	}
}
