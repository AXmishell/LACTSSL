package db

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// isMySQLDuplicateIndexError 判断是否为重复索引名（MySQL 1061）。
func isMySQLDuplicateIndexError(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1061
}

// MinMySQLVersion 是支持的最低 MySQL 主版本。
const MinMySQLVersion = "5.7"

// MySQLConfig 解析 MySQL DSN，兼容 go-sql-driver 原生格式与 mysql:// URL。
// 返回值已经补齐 UTC 时间解析、utf8mb4 字符集等本程序依赖的默认项。
func MySQLConfig(dsn string) (*mysqldriver.Config, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("MySQL DSN 不能为空")
	}
	var (
		cfg *mysqldriver.Config
		err error
	)
	if strings.Contains(dsn, "://") {
		cfg, err = parseMySQLURL(dsn)
	} else {
		cfg, err = mysqldriver.ParseDSN(normalizeMySQLNativeDSN(dsn))
	}
	if err != nil {
		return nil, fmt.Errorf("解析 MySQL DSN 失败: %w", err)
	}
	if cfg.Net == "" {
		cfg.Net = "tcp"
	}
	if cfg.Addr == "" {
		if cfg.Net == "unix" {
			cfg.Addr = "/tmp/mysql.sock"
		} else {
			cfg.Addr = "127.0.0.1:3306"
		}
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		return nil, fmt.Errorf("MySQL DSN 必须包含数据库名")
	}
	if cfg.Loc == nil {
		cfg.Loc = time.UTC
	}
	// 统一解析 DATETIME 为 time.Time，并用 UTC 读写，避免容器时区差异。
	cfg.ParseTime = true
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if cfg.Collation == "" {
		if _, hasCharset := cfg.Params["charset"]; !hasCharset {
			cfg.Collation = "utf8mb4_general_ci"
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	// 未显式配置 TLS 时，TCP 连接默认尝试加密，避免明文传输数据库凭据。
	// 注意 preferred 不校验证书；生产环境应显式使用 tls=true（系统 CA）
	// 或注册自定义 CA 的 TLS 配置。
	if strings.TrimSpace(cfg.TLSConfig) == "" && (cfg.Net == "tcp" || cfg.Net == "tcp4" || cfg.Net == "tcp6") {
		cfg.TLSConfig = "preferred"
	}
	return cfg, nil
}

// MySQLAddress 返回 MySQL DSN 中的 host:port，用于迁移目标的安全校验。
// Unix socket 等无法转换成 IP 地址的形式返回 false。
func MySQLAddress(dsn string) (string, bool) {
	cfg, err := MySQLConfig(dsn)
	if err != nil || cfg.Net != "tcp" && cfg.Net != "tcp4" && cfg.Net != "tcp6" {
		return "", false
	}
	return cfg.Addr, true
}

func parseMySQLURL(dsn string) (*mysqldriver.Config, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mysql", "mariadb":
	default:
		return nil, fmt.Errorf("不支持的 MySQL URL scheme: %s", parsed.Scheme)
	}
	cfg := mysqldriver.NewConfig()
	cfg.Params = map[string]string{}
	if parsed.User != nil {
		cfg.User = parsed.User.Username()
		cfg.Passwd, _ = parsed.User.Password()
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("MySQL URL 缺少主机名")
	}
	port := parsed.Port()
	if port == "" {
		port = "3306"
	}
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	if path := strings.TrimPrefix(parsed.Path, "/"); path != "" {
		if unescaped, err := url.PathUnescape(path); err == nil {
			cfg.DBName = unescaped
		} else {
			cfg.DBName = path
		}
	}
	for key, values := range parsed.Query() {
		if len(values) == 0 {
			continue
		}
		value := values[len(values)-1]
		switch strings.ToLower(key) {
		case "charset":
			cfg.Params["charset"] = value
		case "collation":
			cfg.Collation = value
		case "tls", "sslmode":
			cfg.TLSConfig = normalizeMySQLTLS(value)
		case "timeout":
			if duration, err := time.ParseDuration(value); err == nil {
				cfg.Timeout = duration
			}
		case "readtimeout":
			if duration, err := time.ParseDuration(value); err == nil {
				cfg.ReadTimeout = duration
			}
		case "writetimeout":
			if duration, err := time.ParseDuration(value); err == nil {
				cfg.WriteTimeout = duration
			}
		case "parsetime":
			cfg.ParseTime = !strings.EqualFold(value, "false")
		case "loc":
			if location, err := time.LoadLocation(value); err == nil {
				cfg.Loc = location
			}
		case "interpolateparams":
			cfg.InterpolateParams = !strings.EqualFold(value, "false")
		case "multistatements":
			cfg.MultiStatements = strings.EqualFold(value, "true")
		case "allownativepasswords":
			cfg.AllowNativePasswords = !strings.EqualFold(value, "false")
		case "allowcleartextpasswords":
			cfg.AllowCleartextPasswords = strings.EqualFold(value, "true")
		case "allowoldpasswords":
			cfg.AllowOldPasswords = strings.EqualFold(value, "true")
		case "allowfallbacktoplaintext":
			cfg.AllowFallbackToPlaintext = strings.EqualFold(value, "true")
		default:
			cfg.Params[key] = value
		}
	}
	return cfg, nil
}

// normalizeMySQLTLS 把页面上的 sslmode 语义映射到 go-sql-driver 的 tls 参数。
func normalizeMySQLTLS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "disable", "disabled", "false", "off":
		return "false"
	case "prefer", "preferred":
		return "preferred"
	case "require", "required", "true", "on", "verify-ca", "verify-full", "verify-identity":
		return "true"
	case "skip-verify", "insecure":
		return "skip-verify"
	default:
		return value
	}
}

// normalizeMySQLNativeDSN 让 go-sql-driver 原生 DSN 也接受页面上的 tls/sslmode 写法。
// 原生格式里 tls 是保留参数，直接传 tls=disable 会在 ParseDSN 阶段失败。
func normalizeMySQLNativeDSN(dsn string) string {
	slash := strings.LastIndexByte(dsn, '/')
	if slash < 0 {
		return dsn
	}
	queryIndex := strings.IndexByte(dsn[slash:], '?')
	if queryIndex < 0 {
		return dsn
	}
	index := slash + queryIndex
	query, err := url.ParseQuery(dsn[index+1:])
	if err != nil {
		return dsn
	}
	tlsValue := query.Get("tls")
	if tlsValue == "" {
		tlsValue = query.Get("sslmode")
	}
	if tlsValue == "" {
		return dsn
	}
	query.Del("sslmode")
	query.Set("tls", normalizeMySQLTLS(tlsValue))
	return dsn[:index] + "?" + query.Encode()
}

// MySQLCLIConnection 是 mysql / mysqldump 客户端可用的连接参数。
type MySQLCLIConnection struct {
	Network  string
	Host     string
	Port     string
	User     string
	Password string
	Database string
	Socket   string
	TLSMode  string
}

// MySQLCLI 解析 DSN，生成命令行客户端的连接参数。密码通过 MYSQL_PWD 传递，
// 不会出现在进程参数中。
func MySQLCLI(dsn string) (MySQLCLIConnection, error) {
	cfg, err := MySQLConfig(dsn)
	if err != nil {
		return MySQLCLIConnection{}, err
	}
	connection := MySQLCLIConnection{
		Network:  cfg.Net,
		User:     cfg.User,
		Password: cfg.Passwd,
		Database: cfg.DBName,
		TLSMode:  mysqlCLITLSMode(cfg.TLSConfig),
	}
	switch cfg.Net {
	case "unix":
		connection.Socket = cfg.Addr
	default:
		host, port, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			host = cfg.Addr
			port = "3306"
		}
		connection.Host = host
		connection.Port = port
	}
	return connection, nil
}

func mysqlCLITLSMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false":
		return "DISABLED"
	case "preferred":
		return "PREFERRED"
	case "skip-verify":
		return "REQUIRED"
	case "true":
		return "VERIFY_IDENTITY"
	default:
		return "PREFERRED"
	}
}

// SupportsMySQLVersion 判断版本字符串是否达到 MySQL 5.7 或 MariaDB 10.2。
// MySQL 兼容握手可能返回 "5.5.5-10.3.8-MariaDB" 这类前缀。
func SupportsMySQLVersion(version string) bool {
	parts := mysqlVersionNumbers(version)
	if len(parts) < 2 {
		return false
	}
	major, minor := parts[0], parts[1]
	if strings.Contains(strings.ToUpper(version), "MARIADB") {
		return major > 10 || (major == 10 && minor >= 2)
	}
	return major > 5 || (major == 5 && minor >= 7)
}

func mysqlVersionNumbers(version string) []int {
	var (
		numbers []int
		current strings.Builder
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		value, err := strconv.Atoi(current.String())
		if err == nil {
			numbers = append(numbers, value)
		}
		current.Reset()
	}
	for _, r := range version {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	// MariaDB 的握手版本会带 5.5.5 前缀，真正版本在最后一段之前。
	if strings.Contains(strings.ToUpper(version), "MARIADB") && len(numbers) > 3 && numbers[0] == 5 && numbers[1] == 5 && numbers[2] == 5 {
		return numbers[3:]
	}
	return numbers
}
