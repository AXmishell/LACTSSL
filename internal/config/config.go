// Package config 负责加载 config.yaml 运行配置。
package config

import (
	"crypto/rand"
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

// Config 保存服务运行所需的全部基础配置。
type Config struct {
	Addr           string        // 监听地址，例如 :8080
	DataDir        string        // 数据目录
	DBPath         string        // SQLite 数据库文件
	JWTSecret      []byte        // JWT 签名密钥
	EncryptionKey  []byte        // 数据库敏感配置加密密钥（盐）
	Entry          string        // 初始安全入口（可通过环境变量或配置文件覆盖）
	PublicURL      string        // 对外访问地址（用于邮件链接等绝对地址）
	TrustedProxies []string      // 可信反向代理 CIDR；为空表示不信任任何代理头
	ACME           ACMEConfig    // 内置 ACME 服务端配置
	Plugins        PluginsConfig // 外部 DNS 插件配置
	DeployPlugins  PluginsConfig // 外部证书部署插件配置
	Log            LogConfig     // 进程底层日志配置
	File           string        // 实际使用的配置文件路径
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
		Entry     string `yaml:"entry"`
		PublicURL string `yaml:"public_url"`
		JWTSecret string `yaml:"jwt_secret"`
		Salt      string `yaml:"salt"`
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
	} `yaml:"plugins"`
	DeployPlugins struct {
		Enabled        bool   `yaml:"enabled"`
		Dir            string `yaml:"dir"`
		MemoryLimitMB  int    `yaml:"memory_limit_mb"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
		MaxOutputKB    int    `yaml:"max_output_kb"`
	} `yaml:"deploy_plugins"`
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
	} `yaml:"log"`
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
	// 部署插件会接收证书私钥，默认关闭，由管理员显式启用；
	// 目录为空时使用 <data_dir>/plugins/deploy，与 DNS 插件目录分开。
	fc.DeployPlugins.Enabled = false
	fc.DeployPlugins.MemoryLimitMB = 128
	fc.DeployPlugins.TimeoutSeconds = 300
	fc.DeployPlugins.MaxOutputKB = 64
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
	return fc
}

// Load 读取 YAML 配置与环境变量，并确保目录与密钥存在。
// 优先级：环境变量 > config.yaml > 内置默认值。
func Load() (*Config, error) {
	path := strings.TrimSpace(os.Getenv("LACTSSL_CONFIG"))
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

	dataDir := strings.TrimSpace(os.Getenv("LACTSSL_DATA"))
	if dataDir == "" {
		dataDir = strings.TrimSpace(fc.DataDir)
	}
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

	entry := strings.TrimSpace(os.Getenv("LACTSSL_ENTRY"))
	if entry == "" {
		entry = strings.TrimSpace(fc.Security.Entry)
	}

	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LACTSSL_PUBLIC_URL")), "/")
	if publicURL == "" {
		publicURL = strings.TrimRight(strings.TrimSpace(fc.Security.PublicURL), "/")
	}
	if err := validatePublicURL(publicURL); err != nil {
		return nil, err
	}

	trustedProxies := fc.Server.TrustedProxies
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_TRUSTED_PROXIES")); raw != "" {
		trustedProxies = splitList(raw)
	}

	acmeCfg := ACMEConfig{
		Enabled:      fc.ACME.Enabled,
		Addr:         strings.TrimSpace(fc.ACME.Addr),
		BaseURL:      strings.TrimRight(strings.TrimSpace(fc.ACME.BaseURL), "/"),
		TLSCert:      strings.TrimSpace(fc.ACME.TLS.Cert),
		TLSKey:       strings.TrimSpace(fc.ACME.TLS.Key),
		MTLSEnabled:  fc.ACME.MTLS.Enabled,
		MTLSClientCA: strings.TrimSpace(fc.ACME.MTLS.ClientCA),
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_ADDR")); raw != "" {
		acmeCfg.Addr = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_BASE_URL")); raw != "" {
		acmeCfg.BaseURL = strings.TrimRight(raw, "/")
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_TLS_CERT")); raw != "" {
		acmeCfg.TLSCert = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_TLS_KEY")); raw != "" {
		acmeCfg.TLSKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_MTLS_CLIENT_CA")); raw != "" {
		acmeCfg.MTLSClientCA = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_ENABLED")); raw != "" {
		acmeCfg.Enabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_ACME_MTLS_ENABLED")); raw != "" {
		acmeCfg.MTLSEnabled = parseBool(raw)
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
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_PLUGINS_ENABLED")); raw != "" {
		pluginsCfg.Enabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_PLUGINS_DIR")); raw != "" {
		pluginsCfg.Dir = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_PLUGINS_MEMORY_MB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			pluginsCfg.MemoryLimitMB = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_PLUGINS_TIMEOUT_SECONDS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			pluginsCfg.Timeout = time.Duration(value) * time.Second
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_PLUGINS_MAX_OUTPUT_KB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			pluginsCfg.MaxOutputKB = value
		}
	}
	if pluginsCfg.Dir == "" {
		pluginsCfg.Dir = filepath.Join(dataDir, "plugins", "dns")
	}
	if err := validatePluginConfig("plugins", pluginsCfg); err != nil {
		return nil, err
	}

	deployPluginsCfg := PluginsConfig{
		Enabled:       fc.DeployPlugins.Enabled,
		Dir:           strings.TrimSpace(fc.DeployPlugins.Dir),
		MemoryLimitMB: fc.DeployPlugins.MemoryLimitMB,
		Timeout:       time.Duration(fc.DeployPlugins.TimeoutSeconds) * time.Second,
		MaxOutputKB:   fc.DeployPlugins.MaxOutputKB,
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_DEPLOY_PLUGINS_ENABLED")); raw != "" {
		deployPluginsCfg.Enabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_DEPLOY_PLUGINS_DIR")); raw != "" {
		deployPluginsCfg.Dir = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_DEPLOY_PLUGINS_MEMORY_MB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			deployPluginsCfg.MemoryLimitMB = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_DEPLOY_PLUGINS_TIMEOUT_SECONDS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			deployPluginsCfg.Timeout = time.Duration(value) * time.Second
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_DEPLOY_PLUGINS_MAX_OUTPUT_KB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			deployPluginsCfg.MaxOutputKB = value
		}
	}
	if deployPluginsCfg.Dir == "" {
		deployPluginsCfg.Dir = filepath.Join(dataDir, "plugins", "deploy")
	}
	if err := validatePluginConfig("deploy_plugins", deployPluginsCfg); err != nil {
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
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_LEVEL")); raw != "" {
		logCfg.Level = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_FORMAT")); raw != "" {
		logCfg.Format = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_FILE")); raw != "" {
		logCfg.File = raw
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_ALSO_STDOUT")); raw != "" {
		logCfg.AlsoStdout = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_MAX_SIZE_MB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			logCfg.MaxSizeMB = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_MAX_BACKUPS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			logCfg.MaxBackups = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_MAX_AGE_DAYS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			logCfg.MaxAgeDays = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_COMPRESS")); raw != "" {
		logCfg.Compress = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_ACCESS")); raw != "" {
		logCfg.AccessLog = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_SYSTEM_ENABLED")); raw != "" {
		logCfg.SystemEnabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_SYSTEM_LEVEL")); raw != "" {
		logCfg.SystemLevel = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_SYSTEM_ACCESS")); raw != "" {
		logCfg.SystemAccess = parseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_SYSTEM_RETENTION_DAYS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			logCfg.SystemRetentionDays = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LACTSSL_LOG_SYSTEM_MAX_ROWS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			logCfg.SystemMaxRows = value
		}
	}
	if err := validateLogConfig(logCfg); err != nil {
		return nil, err
	}

	return &Config{
		Addr:           addr,
		DataDir:        dataDir,
		DBPath:         dbPath,
		JWTSecret:      []byte(strings.TrimSpace(fc.Security.JWTSecret)),
		EncryptionKey:  []byte(strings.TrimSpace(fc.Security.Salt)),
		Entry:          entry,
		PublicURL:      publicURL,
		TrustedProxies: trustedProxies,
		ACME:           acmeCfg,
		Plugins:        pluginsCfg,
		DeployPlugins:  deployPluginsCfg,
		Log:            logCfg,
		File:           path,
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
	return nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// splitList 解析逗号分隔的配置项，忽略空白项。
func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
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
	if err := validateLogConfig(LogConfig{
		Level:               strings.ToLower(strings.TrimSpace(fc.Log.Level)),
		Format:              strings.ToLower(strings.TrimSpace(fc.Log.Format)),
		MaxSizeMB:           fc.Log.MaxSizeMB,
		MaxBackups:          fc.Log.MaxBackups,
		MaxAgeDays:          fc.Log.MaxAgeDays,
		SystemLevel:         strings.ToLower(strings.TrimSpace(fc.Log.System.Level)),
		SystemRetentionDays: fc.Log.System.RetentionDays,
		SystemMaxRows:       fc.Log.System.MaxRows,
	}); err != nil {
		return err
	}
	return nil
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

// resolveAddr 计算监听地址，环境变量优先于配置文件。
func resolveAddr(fc *fileConfig) (string, error) {
	if addr := strings.TrimSpace(os.Getenv("LACTSSL_ADDR")); addr != "" {
		return addr, nil
	}
	if port := strings.TrimSpace(os.Getenv("LACTSSL_PORT")); port != "" {
		p, err := parsePort(port)
		if err != nil {
			return "", err
		}
		return ":" + strconv.Itoa(p), nil
	}
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

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口格式不正确: %s", value)
	}
	return port, nil
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
# 修改后需重启服务生效；环境变量优先级高于本文件。
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
  # 是否记录 HTTP 访问日志（不记录查询参数与安全入口）
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

security:
  # 安全入口；留空则首次启动随机生成并保存到数据库
  entry: %s
  # 对外访问地址，例如 https://ssl.example.com；用于邮件中的重置链接等绝对地址。
  # 密码重置等邮件链接必须基于该固定地址生成；留空时仅允许本机回环请求按 Host 推导，远程请求会被拒绝，避免 Host 头注入。
  public_url: %s
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
    cert: %s
    key: %s
  mtls:
    enabled: %t
    client_ca: %s

plugins:
  # 是否加载外部 DNS exec helper 插件
  enabled: %t
  # 插件目录；留空表示 <data_dir>/plugins/dns
  dir: %s
  # 单个 helper 进程常驻内存上限（MB），0 表示不限制
  memory_limit_mb: %d
  # present/cleanup 单次执行超时（秒）
  timeout_seconds: %d
  # helper 输出日志上限（KB）
  max_output_kb: %d

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
		quote(fc.Security.Entry),
		quote(fc.Security.PublicURL),
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
		fc.DeployPlugins.Enabled,
		quote(fc.DeployPlugins.Dir),
		fc.DeployPlugins.MemoryLimitMB,
		fc.DeployPlugins.TimeoutSeconds,
		fc.DeployPlugins.MaxOutputKB,
	)
	return writeFile(path, []byte(content))
}

// writeYAML 在补齐空密钥后回写配置文件（此时会丢失模板注释）。
func writeYAML(path string, fc *fileConfig) error {
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
