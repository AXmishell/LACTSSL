package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesConfigAndMigratesLegacySecret(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfgPath := "config.yaml"
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(dataDir, "jwt.secret"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

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
	dir := t.TempDir()
	t.Chdir(dir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ACME.Enabled {
		t.Fatal("内置 ACME 服务端应默认开启")
	}

	// 显式关闭时仍以配置文件为准。
	content := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
acme:
  enabled: false
`
	if err := os.WriteFile("config.yaml", []byte(content), 0o600); err != nil {
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

func TestLoadFromYAML(t *testing.T) {
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

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Fatalf("YAML 监听地址未生效: %s", cfg.Addr)
	}
	if cfg.Entry != "from-file" {
		t.Fatalf("YAML 门禁入口未生效: %s", cfg.Entry)
	}
	if string(cfg.JWTSecret) != "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj" || string(cfg.EncryptionKey) != "ssssssssssssssssssssssssssssssss" {
		t.Fatal("YAML 密钥未生效")
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("YAML 可信代理未生效: %v", cfg.TrustedProxies)
	}
}

func TestLoadNormalizesTrustedProxies(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`server:
  trusted_proxies: [" 127.0.0.1 ", "::ffff:192.0.2.1", "10.1.2.3/8", "::ffff:172.16.0.0/108"]
data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "192.0.2.1", "10.0.0.0/8", "172.16.0.0/12"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("可信代理未规范化: %v", cfg.TrustedProxies)
	}
	for index := range want {
		if cfg.TrustedProxies[index] != want[index] {
			t.Fatalf("第 %d 项应为 %s，实际 %s", index, want[index], cfg.TrustedProxies[index])
		}
	}
}

func TestLoadProxyModes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`server:
  client_ip_mode: "cloudflare"
  proxy_protocol: true
  tls:
    cert: "/tmp/server.crt"
    key: "/tmp/server.key"
    proxy_client_ca: "/tmp/proxy-ca.pem"
data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPMode != "cloudflare" || !cfg.ProxyProtocol {
		t.Fatalf("代理模式未生效: mode=%s proxy=%v", cfg.ClientIPMode, cfg.ProxyProtocol)
	}
	if cfg.ServerTLS.Cert != "/tmp/server.crt" || cfg.ServerTLS.Key != "/tmp/server.key" || cfg.ServerTLS.ProxyClientCA != "/tmp/proxy-ca.pem" {
		t.Fatalf("主监听 TLS 未生效: %+v", cfg.ServerTLS)
	}

	content = fmt.Sprintf(`server:
  client_ip_mode: "invalid"
data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("应拒绝非法 client_ip_mode")
	}

	content = fmt.Sprintf(`server:
  tls:
    cert: "/tmp/server.crt"
data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("应拒绝不完整的 server.tls 配置")
	}
}

func TestLoadRejectsUnsafeTrustedProxies(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`server:
  trusted_proxies: ["0.0.0.0/0"]
data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("启动加载应拒绝 /0 可信代理")
	}
}

// TestLoadIgnoresEnvironmentVariables 确认运行配置只来自 config.yaml。
func TestLoadIgnoresEnvironmentVariables(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`server:
  addr: ":9090"
  trusted_proxies: ["127.0.0.1"]
data_dir: %q
security:
  entry: "from-file"
  public_url: "https://file.example.com"
  session_persistence: false
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  level: info
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LACTSSL_ADDR", ":9999")
	t.Setenv("LACTSSL_PORT", "7777")
	t.Setenv("LACTSSL_DATA", filepath.Join(dir, "env-data"))
	t.Setenv("LACTSSL_ENTRY", "from-env")
	t.Setenv("LACTSSL_PUBLIC_URL", "https://env.example.com")
	t.Setenv("LACTSSL_SESSION_PERSISTENCE", "true")
	t.Setenv("LACTSSL_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("LACTSSL_LOG_LEVEL", "debug")

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9090" || cfg.DataDir != dataDir || cfg.Entry != "from-file" {
		t.Fatalf("配置文件值被环境变量影响: addr=%s data=%s entry=%s", cfg.Addr, cfg.DataDir, cfg.Entry)
	}
	if cfg.PublicURL != "https://file.example.com" || cfg.SessionPersistence {
		t.Fatalf("配置文件值被环境变量影响: public_url=%s session=%v", cfg.PublicURL, cfg.SessionPersistence)
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "127.0.0.1" || cfg.Log.Level != "info" {
		t.Fatalf("配置文件值被环境变量影响: proxies=%v log=%s", cfg.TrustedProxies, cfg.Log.Level)
	}
}

func TestLoadSessionPersistence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	content := fmt.Sprintf(`data_dir: %q
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`, dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionPersistence {
		t.Fatal("会话保持应默认关闭")
	}

	content += "  session_persistence: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SessionPersistence {
		t.Fatal("YAML 中的会话保持未生效")
	}
}

func TestLoadRejectsWeakSecrets(t *testing.T) {
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
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("过短的 jwt_secret 应被拒绝")
	}
}

func TestLoadACMEConfig(t *testing.T) {
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
	cfg, err := LoadFrom(cfgPath)
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
}

func TestLoadACMEBaseURLFallsBackToPublicURL(t *testing.T) {
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
	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.BaseURL != "https://ssl.example.com" {
		t.Fatalf("ACME base_url 应回退到 public_url: %q", cfg.ACME.BaseURL)
	}
}

func TestLoadACMERejectsIncompleteTLS(t *testing.T) {
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
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("TLS 证书和私钥只配置一项时应报错")
	}
}

// TestLoadPublicURL 验证对外访问地址的读取与归一化。
func TestLoadPublicURL(t *testing.T) {
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
	if cfg, err := LoadFrom(cfgPath); err != nil {
		t.Fatal(err)
	} else if cfg.PublicURL != "https://ssl.example.com" {
		t.Fatalf("public_url 未正确归一化: %q", cfg.PublicURL)
	}

	invalid := strings.Replace(content, `public_url: "https://ssl.example.com/"`, `public_url: "ftp://invalid"`, 1)
	if err := os.WriteFile(cfgPath, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(cfgPath); err == nil {
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

	cfg, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DeployPlugins.Enabled || cfg.DeployPlugins.Dir != filepath.Join(dir, "deploy-plugins") {
		t.Fatalf("unexpected deploy plugin config: %+v", cfg.DeployPlugins)
	}
	if cfg.DeployPlugins.MemoryLimitMB != 96 || cfg.DeployPlugins.Timeout.Seconds() != 120 || cfg.DeployPlugins.MaxOutputKB != 32 {
		t.Fatalf("unexpected deploy plugin limits: %+v", cfg.DeployPlugins)
	}
}

func TestLoadLogConfig(t *testing.T) {
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

	cfg, err := LoadFrom(cfgPath)
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
}

func TestLoadRejectsInvalidLogConfig(t *testing.T) {
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
	if _, err := LoadFrom(cfgPath); err == nil {
		t.Fatal("非法日志级别应导致启动失败")
	}
}

func TestDeployPluginsDefaultDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployPlugins.Enabled {
		t.Fatal("deploy plugins should be disabled by default")
	}
	if cfg.Plugins.Dir != filepath.Join("data", "plugins", "dns") {
		t.Fatalf("unexpected default DNS plugin dir: %s", cfg.Plugins.Dir)
	}
	if cfg.DeployPlugins.Dir != filepath.Join("data", "plugins", "deploy") {
		t.Fatalf("unexpected default deploy plugin dir: %s", cfg.DeployPlugins.Dir)
	}
	if cfg.DeployPlugins.Timeout.Seconds() != 300 {
		t.Fatalf("unexpected default timeout: %s", cfg.DeployPlugins.Timeout)
	}
}

// TestLoadEnablesOfficialPluginRepoKeyByDefault 验证新配置默认启用官方仓库验签，
// 且生成的 config.yaml 模板写入同一个公钥（避免默认值与模板漂移）。
func TestLoadEnablesOfficialPluginRepoKeyByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfgPath := "config.yaml"

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PluginRepo.PublicKey != officialPluginRepoPublicKey {
		t.Fatalf("插件仓库公钥默认值不正确: %q", cfg.PluginRepo.PublicKey)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), officialPluginRepoPublicKey) {
		t.Fatal("生成的 config.yaml 未写入官方插件仓库公钥")
	}
	text := string(raw)
	if strings.Contains(text, "%!") {
		t.Fatalf("生成的 config.yaml 存在格式化占位符错误:\n%s", text)
	}
	for _, key := range []string{"session_persistence:", "public_key:", "token:"} {
		if !strings.Contains(text, key) {
			t.Fatalf("生成的 config.yaml 缺少配置项 %q:\n%s", key, text)
		}
	}
	assertYAMLHasAllConfigFields(t, raw, "生成的 config.yaml")
}

// TestWriteYAMLPreservesCommentsWhenFillingSecrets 验证补齐密钥时不会丢掉用户注释。
func TestWriteYAMLPreservesCommentsWhenFillingSecrets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`# 用户自定义注释，必须保留
data_dir: %q
security:
  # 密钥留空时启动自动生成
  jwt_secret: ""
  salt: ""
`, filepath.Join(dir, "data"))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFrom(cfgPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "用户自定义注释") || !strings.Contains(text, "密钥留空时启动自动生成") {
		t.Fatalf("补齐密钥后注释丢失:\n%s", text)
	}
	if strings.Contains(text, `jwt_secret: ""`) || strings.Contains(text, `salt: ""`) {
		t.Fatalf("密钥未写入:\n%s", text)
	}
}
