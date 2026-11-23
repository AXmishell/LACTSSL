package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/ratelimit"
)

func TestUpdateSetupConfigPreservesCommentsAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# top comment
server:
  # keep server comment
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
  public_url: ""
  session_persistence: false
acme:
  enabled: true
  addr: ""
  base_url: ""
  tls:
    cert: ""
    key: ""
  mtls:
    enabled: false
    client_ca: ""
log:
  # keep log comment
  level: info
  format: text
  also_stdout: true
  max_size_mb: 20
  max_backups: 5
  max_age_days: 30
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 7
    max_rows: 20000
upgrade:
  auto: true
  auto_optimize: true
  vacuum: true
  program:
    enabled: true
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
plugins:
  enabled: true
  dir: "/tmp/custom-dns-plugins"
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
deploy_plugins:
  enabled: false
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
notify_plugins:
  enabled: true
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	publicURL := "https://ssl.example.com/"
	sessionPersistence := true
	host := "0.0.0.0"
	port := 9090
	proxies := []string{"10.0.0.0/8", "127.0.0.1"}
	acmeEnabled := false
	acmeAddr := ":14000"
	acmeBaseURL := "https://pki.example.com/"
	tlsCert := "/etc/lactssl/acme.crt"
	tlsKey := "/etc/lactssl/acme.key"
	mtlsEnabled := true
	mtlsClientCA := "/etc/lactssl/client-ca.crt"
	level := "debug"
	format := "json"
	alsoStdout := false
	maxSizeMB := 50
	maxBackups := 10
	maxAgeDays := 7
	compress := false
	accessLog := false
	systemEnabled := false
	systemLevel := "warn"
	systemAccess := true
	systemRetentionDays := 30
	systemMaxRows := 5000
	upgradeAuto := false
	upgradeAutoOptimize := false
	upgradeVacuum := false
	programEnabled := false
	programChannel := "beta"
	programCheckInterval := 12
	programMaxDownload := 64
	dnsEnabled := false
	dnsMemory := 256
	dnsTimeout := 120
	dnsMaxOutput := 128
	deployEnabled := true
	deployMemory := 512
	deployTimeout := 600
	deployMaxOutput := 256
	notifyEnabled := false
	notifyMemory := 64
	notifyTimeout := 15
	notifyMaxOutput := 32
	patch := SetupConfigPatch{
		Server:             &SetupServerPatch{Host: &host, Port: &port},
		TrustedProxies:     &proxies,
		PublicURL:          &publicURL,
		SessionPersistence: &sessionPersistence,
		ACME: &SetupACMEPatch{
			Enabled: &acmeEnabled,
			Addr:    &acmeAddr,
			BaseURL: &acmeBaseURL,
			TLS:     &SetupTLSPatch{Cert: &tlsCert, Key: &tlsKey},
			MTLS:    &SetupMTLSPatch{Enabled: &mtlsEnabled, ClientCA: &mtlsClientCA},
		},
		Log: &SetupLogPatch{
			Level:      &level,
			Format:     &format,
			AlsoStdout: &alsoStdout,
			MaxSizeMB:  &maxSizeMB,
			MaxBackups: &maxBackups,
			MaxAgeDays: &maxAgeDays,
			Compress:   &compress,
			AccessLog:  &accessLog,
			System: &SetupLogSystemPatch{
				Enabled:       &systemEnabled,
				Level:         &systemLevel,
				Access:        &systemAccess,
				RetentionDays: &systemRetentionDays,
				MaxRows:       &systemMaxRows,
			},
		},
		Upgrade: &SetupUpgradePatch{
			Auto:         &upgradeAuto,
			AutoOptimize: &upgradeAutoOptimize,
			Vacuum:       &upgradeVacuum,
			Program: &SetupProgramPatch{
				Enabled:            &programEnabled,
				Channel:            &programChannel,
				CheckIntervalHours: &programCheckInterval,
				MaxDownloadMB:      &programMaxDownload,
			},
		},
		Plugins: &SetupPluginsPatch{
			Enabled:        &dnsEnabled,
			MemoryLimitMB:  &dnsMemory,
			TimeoutSeconds: &dnsTimeout,
			MaxOutputKB:    &dnsMaxOutput,
		},
		DeployPlugins: &SetupPluginRuntimePatch{
			Enabled:        &deployEnabled,
			MemoryLimitMB:  &deployMemory,
			TimeoutSeconds: &deployTimeout,
			MaxOutputKB:    &deployMaxOutput,
		},
		NotifyPlugins: &SetupPluginRuntimePatch{
			Enabled:        &notifyEnabled,
			MemoryLimitMB:  &notifyMemory,
			TimeoutSeconds: &notifyTimeout,
			MaxOutputKB:    &notifyMaxOutput,
		},
	}
	if err := UpdateSetupConfig(path, patch); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{
		"# top comment",
		"# keep server comment",
		"# keep log comment",
		`dir: "/tmp/custom-dns-plugins"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("配置注释或未编辑字段丢失: 缺少 %q\n%s", want, content)
		}
	}

	fc, _, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Security.JWTSecret != "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj" || fc.Security.Salt != "ssssssssssssssssssssssssssssssss" {
		t.Fatal("更新白名单字段时不应改动密钥")
	}
	if fc.Server.Host != host || fc.Server.Port != port {
		t.Fatalf("监听地址未更新: %+v", fc.Server)
	}
	if len(fc.Server.TrustedProxies) != 2 || fc.Server.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("可信代理未更新: %+v", fc.Server.TrustedProxies)
	}
	if fc.Security.PublicURL != "https://ssl.example.com" || !fc.Security.SessionPersistence {
		t.Fatalf("安全配置未更新: %+v", fc.Security)
	}
	if fc.ACME.Enabled || fc.ACME.Addr != acmeAddr || fc.ACME.BaseURL != "https://pki.example.com" ||
		fc.ACME.TLS.Cert != tlsCert || fc.ACME.TLS.Key != tlsKey ||
		!fc.ACME.MTLS.Enabled || fc.ACME.MTLS.ClientCA != mtlsClientCA {
		t.Fatalf("ACME 配置未更新: %+v", fc.ACME)
	}
	if fc.Log.Level != level || fc.Log.Format != format || fc.Log.AlsoStdout ||
		fc.Log.MaxSizeMB != maxSizeMB || fc.Log.MaxBackups != maxBackups || fc.Log.MaxAgeDays != maxAgeDays ||
		fc.Log.Compress || fc.Log.AccessLog {
		t.Fatalf("日志配置未更新: %+v", fc.Log)
	}
	if fc.Log.System.Enabled || fc.Log.System.Level != systemLevel || !fc.Log.System.Access ||
		fc.Log.System.RetentionDays != systemRetentionDays || fc.Log.System.MaxRows != systemMaxRows {
		t.Fatalf("系统日志配置未更新: %+v", fc.Log.System)
	}
	if fc.Upgrade.Auto || fc.Upgrade.AutoOptimize || fc.Upgrade.Vacuum ||
		fc.Upgrade.Program.Enabled || fc.Upgrade.Program.Channel != programChannel ||
		fc.Upgrade.Program.CheckIntervalHours != programCheckInterval ||
		fc.Upgrade.Program.MaxDownloadMB != programMaxDownload {
		t.Fatalf("升级配置未更新: %+v", fc.Upgrade)
	}
	if fc.Plugins.Enabled || fc.Plugins.MemoryLimitMB != dnsMemory || fc.Plugins.TimeoutSeconds != dnsTimeout ||
		fc.Plugins.MaxOutputKB != dnsMaxOutput {
		t.Fatalf("DNS 插件配置未更新: %+v", fc.Plugins)
	}
	if !fc.DeployPlugins.Enabled || fc.DeployPlugins.MemoryLimitMB != deployMemory ||
		fc.DeployPlugins.TimeoutSeconds != deployTimeout || fc.DeployPlugins.MaxOutputKB != deployMaxOutput {
		t.Fatalf("部署插件配置未更新: %+v", fc.DeployPlugins)
	}
	if fc.NotifyPlugins.Enabled || fc.NotifyPlugins.MemoryLimitMB != notifyMemory ||
		fc.NotifyPlugins.TimeoutSeconds != notifyTimeout || fc.NotifyPlugins.MaxOutputKB != notifyMaxOutput {
		t.Fatalf("通知插件配置未更新: %+v", fc.NotifyPlugins)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限不正确: %v", info.Mode().Perm())
	}
}

func TestUpdateSetupConfigRejectsInvalidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  level: info
  format: text
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	badURL := "https://ssl.example.com/path"
	if err := UpdateSetupConfig(path, SetupConfigPatch{PublicURL: &badURL}); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("非法 public_url 应返回 ErrSetupConfigInvalid: %v", err)
	}
	badProxy := []string{"not-an-ip"}
	if err := UpdateSetupConfig(path, SetupConfigPatch{TrustedProxies: &badProxy}); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("非法可信代理应返回 ErrSetupConfigInvalid: %v", err)
	}
	badSize := 4096
	if err := UpdateSetupConfig(path, SetupConfigPatch{Log: &SetupLogPatch{MaxSizeMB: &badSize}}); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("非法日志大小应返回 ErrSetupConfigInvalid: %v", err)
	}

	badMTLS := true
	if err := UpdateSetupConfig(path, SetupConfigPatch{ACME: &SetupACMEPatch{
		MTLS: &SetupMTLSPatch{Enabled: &badMTLS},
	}}); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("未配置 TLS 时启用 mTLS 应返回 ErrSetupConfigInvalid: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("校验失败时不应写入配置文件:\n%s", after)
	}
}

func TestLoadSetupConfigUsesDefaultsAndDoesNotExposeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("默认端口不正确: %d", cfg.Server.Port)
	}
	if cfg.Server.ClientIPMode != "auto" || cfg.Server.ProxyProtocol {
		t.Fatalf("默认客户端 IP 模式不正确: %+v", cfg.Server)
	}
	if !cfg.ACME.Enabled || !cfg.Plugins.Enabled || cfg.DeployPlugins.Enabled || !cfg.NotifyPlugins.Enabled {
		t.Fatalf("默认开关不正确: %+v", cfg)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" || cfg.Log.MaxSizeMB != 20 ||
		!cfg.Log.AlsoStdout || !cfg.Log.System.Enabled {
		t.Fatalf("默认日志配置不正确: %+v", cfg.Log)
	}
	if !cfg.Log.Archive.Enabled || !cfg.Log.Archive.Compress || cfg.Log.Archive.QuotaPolicy != "fail" {
		t.Fatalf("默认日志归档配置不正确: %+v", cfg.Log.Archive)
	}
	if !cfg.Upgrade.Auto || !cfg.Upgrade.Program.Enabled || cfg.Upgrade.Program.Channel != "stable" {
		t.Fatalf("默认升级配置不正确: %+v", cfg.Upgrade)
	}
	if cfg.Plugins.TimeoutSeconds != 60 {
		t.Fatalf("默认插件配置不正确: %+v", cfg.Plugins)
	}
	if cfg.RateLimit.MaxKeys != ratelimit.DefaultMaxKeys {
		t.Fatalf("默认限流 key 上限不正确: %d", cfg.RateLimit.MaxKeys)
	}
	if rule := cfg.RateLimit.Rules[ratelimit.LoginIP]; rule.Max != 10 || rule.WindowSeconds != 300 {
		t.Fatalf("默认登录限流规则不正确: %+v", rule)
	}
}

func TestUpdateSetupConfigRateLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	maxKeys := 2000
	max := 3
	window := 90
	patch := SetupConfigPatch{RateLimit: &SetupRateLimitPatch{
		MaxKeys: &maxKeys,
		Rules: map[string]SetupRateLimitRulePatch{
			ratelimit.LoginIP: {Max: &max, WindowSeconds: &window},
		},
	}}
	if err := UpdateSetupConfig(path, patch); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.MaxKeys != maxKeys {
		t.Fatalf("max_keys 未写入: %d", cfg.RateLimit.MaxKeys)
	}
	if rule := cfg.RateLimit.Rules[ratelimit.LoginIP]; rule.Max != max || rule.WindowSeconds != window {
		t.Fatalf("login_ip 未写入: %+v", rule)
	}
	if rule := cfg.RateLimit.Rules[ratelimit.ACMERequest]; rule.Max != 1200 || rule.WindowSeconds != 60 {
		t.Fatalf("未覆盖的规则应保持默认值: %+v", rule)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rate_limit:") || !strings.Contains(string(data), "login_ip:") {
		t.Fatalf("配置文件缺少限流节点:\n%s", data)
	}
}

func TestUpdateSetupConfigRejectsUnknownRateLimitRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	max := 1
	patch := SetupConfigPatch{RateLimit: &SetupRateLimitPatch{
		Rules: map[string]SetupRateLimitRulePatch{
			"not_a_rule": {Max: &max},
		},
	}}
	if err := UpdateSetupConfig(path, patch); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("未知规则应被拒绝，实际: %v", err)
	}
}

func TestUpdateSetupConfigRequiresExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	publicURL := "https://ssl.example.com"
	err := UpdateSetupConfig(path, SetupConfigPatch{PublicURL: &publicURL})
	if err == nil {
		t.Fatal("配置文件不存在时应拒绝写入，避免生成缺失密钥的配置")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("不应创建新配置文件: %v", statErr)
	}
}

func TestUpdateSetupConfigProxyAndArchiveFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	clientIPMode := "cloudflare"
	proxyProtocol := true
	archiveEnabled := true
	archiveCompress := false
	archiveRetentionDays := 90
	archiveMaxTotalMB := 512
	archiveQuotaPolicy := "prune_oldest"
	allowUnsigned := true
	patch := SetupConfigPatch{
		Server: &SetupServerPatch{
			ClientIPMode:  &clientIPMode,
			ProxyProtocol: &proxyProtocol,
		},
		Log: &SetupLogPatch{Archive: &SetupLogArchivePatch{
			Enabled:       &archiveEnabled,
			Compress:      &archiveCompress,
			RetentionDays: &archiveRetentionDays,
			MaxTotalMB:    &archiveMaxTotalMB,
			QuotaPolicy:   &archiveQuotaPolicy,
		}},
		Upgrade: &SetupUpgradePatch{Program: &SetupProgramPatch{
			AllowUnsigned: &allowUnsigned,
		}},
	}
	if err := UpdateSetupConfig(path, patch); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ClientIPMode != clientIPMode || !cfg.Server.ProxyProtocol {
		t.Fatalf("代理识别配置未更新: %+v", cfg.Server)
	}
	if !cfg.Log.Archive.Enabled || cfg.Log.Archive.Compress ||
		cfg.Log.Archive.RetentionDays != archiveRetentionDays ||
		cfg.Log.Archive.MaxTotalMB != archiveMaxTotalMB ||
		cfg.Log.Archive.QuotaPolicy != archiveQuotaPolicy {
		t.Fatalf("日志归档配置未更新: %+v", cfg.Log.Archive)
	}
	if !cfg.Upgrade.Program.AllowUnsigned {
		t.Fatal("allow_unsigned 未更新")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"client_ip_mode: cloudflare",
		"proxy_protocol: true",
		"archive:",
		"quota_policy: prune_oldest",
		"allow_unsigned: true",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("配置缺少 %q:\n%s", want, raw)
		}
	}
}

func TestUpdateSetupConfigAllowsUnsignedOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	allowUnsigned := true
	if err := UpdateSetupConfig(path, SetupConfigPatch{
		Upgrade: &SetupUpgradePatch{Program: &SetupProgramPatch{AllowUnsigned: &allowUnsigned}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Upgrade.Program.AllowUnsigned {
		t.Fatal("仅提交 allow_unsigned 时也应写入")
	}
}

func TestUpdateSetupConfigResetsRateLimitRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
  rate_limit:
    rules:
      login_ip: {max: 99, window_seconds: 60}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSetupConfig(path, SetupConfigPatch{
		Reset: []string{"security.rate_limit.rules.login_ip"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "login_ip:") {
		t.Fatalf("恢复默认后应从 config.yaml 删除该规则:\n%s", raw)
	}
	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	def := cfg.RateLimit.Defaults[ratelimit.LoginIP]
	rule := cfg.RateLimit.Rules[ratelimit.LoginIP]
	if rule.Max != def.Max || rule.WindowSeconds != def.WindowSeconds {
		t.Fatalf("规则未恢复内置默认值: rule=%+v default=%+v", rule, def)
	}
}

func TestResetRateLimitRuleIsNotNoopWhenOverrideEqualsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
  rate_limit:
    rules:
      login_ip: {max: 10, window_seconds: 300}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareConfigUpdate(path, SetupConfigPatch{
		Reset: []string{"security.rate_limit.rules.login_ip"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.ChangedFields) == 0 {
		t.Fatal("删除与默认值相同的显式规则时也应产生变更")
	}
	if !containsString(prepared.ChangedFields, "security.rate_limit.rules.login_ip") {
		t.Fatalf("changed_fields 应包含重置路径: %v", prepared.ChangedFields)
	}
}

func TestUpdateSetupConfigRejectsUnknownResetPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateSetupConfig(path, SetupConfigPatch{Reset: []string{"security.jwt_secret"}})
	if !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("不支持的重置路径应被拒绝: %v", err)
	}
}

func TestUpdateSetupConfigResetsLogArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  archive:
    enabled: false
    compress: false
    retention_days: 30
    max_total_mb: 1024
    quota_policy: prune_oldest
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSetupConfig(path, SetupConfigPatch{Reset: []string{"log.archive"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "archive:") {
		t.Fatalf("恢复默认后应删除 log.archive 段:\n%s", raw)
	}
	cfg, err := LoadSetupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Log.Archive.Enabled || !cfg.Log.Archive.Compress ||
		cfg.Log.Archive.RetentionDays != 0 || cfg.Log.Archive.MaxTotalMB != 0 ||
		cfg.Log.Archive.QuotaPolicy != "fail" {
		t.Fatalf("日志归档未恢复默认: %+v", cfg.Log.Archive)
	}
}
