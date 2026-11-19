// Package config 负责加载 config.yaml 运行配置。
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"lactssl/internal/util"
)

// DefaultPath 是默认配置文件路径（相对当前工作目录）。
const DefaultPath = "config.yaml"

// officialPluginRepoPublicKey 是官方插件仓库索引的 Ed25519 公钥（base64）。
// 内置该公钥后，默认安装/更新会强制校验索引签名，而不是只依赖 HTTPS。
const officialPluginRepoPublicKey = "t7Q4vd0B1zh3mRf0t6Q6qlBViL7cKhoipgpGWx5WSss="

// Config 保存服务运行所需的全部基础配置。
type Config struct {
	Addr          string // 监听地址，例如 :8080
	DataDir       string // 数据目录
	DBPath        string // SQLite 数据库文件
	JWTSecret     []byte // JWT 签名密钥
	EncryptionKey []byte // 数据库敏感配置加密密钥（盐）
	Entry         string // 初始门禁入口（来自配置文件）
	PublicURL     string // 对外访问地址（用于邮件链接等绝对地址）
	// SessionPersistence 为 true 时进程重启后保留登录会话（不清理 sessions 表，
	// 且 JWT 签名子密钥不随启动轮换），否则每次启动都会使所有旧登录失效。
	SessionPersistence bool             // 重启后保留登录会话
	TrustedProxies     []string         // 可信反向代理 CIDR；为空表示不信任任何代理头
	ACME               ACMEConfig       // 内置 ACME 服务端配置
	Plugins            PluginsConfig    // 外部 DNS 插件配置
	PluginRepo         PluginRepoConfig // 插件仓库配置
	DeployPlugins      PluginsConfig    // 外部证书部署插件配置
	NotifyPlugins      PluginsConfig    // 外部通知插件配置
	Log                LogConfig        // 进程底层日志配置
	Upgrade            UpgradeConfig    // 版本升级迁移工具配置
	File               string           // 实际使用的配置文件路径
	// LegacySecretFile 是旧版本 data/jwt.secret 的候选路径。
	// 仅用于升级工具确认密钥已写入 config.yaml 后清理遗留文件，正常加载不再依赖它。
	LegacySecretFile string
}

// UpgradeConfig 控制启动时与 /upgrade 页面的迁移/优化行为。
type UpgradeConfig struct {
	// Auto 表示启动时自动执行结构/数据迁移。关闭后只能在 /upgrade 页面手动执行。
	Auto bool
	// AutoOptimize 表示启动时执行低成本数据库优化（索引、统计、WAL checkpoint），不执行 VACUUM。
	AutoOptimize bool
	// Vacuum 表示手动优化时允许执行 VACUUM 回收磁盘空间。
	Vacuum bool
	// Program 控制 /upgrade 页面的程序自升级。
	Program ProgramConfig
}

// ProgramConfig 控制从 GitHub Release 检查并安装新的程序二进制。
type ProgramConfig struct {
	// Enabled 表示允许在 /upgrade 页面检查和安装新版本。
	Enabled bool
	// Repo 是 GitHub 仓库，格式为 owner/repo。
	Repo string
	// Channel 为 stable 或 beta；beta 会包含预发布版本。
	Channel string
	// CheckInterval 是自动检查新版本的最小间隔。
	CheckInterval time.Duration
	// MaxDownloadMB 是发布包下载大小上限。
	MaxDownloadMB int
	// PublicKey 是发布校验和签名（checksums.txt.sig）的 Ed25519 公钥（base64）。
	// 配置后强制验签；为空表示仅依赖 HTTPS 与 SHA-256。
	PublicKey string
	// Token 是可选的 GitHub API token，用于提高版本检查的速率上限。
	Token string
}

// LogConfig 是进程级结构化日志配置，与数据库中的操作日志相互独立。
type LogConfig struct {
	Level      string // debug、info、warn、error
	Format     string // text、json
	File       string // 日志文件路径；为空表示只输出 stdout
	AlsoStdout bool   // 写文件时是否仍输出 stdout
	MaxSizeMB  int    // 单文件大小上限
	MaxBackups int    // 保留历史文件数量，0 表示不限制
	MaxAgeDays int    // 历史文件保留天数，0 表示不限制
	Compress   bool   // 是否压缩历史文件
	AccessLog  bool   // 是否记录 HTTP 访问日志
	// System 控制是否把进程日志写入数据库，供「系统日志」页面查看。
	SystemEnabled       bool
	SystemLevel         string
	SystemAccess        bool
	SystemRetentionDays int
	SystemMaxRows       int
	// Archive 控制删除/清空日志前的归档行为。
	Archive LogArchiveConfig
}

// LogArchiveConfig 控制数据库日志归档。
type LogArchiveConfig struct {
	Enabled       bool   // 是否在删除前归档
	Dir           string // 归档根目录；为空表示 <data_dir>/archives/logs
	Compress      bool   // 是否 gzip 压缩
	RetentionDays int    // 归档文件保留天数，0 表示永久保留
	MaxTotalMB    int    // 归档目录容量上限（MB），0 表示不限制
	QuotaPolicy   string // 容量超限策略：fail / prune_oldest
}

// PluginRepoConfig 是插件仓库客户端配置。
type PluginRepoConfig struct {
	Enabled       bool
	URL           string
	Channel       string
	AutoUpdate    bool
	PublicKey     string
	Timeout       time.Duration
	MaxDownloadMB int
	CheckInterval time.Duration
}

// PluginsConfig 是外部 exec helper 插件的运行配置（DNS 与部署插件各用一份）。
type PluginsConfig struct {
	Enabled       bool
	Dir           string
	MemoryLimitMB int
	Timeout       time.Duration
	MaxOutputKB   int
}

// ACMEConfig 是内置 ACME 服务端的监听与传输层配置。
// 端点、EAB、域名白名单等运行参数保存在数据库中。
type ACMEConfig struct {
	Enabled      bool
	Addr         string
	BaseURL      string
	TLSCert      string
	TLSKey       string
	MTLSEnabled  bool
	MTLSClientCA string
}

// fileConfig 对应 YAML 文件结构。
type fileConfig struct {
	Server struct {
		Addr           string   `yaml:"addr"`
		Host           string   `yaml:"host"`
		Port           int      `yaml:"port"`
		TrustedProxies []string `yaml:"trusted_proxies,omitempty"`
	} `yaml:"server"`
	DataDir  string `yaml:"data_dir"`
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Security struct {
		Entry              string `yaml:"entry"`
		PublicURL          string `yaml:"public_url"`
		SessionPersistence bool   `yaml:"session_persistence"`
		JWTSecret          string `yaml:"jwt_secret"`
		Salt               string `yaml:"salt"`
	} `yaml:"security"`
	ACME struct {
		Enabled bool   `yaml:"enabled"`
		Addr    string `yaml:"addr"`
		BaseURL string `yaml:"base_url"`
		TLS     struct {
			Cert string `yaml:"cert"`
			Key  string `yaml:"key"`
		} `yaml:"tls"`
		MTLS struct {
			Enabled  bool   `yaml:"enabled"`
			ClientCA string `yaml:"client_ca"`
		} `yaml:"mtls"`
	} `yaml:"acme"`
	Plugins struct {
		Enabled        bool   `yaml:"enabled"`
		Dir            string `yaml:"dir"`
		MemoryLimitMB  int    `yaml:"memory_limit_mb"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
		MaxOutputKB    int    `yaml:"max_output_kb"`
		Repo           struct {
			Enabled            bool   `yaml:"enabled"`
			URL                string `yaml:"url"`
			Channel            string `yaml:"channel"`
			AutoUpdate         bool   `yaml:"auto_update"`
			PublicKey          string `yaml:"public_key"`
			TimeoutSeconds     int    `yaml:"timeout_seconds"`
			MaxDownloadMB      int    `yaml:"max_download_mb"`
			CheckIntervalHours int    `yaml:"check_interval_hours"`
		} `yaml:"repo"`
	} `yaml:"plugins"`
	DeployPlugins struct {
		Enabled        bool   `yaml:"enabled"`
		Dir            string `yaml:"dir"`
		MemoryLimitMB  int    `yaml:"memory_limit_mb"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
		MaxOutputKB    int    `yaml:"max_output_kb"`
	} `yaml:"deploy_plugins"`
	NotifyPlugins struct {
		Enabled        bool   `yaml:"enabled"`
		Dir            string `yaml:"dir"`
		MemoryLimitMB  int    `yaml:"memory_limit_mb"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
		MaxOutputKB    int    `yaml:"max_output_kb"`
	} `yaml:"notify_plugins"`
	Log struct {
		Level      string `yaml:"level"`
		Format     string `yaml:"format"`
		File       string `yaml:"file"`
		AlsoStdout bool   `yaml:"also_stdout"`
		MaxSizeMB  int    `yaml:"max_size_mb"`
		MaxBackups int    `yaml:"max_backups"`
		MaxAgeDays int    `yaml:"max_age_days"`
		Compress   bool   `yaml:"compress"`
		AccessLog  bool   `yaml:"access_log"`
		System     struct {
			Enabled       bool   `yaml:"enabled"`
			Level         string `yaml:"level"`
			Access        bool   `yaml:"access"`
			RetentionDays int    `yaml:"retention_days"`
			MaxRows       int    `yaml:"max_rows"`
		} `yaml:"system"`
		Archive struct {
			Enabled       bool   `yaml:"enabled"`
			Dir           string `yaml:"dir"`
			Compress      bool   `yaml:"compress"`
			RetentionDays int    `yaml:"retention_days"`
			MaxTotalMB    int    `yaml:"max_total_mb"`
			QuotaPolicy   string `yaml:"quota_policy"`
		} `yaml:"archive"`
	} `yaml:"log"`
	Upgrade struct {
		Auto         bool `yaml:"auto"`
		AutoOptimize bool `yaml:"auto_optimize"`
		Vacuum       bool `yaml:"vacuum"`
		Program      struct {
			Enabled            bool   `yaml:"enabled"`
			Repo               string `yaml:"repo"`
			Channel            string `yaml:"channel"`
			CheckIntervalHours int    `yaml:"check_interval_hours"`
			MaxDownloadMB      int    `yaml:"max_download_mb"`
			PublicKey          string `yaml:"public_key"`
			Token              string `yaml:"token"`
		} `yaml:"program"`
	} `yaml:"upgrade"`
}

// defaults 返回配置文件缺失字段时使用的默认值。
func defaults() *fileConfig {
	fc := &fileConfig{}
	fc.Server.Port = 8080
	fc.DataDir = "data"
	// 内置 ACME 服务端默认开启；未创建端点时不会对外提供签发能力。
	fc.ACME.Enabled = true
	// 外部插件默认开启；目录为空时自动使用 <data_dir>/plugins/dns。
	fc.Plugins.Enabled = true
	fc.Plugins.MemoryLimitMB = 128
	fc.Plugins.TimeoutSeconds = 60
	fc.Plugins.MaxOutputKB = 64
	fc.Plugins.Repo.Enabled = true
	fc.Plugins.Repo.URL = "https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
	fc.Plugins.Repo.Channel = "stable"
	// 内置官方仓库公钥，默认对索引做 Ed25519 验签；清空后仅依赖 HTTPS 与资产 sha256。
	fc.Plugins.Repo.PublicKey = officialPluginRepoPublicKey
	fc.Plugins.Repo.TimeoutSeconds = 30
	fc.Plugins.Repo.MaxDownloadMB = 256
	fc.Plugins.Repo.CheckIntervalHours = 24
	// 部署插件会接收证书私钥，默认关闭，由管理员显式启用；
	// 目录为空时使用 <data_dir>/plugins/deploy，与 DNS 插件目录分开。
	fc.DeployPlugins.Enabled = false
	fc.DeployPlugins.MemoryLimitMB = 128
	fc.DeployPlugins.TimeoutSeconds = 300
	fc.DeployPlugins.MaxOutputKB = 64
	// 通知插件持有第三方推送密钥，但不接触证书私钥；默认启用，
	// 目录为空时使用 <data_dir>/plugins/notify。
	fc.NotifyPlugins.Enabled = true
	fc.NotifyPlugins.MemoryLimitMB = 128
	fc.NotifyPlugins.TimeoutSeconds = 30
	fc.NotifyPlugins.MaxOutputKB = 64
	// 底层日志默认输出到 stdout，方便 Docker 采集；需要持久化时再配置 log.file。
	fc.Log.Level = "info"
	fc.Log.Format = "text"
	fc.Log.AlsoStdout = true
	fc.Log.MaxSizeMB = 20
	fc.Log.MaxBackups = 5
	fc.Log.MaxAgeDays = 30
	fc.Log.Compress = true
	fc.Log.AccessLog = true
	// 系统日志页面默认记录 info 及以上级别，但不把 HTTP 访问日志写库。
	fc.Log.System.Enabled = true
	fc.Log.System.Level = "info"
	fc.Log.System.Access = false
	fc.Log.System.RetentionDays = 7
	fc.Log.System.MaxRows = 20000
	// 日志归档默认开启：删除/清空数据库日志前先写入数据目录。
	// 归档失败会阻止删除；默认永久保留，避免审计材料被静默清理。
	fc.Log.Archive.Enabled = true
	fc.Log.Archive.Compress = true
	fc.Log.Archive.RetentionDays = 0
	fc.Log.Archive.MaxTotalMB = 0
	fc.Log.Archive.QuotaPolicy = "fail"
	// 升级迁移默认在启动时自动执行；低成本优化随启动执行，VACUUM 只在手动优化时进行。
	fc.Upgrade.Auto = true
	fc.Upgrade.AutoOptimize = true
	fc.Upgrade.Vacuum = true
	// 程序自升级默认启用；容器只读文件系统等场景会在页面上提示不支持。
	fc.Upgrade.Program.Enabled = true
	fc.Upgrade.Program.Repo = "AXmishell/LACTSSL"
	fc.Upgrade.Program.Channel = "stable"
	fc.Upgrade.Program.CheckIntervalHours = 6
	fc.Upgrade.Program.MaxDownloadMB = 128
	return fc
}

// Load 读取当前工作目录下的 config.yaml，并确保目录与密钥存在。
func Load() (*Config, error) {
	return LoadFrom(DefaultPath)
}

// LoadFrom 读取指定路径的 config.yaml。
// 配置只来自该文件与内置默认值，不读取任何环境变量。
func LoadFrom(path string) (*Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultPath
	}

	fc, created, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	// 已存在的配置文件可能权限过宽，启动时统一收紧到 0600。
	if !created {
		_ = os.Chmod(path, 0o600)
	}

	dataDir := strings.TrimSpace(fc.DataDir)
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	// 数据目录包含数据库，仅允许当前用户访问。
	_ = os.Chmod(dataDir, 0o700)

	// 兼容旧版本：从 data/jwt.secret 迁移密钥，避免已有密文和登录令牌失效。
	legacy, _ := readSecretFile(filepath.Join(dataDir, "jwt.secret"))

	changed := created
	if strings.TrimSpace(fc.Security.JWTSecret) == "" {
		value, err := resolveSecret(legacy)
		if err != nil {
			return nil, err
		}
		fc.Security.JWTSecret = value
		changed = true
	}
	if strings.TrimSpace(fc.Security.Salt) == "" {
		value, err := resolveSecret(legacy)
		if err != nil {
			return nil, err
		}
		fc.Security.Salt = value
		changed = true
	}
	if changed {
		if created {
			err = writeTemplate(path, fc)
		} else {
			err = writeYAML(path, fc)
		}
		if err != nil {
			return nil, err
		}
	}
	// 这两项直接决定令牌签名与敏感字段加密强度，过短的密钥会使整套认证与加密失去意义。
	const minSecretLength = 32
	if len(strings.TrimSpace(fc.Security.JWTSecret)) < minSecretLength {
		return nil, fmt.Errorf("security.jwt_secret 长度至少需要 %d 个字符，请使用随机高熵值", minSecretLength)
	}
	if len(strings.TrimSpace(fc.Security.Salt)) < minSecretLength {
		return nil, fmt.Errorf("security.salt 长度至少需要 %d 个字符，请使用随机高熵值", minSecretLength)
	}

	addr, err := resolveAddr(fc)
	if err != nil {
		return nil, err
	}

	dbPath := strings.TrimSpace(fc.Database.Path)
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "lactssl.db")
	}

	entry := strings.TrimSpace(fc.Security.Entry)

	publicURL := strings.TrimRight(strings.TrimSpace(fc.Security.PublicURL), "/")
	if err := validatePublicURL(publicURL); err != nil {
		return nil, err
	}

	// 会话保持默认关闭：保持原有安全策略，重启即让旧登录全部失效。
	// 显式开启后，进程重启不清空 sessions 表，JWT 也使用固定的 jwt_secret 签名。
	sessionPersistence := fc.Security.SessionPersistence

	trustedProxies := fc.Server.TrustedProxies

	acmeCfg := ACMEConfig{
		Enabled:      fc.ACME.Enabled,
		Addr:         strings.TrimSpace(fc.ACME.Addr),
		BaseURL:      strings.TrimRight(strings.TrimSpace(fc.ACME.BaseURL), "/"),
		TLSCert:      strings.TrimSpace(fc.ACME.TLS.Cert),
		TLSKey:       strings.TrimSpace(fc.ACME.TLS.Key),
		MTLSEnabled:  fc.ACME.MTLS.Enabled,
		MTLSClientCA: strings.TrimSpace(fc.ACME.MTLS.ClientCA),
	}
	// 与主端口共用监听时，ACME 对外地址通常就是管理端的 public_url。
	// 自动回退可以避免未配置 base_url 时继续信任请求 Host。
	if acmeCfg.Enabled && acmeCfg.BaseURL == "" && acmeCfg.Addr == "" && publicURL != "" {
		acmeCfg.BaseURL = publicURL
	}
	if acmeCfg.Enabled && (acmeCfg.TLSCert == "") != (acmeCfg.TLSKey == "") {
		return nil, fmt.Errorf("acme.tls.cert 与 acme.tls.key 必须同时配置")
	}
	if acmeCfg.MTLSEnabled && (acmeCfg.TLSCert == "" || acmeCfg.TLSKey == "") {
		return nil, fmt.Errorf("启用 acme.mtls 时必须配置 acme.tls.cert 与 acme.tls.key")
	}
	if acmeCfg.MTLSEnabled && acmeCfg.MTLSClientCA == "" {
		return nil, fmt.Errorf("启用 acme.mtls 时必须配置 acme.mtls.client_ca")
	}
	if err := validateHTTPURL(acmeCfg.BaseURL, "acme.base_url"); err != nil {
		return nil, err
	}

	pluginsCfg := PluginsConfig{
		Enabled:       fc.Plugins.Enabled,
		Dir:           strings.TrimSpace(fc.Plugins.Dir),
		MemoryLimitMB: fc.Plugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.Plugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.Plugins.MaxOutputKB,
	}
	if pluginsCfg.Dir == "" {
		pluginsCfg.Dir = filepath.Join(dataDir, "plugins", "dns")
	}
	if err := validatePluginConfig("plugins", pluginsCfg); err != nil {
		return nil, err
	}

	repoCfg := PluginRepoConfig{
		Enabled:       fc.Plugins.Repo.Enabled,
		URL:           strings.TrimSpace(fc.Plugins.Repo.URL),
		Channel:       strings.TrimSpace(fc.Plugins.Repo.Channel),
		AutoUpdate:    fc.Plugins.Repo.AutoUpdate,
		PublicKey:     strings.TrimSpace(fc.Plugins.Repo.PublicKey),
		Timeout:       time.Duration(fc.Plugins.Repo.TimeoutSeconds) * time.Second,
		MaxDownloadMB: fc.Plugins.Repo.MaxDownloadMB,
		CheckInterval: time.Duration(fc.Plugins.Repo.CheckIntervalHours) * time.Hour,
	}
	if repoCfg.Channel == "" {
		repoCfg.Channel = "stable"
	}
	if repoCfg.Timeout <= 0 {
		repoCfg.Timeout = 30 * time.Second
	}
	if repoCfg.MaxDownloadMB <= 0 {
		repoCfg.MaxDownloadMB = 256
	}
	if repoCfg.CheckInterval <= 0 {
		repoCfg.CheckInterval = 24 * time.Hour
	}
	if err := validateRepoURL(repoCfg.URL, "plugins.repo.url"); err != nil {
		return nil, err
	}

	deployPluginsCfg := PluginsConfig{
		Enabled:       fc.DeployPlugins.Enabled,
		Dir:           strings.TrimSpace(fc.DeployPlugins.Dir),
		MemoryLimitMB: fc.DeployPlugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.DeployPlugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.DeployPlugins.MaxOutputKB,
	}
	if deployPluginsCfg.Dir == "" {
		deployPluginsCfg.Dir = filepath.Join(dataDir, "plugins", "deploy")
	}
	if err := validatePluginConfig("deploy_plugins", deployPluginsCfg); err != nil {
		return nil, err
	}

	notifyPluginsCfg := PluginsConfig{
		Enabled:       fc.NotifyPlugins.Enabled,
		Dir:           strings.TrimSpace(fc.NotifyPlugins.Dir),
		MemoryLimitMB: fc.NotifyPlugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.NotifyPlugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.NotifyPlugins.MaxOutputKB,
	}
	if notifyPluginsCfg.Dir == "" {
		notifyPluginsCfg.Dir = filepath.Join(dataDir, "plugins", "notify")
	}
	if err := validatePluginConfig("notify_plugins", notifyPluginsCfg); err != nil {
		return nil, err
	}

	logCfg := LogConfig{
		Level:      strings.ToLower(strings.TrimSpace(fc.Log.Level)),
		Format:     strings.ToLower(strings.TrimSpace(fc.Log.Format)),
		File:       strings.TrimSpace(fc.Log.File),
		AlsoStdout: fc.Log.AlsoStdout,
		MaxSizeMB:  fc.Log.MaxSizeMB,
		MaxBackups: fc.Log.MaxBackups,
		MaxAgeDays: fc.Log.MaxAgeDays,
		Compress:   fc.Log.Compress,
		AccessLog:  fc.Log.AccessLog,

		SystemEnabled:       fc.Log.System.Enabled,
		SystemLevel:         strings.ToLower(strings.TrimSpace(fc.Log.System.Level)),
		SystemAccess:        fc.Log.System.Access,
		SystemRetentionDays: fc.Log.System.RetentionDays,
		SystemMaxRows:       fc.Log.System.MaxRows,

		Archive: LogArchiveConfig{
			Enabled:       fc.Log.Archive.Enabled,
			Dir:           strings.TrimSpace(fc.Log.Archive.Dir),
			Compress:      fc.Log.Archive.Compress,
			RetentionDays: fc.Log.Archive.RetentionDays,
			MaxTotalMB:    fc.Log.Archive.MaxTotalMB,
			QuotaPolicy:   strings.ToLower(strings.TrimSpace(fc.Log.Archive.QuotaPolicy)),
		},
	}
	if logCfg.Archive.Dir == "" {
		logCfg.Archive.Dir = filepath.Join(dataDir, "archives", "logs")
	}
	if logCfg.Archive.QuotaPolicy == "" {
		logCfg.Archive.QuotaPolicy = "fail"
	}
	if err := validateLogConfig(logCfg); err != nil {
		return nil, err
	}

	programCfg := ProgramConfig{
		Enabled:       fc.Upgrade.Program.Enabled,
		Repo:          strings.TrimSpace(fc.Upgrade.Program.Repo),
		Channel:       strings.ToLower(strings.TrimSpace(fc.Upgrade.Program.Channel)),
		CheckInterval: time.Duration(fc.Upgrade.Program.CheckIntervalHours) * time.Hour,
		MaxDownloadMB: fc.Upgrade.Program.MaxDownloadMB,
		PublicKey:     strings.TrimSpace(fc.Upgrade.Program.PublicKey),
		Token:         strings.TrimSpace(fc.Upgrade.Program.Token),
	}
	if programCfg.Repo == "" {
		programCfg.Repo = "AXmishell/LACTSSL"
	}
	if programCfg.Channel == "" {
		programCfg.Channel = "stable"
	}
	if programCfg.CheckInterval <= 0 {
		programCfg.CheckInterval = 6 * time.Hour
	}
	if programCfg.MaxDownloadMB <= 0 {
		programCfg.MaxDownloadMB = 128
	}
	if err := validateProgramConfig(programCfg); err != nil {
		return nil, err
	}

	return &Config{
		Addr:               addr,
		DataDir:            dataDir,
		DBPath:             dbPath,
		JWTSecret:          []byte(strings.TrimSpace(fc.Security.JWTSecret)),
		EncryptionKey:      []byte(strings.TrimSpace(fc.Security.Salt)),
		Entry:              entry,
		PublicURL:          publicURL,
		SessionPersistence: sessionPersistence,
		TrustedProxies:     trustedProxies,
		ACME:               acmeCfg,
		Plugins:            pluginsCfg,
		PluginRepo:         repoCfg,
		DeployPlugins:      deployPluginsCfg,
		NotifyPlugins:      notifyPluginsCfg,
		Log:                logCfg,
		Upgrade: UpgradeConfig{
			Auto:         fc.Upgrade.Auto,
			AutoOptimize: fc.Upgrade.AutoOptimize,
			Vacuum:       fc.Upgrade.Vacuum,
			Program:      programCfg,
		},
		File:             path,
		LegacySecretFile: filepath.Join(dataDir, "jwt.secret"),
	}, nil
}

// validatePluginConfig 校验外部插件配置范围，避免误配导致子进程无法启动。
func validatePluginConfig(section string, cfg PluginsConfig) error {
	if cfg.MemoryLimitMB < 0 || cfg.MemoryLimitMB > 4096 {
		return fmt.Errorf("%s.memory_limit_mb 必须在 0-4096 之间", section)
	}
	if cfg.Timeout < time.Second || cfg.Timeout > 10*time.Minute {
		return fmt.Errorf("%s.timeout_seconds 必须在 1-600 之间", section)
	}
	if cfg.MaxOutputKB < 1 || cfg.MaxOutputKB > 1024 {
		return fmt.Errorf("%s.max_output_kb 必须在 1-1024 之间", section)
	}
	return nil
}

// validateProgramConfig 校验程序自升级配置。
func validateProgramConfig(cfg ProgramConfig) error {
	if cfg.Channel != "stable" && cfg.Channel != "beta" {
		return fmt.Errorf("upgrade.program.channel 必须是 stable 或 beta")
	}
	repo := strings.TrimSpace(cfg.Repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(repo, " \t\r\n?#") || strings.HasPrefix(repo, ".") {
		return fmt.Errorf("upgrade.program.repo 必须是 owner/repo 格式")
	}
	if cfg.CheckInterval < time.Hour || cfg.CheckInterval > 7*24*time.Hour {
		return fmt.Errorf("upgrade.program.check_interval_hours 必须在 1-168 之间")
	}
	if cfg.MaxDownloadMB < 8 || cfg.MaxDownloadMB > 2048 {
		return fmt.Errorf("upgrade.program.max_download_mb 必须在 8-2048 之间")
	}
	if value := strings.TrimSpace(cfg.PublicKey); value != "" {
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(value)
		}
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("upgrade.program.public_key 必须是 base64 编码的 Ed25519 公钥")
		}
	}
	return nil
}

// validateLogConfig 校验底层日志配置，避免非法值在启动后才暴露。
func validateLogConfig(cfg LogConfig) error {
	switch cfg.Level {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("log.level 必须是 debug、info、warn 或 error")
	}
	switch cfg.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format 必须是 text 或 json")
	}
	if cfg.MaxSizeMB < 1 || cfg.MaxSizeMB > 1024 {
		return fmt.Errorf("log.max_size_mb 必须在 1-1024 之间")
	}
	if cfg.MaxBackups < 0 || cfg.MaxBackups > 100 {
		return fmt.Errorf("log.max_backups 必须在 0-100 之间")
	}
	if cfg.MaxAgeDays < 0 || cfg.MaxAgeDays > 3650 {
		return fmt.Errorf("log.max_age_days 必须在 0-3650 之间")
	}
	switch cfg.SystemLevel {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("log.system.level 必须是 debug、info、warn 或 error")
	}
	if cfg.SystemRetentionDays < 1 || cfg.SystemRetentionDays > 365 {
		return fmt.Errorf("log.system.retention_days 必须在 1-365 之间")
	}
	if cfg.SystemMaxRows < 100 || cfg.SystemMaxRows > 1000000 {
		return fmt.Errorf("log.system.max_rows 必须在 100-1000000 之间")
	}
	if cfg.Archive.RetentionDays < 0 || cfg.Archive.RetentionDays > 3650 {
		return fmt.Errorf("log.archive.retention_days 必须在 0-3650 之间")
	}
	if cfg.Archive.MaxTotalMB < 0 || cfg.Archive.MaxTotalMB > 1048576 {
		return fmt.Errorf("log.archive.max_total_mb 必须在 0-1048576 之间")
	}
	switch cfg.Archive.QuotaPolicy {
	case "", "fail", "prune_oldest":
	default:
		return fmt.Errorf("log.archive.quota_policy 必须是 fail 或 prune_oldest")
	}
	return nil
}

// loadFile 读取配置文件；文件不存在时返回默认配置。
func loadFile(path string) (*fileConfig, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults(), true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	fc := defaults()
	if err := yaml.Unmarshal(data, fc); err != nil {
		return nil, false, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	return fc, false, nil
}

// ValidateFile 校验一份配置文件是否能在启动阶段被安全加载。
// 该函数不做任何写入或密钥生成，供备份恢复等场景在落盘前做完整预检，
// 避免把会导致下次启动失败的配置写入 restore-pending。
func ValidateFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	fc := defaults()
	if err := yaml.Unmarshal(data, fc); err != nil {
		return fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	return validateFileValues(fc)
}

func validateFileValues(fc *fileConfig) error {
	const minSecretLength = 32
	// 为空表示启动时自动生成；非空则必须达到最小长度，否则 Load 会直接拒绝启动。
	if value := strings.TrimSpace(fc.Security.JWTSecret); value != "" && len(value) < minSecretLength {
		return fmt.Errorf("security.jwt_secret 长度至少需要 %d 个字符", minSecretLength)
	}
	if value := strings.TrimSpace(fc.Security.Salt); value != "" && len(value) < minSecretLength {
		return fmt.Errorf("security.salt 长度至少需要 %d 个字符", minSecretLength)
	}
	if value := strings.TrimSpace(fc.Security.Entry); value != "" {
		if _, err := util.ValidateEntry(value); err != nil {
			return err
		}
	}
	if err := validatePublicURL(strings.TrimRight(strings.TrimSpace(fc.Security.PublicURL), "/")); err != nil {
		return err
	}
	if fc.Server.Port < 0 || fc.Server.Port > 65535 {
		return fmt.Errorf("配置项 server.port 不正确: %d", fc.Server.Port)
	}
	if err := validateListenAddr(strings.TrimSpace(fc.Server.Addr), "server.addr"); err != nil {
		return err
	}
	if err := validateListenAddr(strings.TrimSpace(fc.ACME.Addr), "acme.addr"); err != nil {
		return err
	}
	if err := validateHTTPURL(strings.TrimRight(strings.TrimSpace(fc.ACME.BaseURL), "/"), "acme.base_url"); err != nil {
		return err
	}
	if (strings.TrimSpace(fc.ACME.TLS.Cert) == "") != (strings.TrimSpace(fc.ACME.TLS.Key) == "") {
		return fmt.Errorf("acme.tls.cert 与 acme.tls.key 必须同时配置")
	}
	if fc.ACME.MTLS.Enabled {
		if strings.TrimSpace(fc.ACME.TLS.Cert) == "" || strings.TrimSpace(fc.ACME.TLS.Key) == "" {
			return fmt.Errorf("启用 acme.mtls 时必须配置 acme.tls.cert 与 acme.tls.key")
		}
		if strings.TrimSpace(fc.ACME.MTLS.ClientCA) == "" {
			return fmt.Errorf("启用 acme.mtls 时必须配置 acme.mtls.client_ca")
		}
	}
	if err := validatePluginConfig("plugins", PluginsConfig{
		Enabled:       fc.Plugins.Enabled,
		Dir:           strings.TrimSpace(fc.Plugins.Dir),
		MemoryLimitMB: fc.Plugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.Plugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.Plugins.MaxOutputKB,
	}); err != nil {
		return err
	}
	if err := validatePluginConfig("deploy_plugins", PluginsConfig{
		Enabled:       fc.DeployPlugins.Enabled,
		Dir:           strings.TrimSpace(fc.DeployPlugins.Dir),
		MemoryLimitMB: fc.DeployPlugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.DeployPlugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.DeployPlugins.MaxOutputKB,
	}); err != nil {
		return err
	}
	if err := validatePluginConfig("notify_plugins", PluginsConfig{
		Enabled:       fc.NotifyPlugins.Enabled,
		Dir:           strings.TrimSpace(fc.NotifyPlugins.Dir),
		MemoryLimitMB: fc.NotifyPlugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.NotifyPlugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.NotifyPlugins.MaxOutputKB,
	}); err != nil {
		return err
	}
	if err := validateLogConfig(LogConfig{
		Level:               strings.ToLower(strings.TrimSpace(fc.Log.Level)),
		Format:              strings.ToLower(strings.TrimSpace(fc.Log.Format)),
		MaxSizeMB:           fc.Log.MaxSizeMB,
		MaxBackups:          fc.Log.MaxBackups,
		MaxAgeDays:          fc.Log.MaxAgeDays,
		SystemLevel:         strings.ToLower(strings.TrimSpace(fc.Log.System.Level)),
		SystemRetentionDays: fc.Log.System.RetentionDays,
		SystemMaxRows:       fc.Log.System.MaxRows,
		Archive: LogArchiveConfig{
			RetentionDays: fc.Log.Archive.RetentionDays,
			MaxTotalMB:    fc.Log.Archive.MaxTotalMB,
			QuotaPolicy:   strings.ToLower(strings.TrimSpace(fc.Log.Archive.QuotaPolicy)),
		},
	}); err != nil {
		return err
	}
	programCfg := ProgramConfig{
		Repo:          strings.TrimSpace(fc.Upgrade.Program.Repo),
		Channel:       strings.ToLower(strings.TrimSpace(fc.Upgrade.Program.Channel)),
		CheckInterval: time.Duration(fc.Upgrade.Program.CheckIntervalHours) * time.Hour,
		MaxDownloadMB: fc.Upgrade.Program.MaxDownloadMB,
		PublicKey:     strings.TrimSpace(fc.Upgrade.Program.PublicKey),
	}
	if programCfg.Repo == "" {
		programCfg.Repo = "AXmishell/LACTSSL"
	}
	if programCfg.Channel == "" {
		programCfg.Channel = "stable"
	}
	if programCfg.CheckInterval <= 0 {
		programCfg.CheckInterval = 6 * time.Hour
	}
	if programCfg.MaxDownloadMB <= 0 {
		programCfg.MaxDownloadMB = 128
	}
	return validateProgramConfig(programCfg)
}

// validatePublicURL 校验对外访问地址：必须是 http(s) 且不能包含路径、查询参数或片段。
func validatePublicURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("security.public_url 必须是有效的 http 或 https 地址，且不能包含查询参数或片段")
	}
	if strings.TrimSpace(parsed.Path) != "" && strings.TrimRight(parsed.Path, "/") != "" {
		return fmt.Errorf("security.public_url 不能包含路径")
	}
	return nil
}

// validateHTTPURL 校验 ACME 等服务的 http(s) 基地址，允许带路径前缀。
func validateHTTPURL(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s 必须是有效的 http 或 https 地址", field)
	}
	return nil
}

// validateRepoURL 校验插件仓库地址，支持 http、https 与本地 file:// 镜像。
func validateRepoURL(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s 不是有效地址", field)
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("%s 必须是有效的 http 或 https 地址", field)
		}
	case "file":
		if parsed.Path == "" || parsed.Host != "" {
			return fmt.Errorf("%s 的 file 地址必须包含绝对路径", field)
		}
	default:
		return fmt.Errorf("%s 只支持 http、https 或 file", field)
	}
	return nil
}

// validateListenAddr 校验 host:port 形式的监听地址。
func validateListenAddr(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return fmt.Errorf("%s 格式不正确，应为 host:port: %s", field, value)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("%s 端口不正确: %s", field, value)
	}
	if host != "" && strings.ContainsAny(host, " /\\\t\r\n") {
		return fmt.Errorf("%s 主机名不正确: %s", field, value)
	}
	return nil
}

// resolveAddr 从配置文件计算监听地址。
func resolveAddr(fc *fileConfig) (string, error) {
	if addr := strings.TrimSpace(fc.Server.Addr); addr != "" {
		return addr, nil
	}
	port := fc.Server.Port
	if port == 0 {
		port = 8080
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("配置项 server.port 不正确: %d", port)
	}
	return net.JoinHostPort(strings.TrimSpace(fc.Server.Host), strconv.Itoa(port)), nil
}

// resolveSecret 优先复用旧版密钥文件，否则生成新的随机密钥。
func resolveSecret(legacy string) (string, error) {
	if legacy != "" {
		return legacy, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成密钥失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// readSecretFile 读取旧版 data/jwt.secret。
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if len(value) < 32 {
		return "", nil
	}
	return value, nil
}

// writeTemplate 首次生成配置文件时写入带注释的模板。
func writeTemplate(path string, fc *fileConfig) error {
	content := fmt.Sprintf(`# LACTSSL 配置文件
# 默认读取当前工作目录的 config.yaml；也可在启动时用 -config 指定其他路径。
# 修改后需重启服务生效；所有运行配置只从本文件读取。
# 注意：jwt_secret 与 salt 用于令牌签名和敏感配置加密，请勿泄露并做好备份。

server:
  # 完整监听地址，例如 ":8080"、"127.0.0.1:8080"；留空则使用 host + port
  addr: %s
  # 监听网卡，留空表示监听所有网卡
  host: %s
  port: %d
  # 可信反向代理 CIDR；留空表示不信任任何代理头（直连部署推荐）
  # 例如：["127.0.0.1", "10.0.0.0/8"]
  trusted_proxies: []

# 数据目录：存放 SQLite 数据库
data_dir: %s

database:
  # 留空表示 <data_dir>/lactssl.db
  path: %s

log:
  # 进程日志级别：debug、info、warn、error
  level: %s
  # 输出格式：text 或 json
  format: %s
  # 日志文件；留空仅输出到 stdout。相对路径基于当前工作目录。
  file: %s
  # 写入文件时是否仍输出到 stdout
  also_stdout: %t
  # 单文件大小上限（MB），超过后轮转
  max_size_mb: %d
  # 保留历史文件数量，0 表示不限制
  max_backups: %d
  # 历史文件保留天数，0 表示不限制
  max_age_days: %d
  # 是否压缩历史文件
  compress: %t
  # 是否记录 HTTP 访问日志（不记录查询参数与门禁入口）
  access_log: %t
  system:
    # 是否把进程日志写入数据库，供「系统日志」页面查看
    enabled: %t
    # 写入数据库的最低级别（不能低于上面的 log.level）
    level: %s
    # 是否把 HTTP 访问日志也写入数据库；高频请求会快速占用数据库
    access: %t
    # 系统日志保留天数
    retention_days: %d
    # 系统日志最大行数，超出后删除最旧记录
    max_rows: %d
  archive:
    # 删除/清空日志前是否先归档；归档失败会阻止删除
    enabled: %t
    # 归档目录；留空表示 <data_dir>/archives/logs
    dir: %s
    # 是否 gzip 压缩
    compress: %t
    # 归档文件保留天数，0 表示永久保留
    retention_days: %d
    # 归档目录容量上限（MB），0 表示不限制
    max_total_mb: %d
    # 容量超限策略：fail（拒绝归档并阻止删除）或 prune_oldest（删除最旧归档）
    quota_policy: %s

upgrade:
  # 启动时自动执行数据库/配置的历史遗留迁移；关闭后可在 /upgrade 页面手动执行。
  auto: %t
  # 启动时执行低成本优化（补充索引、更新统计、WAL checkpoint），不执行 VACUUM。
  auto_optimize: %t
  # 在 /upgrade 页面手动优化时允许 VACUUM 回收磁盘空间（会短暂独占数据库）。
  vacuum: %t
  # 程序自升级：从 GitHub Release 下载并替换当前二进制。
  # 只读容器（例如官方 compose 的 read_only: true）会在页面上提示不支持。
  program:
    enabled: %t
    # GitHub 仓库，格式为 owner/repo
    repo: %s
    # stable 仅正式版；beta 包含预发布版本
    channel: %s
    # 自动检查新版本的最小间隔（小时）
    check_interval_hours: %d
    # 发布包下载大小上限（MB）
    max_download_mb: %d
    # 发布校验和签名公钥（base64 Ed25519）；留空表示仅依赖 HTTPS 与 SHA-256
    public_key: %s
    # 可选的 GitHub API token，用于提高版本检查的速率上限；留空表示匿名请求。
    token: %s

security:
  # 门禁入口；留空则首次启动随机生成并保存到数据库
  entry: %s
  # 对外访问地址，例如 https://ssl.example.com；用于邮件中的重置链接等绝对地址。
  # 密码重置等邮件链接必须基于该固定地址生成；留空时仅允许本机回环请求按 Host 推导，远程请求会被拒绝，避免 Host 头注入。
  public_url: %s
  # 会话保持（默认 false）：为 true 时进程重启后保留登录会话，无需重新登录。
  # 开启后 JWT 签名不再随启动轮换，请妥善保管 jwt_secret。
  session_persistence: %t
  # JWT 签名密钥（加盐）；留空则自动生成并写回本文件。手动配置时至少 32 个字符。
  jwt_secret: %s
  # 数据库敏感字段加密盐（AES-256-GCM）；留空则自动生成并写回本文件。手动配置时至少 32 个字符。
  salt: %s

acme:
  # 是否启用内置 ACME 服务端（默认开启）；端点、EAB、域名白名单在管理接口中配置
  enabled: %t
  # 独立监听地址；留空则挂载到主端口 /acme
  addr: %s
  # 对外访问地址，例如 https://pki.example.com:14000；留空则按请求 Host 推导
  base_url: %s
  tls:
    # 直接由 ACME 监听提供 TLS 时填写；反向代理终止 TLS 时留空
    cert: %s
    key: %s
  mtls:
    # 启用 mTLS 时必须同时配置上面的 tls.cert / tls.key
    enabled: %t
    client_ca: %s

plugins:
  # 是否加载 DNS 插件
  enabled: %t
  # 插件目录；留空表示 <data_dir>/plugins/dns
  dir: %s
  # 单个 helper 进程常驻内存上限（MB），0 表示不限制
  memory_limit_mb: %d
  # present/cleanup 单次执行超时（秒）
  timeout_seconds: %d
  # helper 输出日志上限（KB）
  max_output_kb: %d
  repo:
    # 插件仓库默认启用；首次进入插件页会拉取索引
    enabled: %t
    # 索引地址；支持 http、https 与 file:// 离线镜像
    url: %s
    # stable 或 beta
    channel: %s
    # 是否自动更新已安装插件
    auto_update: %t
    # 索引签名公钥（base64 Ed25519）；留空时只校验 HTTPS 与 asset sha256
    public_key: %s
    # 拉取索引/下载插件超时（秒）
    timeout_seconds: %d
    # 单个插件包大小上限（MB）
    max_download_mb: %d
    # 自动检查间隔（小时）
    check_interval_hours: %d

deploy_plugins:
  # 是否加载外部证书部署插件；插件会接收证书私钥，默认关闭
  enabled: %t
  # 插件目录；留空表示 <data_dir>/plugins/deploy（与 DNS 插件目录分开）
  dir: %s
  # 单个 helper 进程常驻内存上限（MB），0 表示不限制
  memory_limit_mb: %d
  # deploy 单次执行超时（秒）；覆盖上传大文件时可适当调大
  timeout_seconds: %d
  # helper 输出日志上限（KB）
  max_output_kb: %d

notify_plugins:
  # 是否加载外部通知插件；主程序只内置邮件通知
  enabled: %t
  # 插件目录；留空表示 <data_dir>/plugins/notify（与 DNS/部署插件目录分开）
  dir: %s
  # 单个 helper 进程常驻内存上限（MB），0 表示不限制
  memory_limit_mb: %d
  # send 单次执行超时（秒）
  timeout_seconds: %d
  # helper 输出日志上限（KB）
  max_output_kb: %d
`,
		quote(fc.Server.Addr),
		quote(fc.Server.Host),
		fc.Server.Port,
		quote(fc.DataDir),
		quote(fc.Database.Path),
		quote(fc.Log.Level),
		quote(fc.Log.Format),
		quote(fc.Log.File),
		fc.Log.AlsoStdout,
		fc.Log.MaxSizeMB,
		fc.Log.MaxBackups,
		fc.Log.MaxAgeDays,
		fc.Log.Compress,
		fc.Log.AccessLog,
		fc.Log.System.Enabled,
		quote(fc.Log.System.Level),
		fc.Log.System.Access,
		fc.Log.System.RetentionDays,
		fc.Log.System.MaxRows,
		fc.Log.Archive.Enabled,
		quote(fc.Log.Archive.Dir),
		fc.Log.Archive.Compress,
		fc.Log.Archive.RetentionDays,
		fc.Log.Archive.MaxTotalMB,
		quote(fc.Log.Archive.QuotaPolicy),
		fc.Upgrade.Auto,
		fc.Upgrade.AutoOptimize,
		fc.Upgrade.Vacuum,
		fc.Upgrade.Program.Enabled,
		quote(fc.Upgrade.Program.Repo),
		quote(fc.Upgrade.Program.Channel),
		fc.Upgrade.Program.CheckIntervalHours,
		fc.Upgrade.Program.MaxDownloadMB,
		quote(fc.Upgrade.Program.PublicKey),
		quote(fc.Upgrade.Program.Token),
		quote(fc.Security.Entry),
		quote(fc.Security.PublicURL),
		fc.Security.SessionPersistence,
		quote(fc.Security.JWTSecret),
		quote(fc.Security.Salt),
		fc.ACME.Enabled,
		quote(fc.ACME.Addr),
		quote(fc.ACME.BaseURL),
		quote(fc.ACME.TLS.Cert),
		quote(fc.ACME.TLS.Key),
		fc.ACME.MTLS.Enabled,
		quote(fc.ACME.MTLS.ClientCA),
		fc.Plugins.Enabled,
		quote(fc.Plugins.Dir),
		fc.Plugins.MemoryLimitMB,
		fc.Plugins.TimeoutSeconds,
		fc.Plugins.MaxOutputKB,
		fc.Plugins.Repo.Enabled,
		quote(fc.Plugins.Repo.URL),
		quote(fc.Plugins.Repo.Channel),
		fc.Plugins.Repo.AutoUpdate,
		quote(fc.Plugins.Repo.PublicKey),
		fc.Plugins.Repo.TimeoutSeconds,
		fc.Plugins.Repo.MaxDownloadMB,
		fc.Plugins.Repo.CheckIntervalHours,
		fc.DeployPlugins.Enabled,
		quote(fc.DeployPlugins.Dir),
		fc.DeployPlugins.MemoryLimitMB,
		fc.DeployPlugins.TimeoutSeconds,
		fc.DeployPlugins.MaxOutputKB,
		fc.NotifyPlugins.Enabled,
		quote(fc.NotifyPlugins.Dir),
		fc.NotifyPlugins.MemoryLimitMB,
		fc.NotifyPlugins.TimeoutSeconds,
		fc.NotifyPlugins.MaxOutputKB,
	)
	return writeFile(path, []byte(content))
}

// writeYAML 在补齐空密钥后回写配置文件。
// 优先就地修改 yaml.Node，保留用户注释与未知字段；解析失败时才退回结构体序列化。
func writeYAML(path string, fc *fileConfig) error {
	if data, err := os.ReadFile(path); err == nil {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err == nil {
			if root, err := documentMapping(&doc); err == nil {
				security, err := ensureMapping(root, "security")
				if err == nil {
					err = setScalar(security, "jwt_secret", "!!str", fc.Security.JWTSecret)
				}
				if err == nil {
					err = setScalar(security, "salt", "!!str", fc.Security.Salt)
				}
				if err == nil {
					var buf bytes.Buffer
					encoder := yaml.NewEncoder(&buf)
					encoder.SetIndent(2)
					encodeErr := encoder.Encode(&doc)
					if encodeErr == nil {
						encodeErr = encoder.Close()
					}
					if encodeErr == nil {
						if writeErr := writeFile(path, buf.Bytes()); writeErr == nil {
							return nil
						}
					}
				}
			}
		}
	}
	data, err := yaml.Marshal(fc)
	if err != nil {
		return fmt.Errorf("生成配置文件失败: %w", err)
	}
	return writeFile(path, data)
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		// 配置文件包含密钥，仅允许当前用户访问。
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
		_ = os.Chmod(dir, 0o700)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入配置文件 %s 失败: %w", path, err)
	}
	// 已存在的配置文件可能带着更宽松的权限，这里显式收紧。
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置配置文件权限失败: %w", err)
	}
	return nil
}

// quote 生成安全的 YAML 双引号字符串。
func quote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
