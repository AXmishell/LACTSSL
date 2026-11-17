package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
  repo:
    enabled: true
    url: "https://example.com/index.json"
    channel: stable
    auto_update: false
    public_key: "keep-public-key"
    timeout_seconds: 30
    max_download_mb: 256
    check_interval_hours: 24
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
	repoEnabled := false
	repoURL := "file:///srv/lactssl-plugins/index.json"
	repoChannel := "beta"
	repoAutoUpdate := true
	repoTimeout := 60
	repoMaxDownload := 512
	repoCheckInterval := 48
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
			Repo: &SetupPluginRepoPatch{
				Enabled:            &repoEnabled,
				URL:                &repoURL,
				Channel:            &repoChannel,
				AutoUpdate:         &repoAutoUpdate,
				TimeoutSeconds:     &repoTimeout,
				MaxDownloadMB:      &repoMaxDownload,
				CheckIntervalHours: &repoCheckInterval,
			},
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
		`public_key: "keep-public-key"`,
		`url: file:///srv/lactssl-plugins/index.json`,
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
	if fc.Plugins.Repo.Enabled || fc.Plugins.Repo.URL != repoURL || fc.Plugins.Repo.Channel != repoChannel ||
		!fc.Plugins.Repo.AutoUpdate || fc.Plugins.Repo.TimeoutSeconds != repoTimeout ||
		fc.Plugins.Repo.MaxDownloadMB != repoMaxDownload || fc.Plugins.Repo.CheckIntervalHours != repoCheckInterval {
		t.Fatalf("插件仓库配置未更新: %+v", fc.Plugins.Repo)
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
	badChannel := "nightly"
	if err := UpdateSetupConfig(path, SetupConfigPatch{Plugins: &SetupPluginsPatch{
		Repo: &SetupPluginRepoPatch{Channel: &badChannel},
	}}); !errors.Is(err, ErrSetupConfigInvalid) {
		t.Fatalf("非法插件仓库通道应返回 ErrSetupConfigInvalid: %v", err)
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
	if !cfg.ACME.Enabled || !cfg.Plugins.Enabled || cfg.DeployPlugins.Enabled || !cfg.NotifyPlugins.Enabled {
		t.Fatalf("默认开关不正确: %+v", cfg)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" || cfg.Log.MaxSizeMB != 20 ||
		!cfg.Log.AlsoStdout || !cfg.Log.System.Enabled {
		t.Fatalf("默认日志配置不正确: %+v", cfg.Log)
	}
	if !cfg.Upgrade.Auto || !cfg.Upgrade.Program.Enabled || cfg.Upgrade.Program.Channel != "stable" {
		t.Fatalf("默认升级配置不正确: %+v", cfg.Upgrade)
	}
	if !cfg.Plugins.Repo.Enabled || cfg.Plugins.Repo.Channel != "stable" || cfg.Plugins.TimeoutSeconds != 60 {
		t.Fatalf("默认插件配置不正确: %+v", cfg.Plugins)
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
