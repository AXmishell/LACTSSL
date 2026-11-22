package db

import (
	"regexp"
	"strings"
	"testing"
)

func TestParseDialectMySQL(t *testing.T) {
	for _, value := range []string{"mysql", "MySQL", "mariadb", " MariaDB "} {
		dialect, ok := ParseDialect(value)
		if !ok || dialect != MySQL {
			t.Fatalf("ParseDialect(%q) = %q, %v", value, dialect, ok)
		}
	}
	if _, ok := ParseDialect("oracle"); ok {
		t.Fatal("不支持的驱动不应被接受")
	}
}

func TestMySQLSchemaStatements(t *testing.T) {
	joined := strings.Join(schemaStatements(MySQL), "\n")
	for _, forbidden := range []string{
		"AUTOINCREMENT",
		"INTEGER PRIMARY KEY",
		"TEXT NOT NULL DEFAULT",
		"CREATE INDEX IF NOT EXISTS",
		"CREATE UNIQUE INDEX IF NOT EXISTS",
		" WHERE status IN ('pending','running')",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("MySQL DDL 仍包含不兼容语法 %q:\n%s", forbidden, joined)
		}
	}
	for _, required := range []string{
		"ENGINE=InnoDB",
		"`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY",
		"`email` VARCHAR(255) NOT NULL UNIQUE",
		"`key` VARCHAR(191) PRIMARY KEY",
		"DATETIME(6)",
		"`serial` VARCHAR(128) CHARACTER SET ascii",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("MySQL DDL 缺少 %q", required)
		}
	}
	if regexp.MustCompile(`\bTEXT\s+PRIMARY\s+KEY`).MatchString(joined) {
		t.Fatal("MySQL 不允许 TEXT 直接作为主键")
	}
	if regexp.MustCompile(`\bTEXT\s+[^,\n]*DEFAULT`).MatchString(joined) {
		t.Fatal("MySQL 5.7 不允许 TEXT 列带默认值")
	}
	if strings.Contains(joined, "key VARCHAR(191)") {
		t.Fatal("MySQL 关键字 key 应使用安全列名或反引号")
	}
}

func TestMySQLIndexedColumnsAreNotLONGTEXT(t *testing.T) {
	statements := schemaStatements(MySQL)
	lob := map[string]map[string]bool{}
	for _, stmt := range statements {
		match := regexp.MustCompile(`(?is)^CREATE TABLE IF NOT EXISTS (\w+)\s*\((.*)\)\s*ENGINE`).FindStringSubmatch(stmt)
		if len(match) != 3 {
			continue
		}
		table := match[1]
		lob[table] = map[string]bool{}
		for _, line := range strings.Split(match[2], "\n") {
			column := regexp.MustCompile("^\\s*`([a-z_][a-z0-9_]*)`\\s+LONGTEXT").FindStringSubmatch(line)
			if len(column) == 2 {
				lob[table][column[1]] = true
			}
		}
	}
	indexPattern := regexp.MustCompile(`(?is)(?:CREATE (?:UNIQUE )?INDEX \w+ ON|UNIQUE|PRIMARY KEY)\s*(\w+)?\s*\(([^)]*)\)`)
	for _, stmt := range statements {
		table := ""
		if match := regexp.MustCompile(`(?is)ON\s+(\w+)\s*\(`).FindStringSubmatch(stmt); len(match) == 2 {
			table = match[1]
		}
		if table == "" {
			for candidate := range lob {
				if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS "+candidate) {
					table = candidate
					break
				}
			}
		}
		if table == "" {
			continue
		}
		for _, match := range indexPattern.FindAllStringSubmatch(stmt, -1) {
			for _, column := range strings.Split(match[2], ",") {
				column = strings.Trim(strings.TrimSpace(column), "`")
				if strings.Contains(column, "(") {
					continue
				}
				if lob[table][column] {
					t.Fatalf("表 %s 的 LONGTEXT 列 %s 不能直接建立索引", table, column)
				}
			}
		}
	}
}

func TestMySQLConflictRewrite(t *testing.T) {
	dialect := MySQL
	cases := []struct {
		query string
		want  string
	}{
		{
			query: `INSERT INTO sessions(id) VALUES(?) ON CONFLICT(id) DO NOTHING`,
			want:  "INSERT INTO sessions(id) VALUES(?) ON DUPLICATE KEY UPDATE `id`=`id`",
		},
		{
			query: `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			want:  "INSERT INTO settings(key,value) VALUES(?,?) ON DUPLICATE KEY UPDATE value=VALUES(`value`)",
		},
	}
	for _, item := range cases {
		if got := dialect.Rebind(item.query); got != item.want {
			t.Fatalf("Rebind(%q) = %q, want %q", item.query, got, item.want)
		}
	}
	if got := Postgres.Rebind(`SELECT ?`); got != `SELECT $1` {
		t.Fatalf("Postgres Rebind = %q", got)
	}
}

func TestMySQLConfigFromURL(t *testing.T) {
	cfg, err := MySQLConfig("mysql://user:p%40ss@db.example.com:3307/lactssl?tls=disable&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "user" || cfg.Passwd != "p@ss" || cfg.Addr != "db.example.com:3307" || cfg.DBName != "lactssl" {
		t.Fatalf("解析结果异常: %+v", cfg)
	}
	if !cfg.ParseTime || cfg.Loc.String() != "UTC" {
		t.Fatalf("时间解析未统一为 UTC: %+v", cfg)
	}
	if cfg.TLSConfig != "false" {
		t.Fatalf("tls=disable 应转换为 false，得到 %q", cfg.TLSConfig)
	}
	connection, err := MySQLCLI("mysql://user:pass@db.example.com:3307/lactssl")
	if err != nil {
		t.Fatal(err)
	}
	if connection.Host != "db.example.com" || connection.Port != "3307" || connection.Database != "lactssl" {
		t.Fatalf("CLI 连接参数异常: %+v", connection)
	}
}

func TestSupportsMySQLVersion(t *testing.T) {
	for _, version := range []string{"5.7.44", "8.0.36", "8.4.2", "10.3.39-MariaDB", "5.5.5-10.11.6-MariaDB"} {
		if !SupportsMySQLVersion(version) {
			t.Fatalf("版本 %s 应被支持", version)
		}
	}
	for _, version := range []string{"5.6.51", "5.5.62", "10.1.48-MariaDB"} {
		if SupportsMySQLVersion(version) {
			t.Fatalf("版本 %s 不应被支持", version)
		}
	}
}

// TestMySQLConfigDefaultsToTLS 验证未显式配置 tls 的 TCP 连接默认尝试加密，
// 避免 MySQL 凭据与业务数据明文传输；Unix socket 不强制 TLS。
func TestMySQLConfigDefaultsToTLS(t *testing.T) {
	for _, dsn := range []string{
		"mysql://user:pass@127.0.0.1:3306/lactssl",
		"user:pass@tcp(127.0.0.1:3306)/lactssl",
	} {
		cfg, err := MySQLConfig(dsn)
		if err != nil {
			t.Fatalf("MySQLConfig(%q): %v", dsn, err)
		}
		if cfg.TLSConfig != "preferred" {
			t.Fatalf("默认 TCP 连接应尝试 TLS，得到 %q", cfg.TLSConfig)
		}
	}
	cfg, err := MySQLConfig("user:pass@unix(/tmp/mysql.sock)/lactssl")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig != "" {
		t.Fatalf("Unix socket 不应强制 TLS，得到 %q", cfg.TLSConfig)
	}
}
