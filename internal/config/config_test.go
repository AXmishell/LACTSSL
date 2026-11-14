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
	for _, key := range []string{"LACTSSL_CONFIG", "LACTSSL_DATA", "LACTSSL_ADDR", "LACTSSL_PORT", "LACTSSL_ENTRY", "LACTSSL_PUBLIC_URL", "LACTSSL_TRUSTED_PROXIES", "LACTSSL_ACME_ENABLED", "LACTSSL_ACME_ADDR", "LACTSSL_ACME_BASE_URL", "LACTSSL_ACME_TLS_CERT", "LACTSSL_ACME_TLS_KEY", "LACTSSL_ACME_MTLS_ENABLED", "LACTSSL_ACME_MTLS_CLIENT_CA", "LACTSSL_PLUGINS_ENABLED", "LACTSSL_PLUGINS_DIR", "LACTSSL_PLUGINS_MEMORY_MB", "LACTSSL_PLUGINS_TIMEOUT_SECONDS", "LACTSSL_PLUGINS_MAX_OUTPUT_KB", "LACTSSL_DEPLOY_PLUGINS_ENABLED", "LACTSSL_DEPLOY_PLUGINS_DIR", "LACTSSL_DEPLOY_PLUGINS_MEMORY_MB", "LACTSSL_DEPLOY_PLUGINS_TIMEOUT_SECONDS", "LACTSSL_DEPLOY_PLUGINS_MAX_OUTPUT_KB", "LACTSSL_LOG_LEVEL", "LACTSSL_LOG_FORMAT", "LACTSSL_LOG_FILE", "LACTSSL_LOG_ALSO_STDOUT", "LACTSSL_LOG_MAX_SIZE_MB", "LACTSSL_LOG_MAX_BACKUPS", "LACTSSL_LOG_MAX_AGE_DAYS", "LACTSSL_LOG_COMPRESS", "LACTSSL_LOG_ACCESS", "LACTSSL_LOG_SYSTEM_ENABLED", "LACTSSL_LOG_SYSTEM_LEVEL", "LACTSSL_LOG_SYSTEM_ACCESS", "LACTSSL_LOG_SYSTEM_RETENTION_DAYS", "LACTSSL_LOG_SYSTEM_MAX_ROWS"} {
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

func TestLoadEnablesACMEByDefault(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	t.Setenv("LACTSSL_DATA", filepath.Join(dir, "data"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ACME.Enabled {
		t.Fatal("内置 ACME 服务端应默认开启")
	}

	// 显式关闭时仍以配置文件为准。
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
acme:
  enabled: false
`, filepath.Join(dir, "data"))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.Enabled {
		t.Fatal("显式配置 acme.enabled=false 时应关闭 ACME 服务端")
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

func TestLoadACMEBaseURLFallsBackToPublicURL(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  public_url: "https://ssl.example.com"
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
acme:
  enabled: true
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.BaseURL != "https://ssl.example.com" {
		t.Fatalf("ACME base_url 应回退到 public_url: %q", cfg.ACME.BaseURL)
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

func TestValidateFileRejectsUnsafeRestoreConfig(t *testing.T) {
	validSecret := strings.Repeat("a", 32)
	validSalt := strings.Repeat("b", 32)
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid",
			content: fmt.Sprintf(`security:
  entry: "restore-entry"
  jwt_secret: %q
  salt: %q
server:
  addr: ":8080"
`, validSecret, validSalt),
		},
		{
			name: "short salt",
			content: `security:
  salt: "short"
`,
			wantErr: true,
		},
		{
			name: "invalid entry",
			content: `security:
  entry: "x"
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "invalid port",
			content: `server:
  port: 70000
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "invalid listen addr",
			content: `server:
  addr: "localhost"
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "tls pair mismatch",
			content: `acme:
  tls:
    cert: "/tmp/cert.pem"
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "mtls without tls",
			content: `acme:
  mtls:
    enabled: true
    client_ca: "/tmp/ca.pem"
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "invalid log level",
			content: `log:
  level: verbose
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
		{
			name: "invalid system log level",
			content: `log:
  system:
    level: verbose
security:
  jwt_secret: "` + validSecret + `"
  salt: "` + validSalt + `"
`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateFile(path)
			if tc.wantErr && err == nil {
				t.Fatal("应拒绝该配置")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("合法配置被拒绝: %v", err)
			}
		})
	}
}

func TestLoadDeployPluginsConfig(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
deploy_plugins:
  enabled: true
  dir: %q
  memory_limit_mb: 96
  timeout_seconds: 120
  max_output_kb: 32
`, dataDir, filepath.Join(dir, "deploy-plugins"))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DeployPlugins.Enabled || cfg.DeployPlugins.Dir != filepath.Join(dir, "deploy-plugins") {
		t.Fatalf("unexpected deploy plugin config: %+v", cfg.DeployPlugins)
	}
	if cfg.DeployPlugins.MemoryLimitMB != 96 || cfg.DeployPlugins.Timeout.Seconds() != 120 || cfg.DeployPlugins.MaxOutputKB != 32 {
		t.Fatalf("unexpected deploy plugin limits: %+v", cfg.DeployPlugins)
	}

	// 环境变量优先于配置文件。
	t.Setenv("LACTSSL_DEPLOY_PLUGINS_ENABLED", "false")
	t.Setenv("LACTSSL_DEPLOY_PLUGINS_DIR", filepath.Join(dir, "env-plugins"))
	t.Setenv("LACTSSL_DEPLOY_PLUGINS_MEMORY_MB", "64")
	t.Setenv("LACTSSL_DEPLOY_PLUGINS_TIMEOUT_SECONDS", "90")
	t.Setenv("LACTSSL_DEPLOY_PLUGINS_MAX_OUTPUT_KB", "16")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployPlugins.Enabled {
		t.Fatal("environment should disable deploy plugins")
	}
	if cfg.DeployPlugins.Dir != filepath.Join(dir, "env-plugins") || cfg.DeployPlugins.MemoryLimitMB != 64 {
		t.Fatalf("environment override not applied: %+v", cfg.DeployPlugins)
	}
	if cfg.DeployPlugins.Timeout.Seconds() != 90 || cfg.DeployPlugins.MaxOutputKB != 16 {
		t.Fatalf("environment limits not applied: %+v", cfg.DeployPlugins)
	}
}

func TestLoadLogConfig(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  level: debug
  format: json
  file: %q
  also_stdout: false
  max_size_mb: 8
  max_backups: 3
  max_age_days: 14
  compress: false
  access_log: false
  system:
    enabled: false
    level: warn
    access: true
    retention_days: 3
    max_rows: 500
`, dataDir, filepath.Join(dir, "logs", "app.log"))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" || cfg.Log.AlsoStdout || cfg.Log.Compress || cfg.Log.AccessLog {
		t.Fatalf("unexpected log config: %+v", cfg.Log)
	}
	if cfg.Log.File != filepath.Join(dir, "logs", "app.log") || cfg.Log.MaxSizeMB != 8 || cfg.Log.MaxBackups != 3 || cfg.Log.MaxAgeDays != 14 {
		t.Fatalf("unexpected log limits: %+v", cfg.Log)
	}
	if cfg.Log.SystemEnabled || cfg.Log.SystemLevel != "warn" || !cfg.Log.SystemAccess || cfg.Log.SystemRetentionDays != 3 || cfg.Log.SystemMaxRows != 500 {
		t.Fatalf("unexpected system log config: %+v", cfg.Log)
	}

	// 环境变量优先于配置文件。
	t.Setenv("LACTSSL_LOG_LEVEL", "warn")
	t.Setenv("LACTSSL_LOG_FORMAT", "text")
	t.Setenv("LACTSSL_LOG_FILE", filepath.Join(dir, "env.log"))
	t.Setenv("LACTSSL_LOG_ALSO_STDOUT", "true")
	t.Setenv("LACTSSL_LOG_MAX_SIZE_MB", "16")
	t.Setenv("LACTSSL_LOG_MAX_BACKUPS", "0")
	t.Setenv("LACTSSL_LOG_MAX_AGE_DAYS", "0")
	t.Setenv("LACTSSL_LOG_COMPRESS", "true")
	t.Setenv("LACTSSL_LOG_ACCESS", "true")
	t.Setenv("LACTSSL_LOG_SYSTEM_ENABLED", "true")
	t.Setenv("LACTSSL_LOG_SYSTEM_LEVEL", "error")
	t.Setenv("LACTSSL_LOG_SYSTEM_ACCESS", "false")
	t.Setenv("LACTSSL_LOG_SYSTEM_RETENTION_DAYS", "30")
	t.Setenv("LACTSSL_LOG_SYSTEM_MAX_ROWS", "1000")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "warn" || cfg.Log.Format != "text" || !cfg.Log.AlsoStdout || !cfg.Log.Compress || !cfg.Log.AccessLog {
		t.Fatalf("environment override not applied: %+v", cfg.Log)
	}
	if cfg.Log.File != filepath.Join(dir, "env.log") || cfg.Log.MaxSizeMB != 16 || cfg.Log.MaxBackups != 0 || cfg.Log.MaxAgeDays != 0 {
		t.Fatalf("environment log limits not applied: %+v", cfg.Log)
	}
	if !cfg.Log.SystemEnabled || cfg.Log.SystemLevel != "error" || cfg.Log.SystemAccess || cfg.Log.SystemRetentionDays != 30 || cfg.Log.SystemMaxRows != 1000 {
		t.Fatalf("environment system log config not applied: %+v", cfg.Log)
	}
}

func TestLoadRejectsInvalidLogConfig(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  level: verbose
`, filepath.Join(dir, "data"))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_CONFIG", cfgPath)
	if _, err := Load(); err == nil {
		t.Fatal("非法日志级别应导致启动失败")
	}
}

func TestDeployPluginsDefaultDisabled(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	t.Setenv("LACTSSL_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("LACTSSL_DATA", filepath.Join(dir, "data"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployPlugins.Enabled {
		t.Fatal("deploy plugins should be disabled by default")
	}
	if cfg.Plugins.Dir != filepath.Join(dir, "data", "plugins", "dns") {
		t.Fatalf("unexpected default DNS plugin dir: %s", cfg.Plugins.Dir)
	}
	if cfg.DeployPlugins.Dir != filepath.Join(dir, "data", "plugins", "deploy") {
		t.Fatalf("unexpected default deploy plugin dir: %s", cfg.DeployPlugins.Dir)
	}
	if cfg.DeployPlugins.Timeout.Seconds() != 300 {
		t.Fatalf("unexpected default timeout: %s", cfg.DeployPlugins.Timeout)
	}
}
