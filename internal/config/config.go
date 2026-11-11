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

	"gopkg.in/yaml.v3"
)

// DefaultPath 是默认配置文件路径（相对当前工作目录）。
const DefaultPath = "config.yaml"

// Config 保存服务运行所需的全部基础配置。
type Config struct {
	Addr           string     // 监听地址，例如 :8080
	DataDir        string     // 数据目录
	DBPath         string     // SQLite 数据库文件
	JWTSecret      []byte     // JWT 签名密钥
	EncryptionKey  []byte     // 数据库敏感配置加密密钥（盐）
	Entry          string     // 初始安全入口（可通过环境变量或配置文件覆盖）
	PublicURL      string     // 对外访问地址（用于邮件链接等绝对地址）
	TrustedProxies []string   // 可信反向代理 CIDR；为空表示不信任任何代理头
	ACME           ACMEConfig // 内置 ACME 服务端配置
	File           string     // 实际使用的配置文件路径
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
}

// defaults 返回配置文件缺失字段时使用的默认值。
func defaults() *fileConfig {
	fc := &fileConfig{}
	fc.Server.Port = 8080
	fc.DataDir = "data"
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
	if publicURL != "" {
		parsed, err := url.Parse(publicURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("security.public_url 必须是有效的 http 或 https 地址，且不能包含查询参数或片段")
		}
		if strings.TrimSpace(parsed.Path) != "" && strings.TrimRight(parsed.Path, "/") != "" {
			return nil, fmt.Errorf("security.public_url 不能包含路径")
		}
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
	if acmeCfg.Enabled && (acmeCfg.TLSCert == "") != (acmeCfg.TLSKey == "") {
		return nil, fmt.Errorf("acme.tls.cert 与 acme.tls.key 必须同时配置")
	}
	if acmeCfg.MTLSEnabled && (acmeCfg.TLSCert == "" || acmeCfg.TLSKey == "") {
		return nil, fmt.Errorf("启用 acme.mtls 时必须配置 acme.tls.cert 与 acme.tls.key")
	}
	if acmeCfg.MTLSEnabled && acmeCfg.MTLSClientCA == "" {
		return nil, fmt.Errorf("启用 acme.mtls 时必须配置 acme.mtls.client_ca")
	}
	if acmeCfg.BaseURL != "" {
		parsed, err := url.Parse(acmeCfg.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("acme.base_url 必须是有效的 http 或 https 地址")
		}
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
		File:           path,
	}, nil
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

security:
  # 安全入口；留空则首次启动随机生成并保存到数据库
  entry: %s
  # 对外访问地址，例如 https://ssl.example.com；用于邮件中的重置链接等绝对地址。
  # 密码重置等邮件链接必须基于该固定地址生成；留空时仅允许本机回环请求按 Host 推导，远程请求会被拒绝，避免 Host 头注入。
  public_url: %s
  # JWT 签名密钥（加盐）；留空则自动生成并写回本文件
  jwt_secret: %s
  # 数据库敏感字段加密盐（AES-256-GCM）；留空则自动生成并写回本文件
  salt: %s

acme:
  # 是否启用内置 ACME 服务端；端点、EAB、域名白名单在管理接口中配置
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
`,
		quote(fc.Server.Addr),
		quote(fc.Server.Host),
		fc.Server.Port,
		quote(fc.DataDir),
		quote(fc.Database.Path),
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
