package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// mysqlTextOverrides 把需要参与主键/唯一索引或长度可控的 TEXT 列改为 VARCHAR。
// MySQL 不允许 TEXT/BLOB 直接作为索引键，也不允许 TEXT 列带 DEFAULT；
// 其余 TEXT 列统一使用 LONGTEXT（去掉默认值，由 INSERT 显式提供）。
var mysqlTextOverrides = map[string]string{
	"users.email":                          "VARCHAR(255)",
	"settings.key":                         "VARCHAR(191)",
	"plugin_installations.kind":            "VARCHAR(32)",
	"plugin_installations.id":              "VARCHAR(128)",
	"plugin_installations.index_digest":    "VARCHAR(64) CHARACTER SET ascii",
	"plugin_installations.entry_digest":    "VARCHAR(64) CHARACTER SET ascii",
	"plugin_file_meta.kind":                "VARCHAR(32)",
	"plugin_file_meta.filename":            "VARCHAR(255)",
	"plugin_metadata.kind":                 "VARCHAR(32)",
	"plugin_metadata.id":                   "VARCHAR(128)",
	"sessions.id":                          "VARCHAR(191)",
	"passkeys.credential_id":               "VARCHAR(1023) CHARACTER SET ascii",
	"api_keys.token_hash":                  "VARCHAR(128) CHARACTER SET ascii",
	"api_keys.last_used_ip":                "VARCHAR(64)",
	"dns_zone_bindings.zone":               "VARCHAR(255) CHARACTER SET ascii",
	"trusted_cas.fingerprint":              "VARCHAR(128) CHARACTER SET ascii",
	"revocations.serial":                   "VARCHAR(128) CHARACTER SET ascii",
	"acme_cas.public_id":                   "VARCHAR(64) CHARACTER SET ascii",
	"ca_pools.public_id":                   "VARCHAR(64) CHARACTER SET ascii",
	"ca_pool_items.ca_value":               "VARCHAR(512)",
	"acme_endpoints.public_id":             "VARCHAR(64) CHARACTER SET ascii",
	"acme_endpoints.slug":                  "VARCHAR(191) CHARACTER SET ascii",
	"acme_accounts.public_id":              "VARCHAR(64) CHARACTER SET ascii",
	"acme_accounts.kid":                    "VARCHAR(512) CHARACTER SET ascii",
	"acme_accounts.thumbprint":             "VARCHAR(128) CHARACTER SET ascii",
	"acme_eab_credentials.public_id":       "VARCHAR(64) CHARACTER SET ascii",
	"acme_eab_credentials.kid":             "VARCHAR(255) CHARACTER SET ascii",
	"acme_orders.public_id":                "VARCHAR(64) CHARACTER SET ascii",
	"acme_authorizations.public_id":        "VARCHAR(64) CHARACTER SET ascii",
	"acme_challenges.public_id":            "VARCHAR(64) CHARACTER SET ascii",
	"acme_challenges.token":                "VARCHAR(255) CHARACTER SET ascii",
	"certificate_requests.public_id":       "VARCHAR(64) CHARACTER SET ascii",
	"certificate_requests.csr_sha256":      "VARCHAR(64) CHARACTER SET ascii",
	"certificate_requests.ca":              "VARCHAR(255)",
	"certificate_requests.dns_routes_hash": "VARCHAR(64) CHARACTER SET ascii",
	"certificate_requests.status":          "VARCHAR(32)",
	"security_events.fingerprint":          "VARCHAR(128)",
	"logs.category":                        "VARCHAR(32)",
	"logs.source":                          "VARCHAR(64)",
	"logs.ref_type":                        "VARCHAR(64)",
	"logs.level":                           "VARCHAR(16)",
	"system_logs.level":                    "VARCHAR(16)",
	"system_logs.component":                "VARCHAR(128)",
	"certificates.status":                  "VARCHAR(32)",
	"acme_orders.status":                   "VARCHAR(32)",
	"acme_challenges.status":               "VARCHAR(32)",
}

var (
	mysqlCreateTablePattern  = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*\(`)
	mysqlColumnLinePattern   = regexp.MustCompile(`^(\s*)([a-z_][a-z0-9_]*)\s+(.+)$`)
	mysqlTextTypePattern     = regexp.MustCompile(`\bTEXT\b`)
	mysqlTextDefaultPattern  = regexp.MustCompile(`(?i)\s+DEFAULT\s+'(?:[^'\\]|\\.|'')*'`)
	mysqlDefaultValuePattern = regexp.MustCompile(`(?i)\bDEFAULT\s+'((?:[^'\\]|\\.|'')*)'`)
)

// mysqlCaseInsensitiveOverrides 是不启用二进制排序规则的列：
// 人类输入（email）、内部枚举状态和日志元数据保留表默认的 *_ci，
// 避免为大表（尤其是 logs）做无收益的整表重建。
var mysqlCaseInsensitiveOverrides = map[string]bool{
	"users.email":                 true,
	"logs.category":               true,
	"logs.source":                 true,
	"logs.ref_type":               true,
	"logs.level":                  true,
	"system_logs.level":           true,
	"system_logs.component":       true,
	"certificates.status":         true,
	"acme_orders.status":          true,
	"acme_challenges.status":      true,
	"certificate_requests.status": true,
}

// mysqlColumnDefinition 把基础（SQLite 语义）列定义转换为 MySQL 5.7 写法。
// 全新安装与 ALTER TABLE 升级共用该函数，避免两条路径的类型漂移。
func mysqlColumnDefinition(table, column, definition string) string {
	key := table + "." + column
	replacement, overridden := mysqlTextOverrides[key]
	if overridden {
		upper := strings.ToUpper(replacement)
		if !strings.Contains(upper, "COLLATE") && !mysqlCaseInsensitiveOverrides[key] {
			// 不透明 ID/哈希/状态等使用二进制排序规则，保持与 SQLite/PG 的
			// 大小写敏感语义一致。
			if strings.Contains(upper, "CHARACTER SET ASCII") {
				replacement += " COLLATE ascii_bin"
			} else {
				replacement += " COLLATE utf8mb4_bin"
			}
		}
	}
	hasText := mysqlTextTypePattern.MatchString(definition)
	if hasText {
		if overridden {
			definition = mysqlTextTypePattern.ReplaceAllString(definition, replacement)
		} else {
			definition = mysqlTextTypePattern.ReplaceAllString(definition, "LONGTEXT")
			// MySQL 5.7 的 LONGTEXT 不允许 DEFAULT；应用插入时必须显式提供该列。
			definition = mysqlTextDefaultPattern.ReplaceAllString(definition, "")
		}
	}
	definition = strings.ReplaceAll(definition, "INTEGER", "BIGINT")
	definition = strings.ReplaceAll(definition, "DATETIME", "DATETIME(6)")
	return strings.TrimSpace(definition)
}

// mysqlDefaultValue 提取列定义中字符串默认值，供 MySQL 升级补列后回填旧行。
func mysqlDefaultValue(definition string) (string, bool) {
	match := mysqlDefaultValuePattern.FindStringSubmatch(definition)
	if len(match) != 2 {
		return "", false
	}
	return strings.ReplaceAll(match[1], "''", "'"), true
}

// mysqlStatements 把基础 SQLite DDL 转换为 MySQL 5.7+ 可执行写法。
func mysqlStatements() []string {
	base := sqliteStatements()
	out := make([]string, 0, len(base)+1)
	for _, stmt := range base {
		trimmed := strings.TrimSpace(stmt)
		upper := strings.ToUpper(trimmed)
		// MySQL 不支持部分索引（CREATE INDEX ... WHERE），在途申请唯一性
		// 由 migrate 阶段创建的生成列唯一索引保证。
		if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
			if strings.Contains(strings.ToUpper(trimmed), " WHERE ") {
				continue
			}
			stmt = strings.Replace(stmt, "IF NOT EXISTS ", "", 1)
			out = append(out, stmt)
			continue
		}
		if strings.HasPrefix(upper, "CREATE TABLE") {
			out = append(out, translateMySQLDDL(stmt))
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// translateMySQLDDL 逐列转换表结构，保证列顺序与基础 schema 一致。
func translateMySQLDDL(stmt string) string {
	table := ""
	if match := mysqlCreateTablePattern.FindStringSubmatch(stmt); len(match) == 2 {
		table = strings.ToLower(match[1])
	}
	lines := strings.Split(stmt, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") ||
			strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CONSTRAINT") {
			continue
		}
		if autoIncrementPattern.MatchString(line) {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			replacement := indent + "`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY"
			if strings.HasSuffix(strings.TrimSpace(line), ",") {
				replacement += ","
			}
			lines[index] = replacement
			continue
		}
		match := mysqlColumnLinePattern.FindStringSubmatch(line)
		if len(match) != 4 {
			continue
		}
		column := strings.ToLower(match[2])
		definition := mysqlColumnDefinition(table, column, match[3])
		lines[index] = match[1] + "`" + match[2] + "` " + definition
	}
	result := strings.Join(lines, "\n")
	result = strings.TrimRight(result, " \t\n")
	if strings.HasSuffix(result, ")") {
		result += " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci"
	}
	return result
}

// ensureMySQLActiveIndex 创建 CSR 在途申请唯一索引。
// MySQL 没有部分索引，用生成列把仅 pending/running 的申请映射到非 NULL 值，
// 其余状态为 NULL；唯一索引允许多个 NULL，从而保持与 SQLite/PostgreSQL 相同的语义。
func ensureMySQLActiveIndex(ctx context.Context, conn *Handle) error {
	exists, err := hasColumn(conn, "certificate_requests", "active_request_key")
	if err != nil {
		return fmt.Errorf("读取 CSR 唯一索引列失败: %w", err)
	}
	if !exists {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE certificate_requests ADD COLUMN active_request_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin
			GENERATED ALWAYS AS (
				IF(status IN ('pending','running'),
					SHA2(CONCAT_WS('|', csr_sha256, ca, api_key_id, dns_account_id, dns_routes_hash), 256),
					NULL)
			) STORED`); err != nil {
			return fmt.Errorf("创建 CSR 唯一索引列失败: %w", err)
		}
	}
	var count int64
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema=DATABASE() AND table_name='certificate_requests' AND index_name='idx_cert_request_active_hash'`).Scan(&count); err != nil {
		return fmt.Errorf("读取 CSR 唯一索引失败: %w", err)
	}
	if count == 0 {
		if _, err := conn.ExecContext(ctx, `CREATE UNIQUE INDEX idx_cert_request_active_hash ON certificate_requests(active_request_key)`); err != nil {
			return fmt.Errorf("创建 CSR 唯一索引失败: %w", err)
		}
	}
	return nil
}

// mysqlLegacyLOBColumns 是旧版本通过 ALTER TABLE 补列时被写成 VARCHAR(2048) 的文本列。
// 新版本统一改为 LONGTEXT，避免日志/流程 JSON 在 MySQL 上被 2048 字符限制截断。
var mysqlLegacyLOBColumns = []struct {
	Table  string
	Column string
}{
	{"tasks", "dns_routes"},
	{"tasks", "pipeline"},
	{"tasks", "target_flow"},
	{"tasks", "flow_mode"},
	{"certificate_requests", "dns_routes"},
	{"certificate_requests", "resolved_by"},
	{"certificate_requests", "resolved_zone"},
	{"logs", "fields"},
	{"logs", "request_id"},
	{"api_keys", "allowed_ips"},
	{"api_keys", "allowed_dns_account_ids"},
	{"dns_accounts", "verify_status"},
	{"dns_accounts", "verify_message"},
	{"dns_accounts", "verify_mode"},
	{"deploy_accounts", "verify_status"},
	{"deploy_accounts", "verify_message"},
	{"deploy_accounts", "verify_mode"},
	{"acme_endpoints", "allowed_domains"},
	{"acme_endpoints", "allowed_ip_cidrs"},
	{"acme_endpoints", "http01_allowed_cidrs"},
	{"acme_endpoints", "tls_alpn_01_allowed_cidrs"},
	{"acme_orders", "replaces"},
	{"certificates", "source"},
	{"private_cas", "ca_type"},
	{"security_events", "smtp_snapshot"},
}

// ensureMySQLLegacyTextColumns 修复历史上被升级成 VARCHAR(2048) 的文本列。
func ensureMySQLLegacyTextColumns(ctx context.Context, conn *Handle) error {
	for _, item := range mysqlLegacyLOBColumns {
		var dataType, nullable string
		err := conn.QueryRowContext(ctx, `SELECT data_type,is_nullable FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, item.Table, item.Column).Scan(&dataType, &nullable)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("读取 %s.%s 列类型失败: %w", item.Table, item.Column, err)
		}
		if strings.EqualFold(dataType, "longtext") {
			continue
		}
		definition := "LONGTEXT"
		if nullable == "NO" {
			definition += " NOT NULL"
		}
		stmt := "ALTER TABLE " + conn.Dialect().QuoteIdent(item.Table) +
			" MODIFY COLUMN " + conn.Dialect().QuoteIdent(item.Column) + " " + definition
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("修复 %s.%s 为 LONGTEXT 失败: %w", item.Table, item.Column, err)
		}
	}
	return nil
}

// ensureMySQLBinaryCollations 把不透明 ID/哈希/状态列从默认的 *_ci 排序规则改为二进制。
// 已有库升级时执行；新库在建表阶段已经使用二进制排序规则。
func ensureMySQLBinaryCollations(ctx context.Context, conn *Handle) error {
	for full := range mysqlTextOverrides {
		if mysqlCaseInsensitiveOverrides[full] {
			continue
		}
		parts := strings.SplitN(full, ".", 2)
		if len(parts) != 2 {
			continue
		}
		table, column := parts[0], parts[1]
		var (
			columnType, nullable, extra string
			columnDefault               sql.NullString
			collation, charset          sql.NullString
			generation                  sql.NullString
		)
		err := conn.QueryRowContext(ctx, `SELECT column_type,is_nullable,column_default,extra,
			collation_name,character_set_name,generation_expression FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).
			Scan(&columnType, &nullable, &columnDefault, &extra, &collation, &charset, &generation)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("读取 %s.%s 排序规则失败: %w", table, column, err)
		}
		if !charset.Valid || charset.String == "" {
			continue
		}
		desiredCharset := charset.String
		if strings.Contains(strings.ToUpper(mysqlTextOverrides[full]), "CHARACTER SET ASCII") {
			desiredCharset = "ascii"
		}
		desiredCollation := desiredCharset + "_bin"
		if strings.EqualFold(collation.String, desiredCollation) {
			continue
		}
		if strings.Contains(strings.ToUpper(extra), "GENERATED") {
			continue
		}
		definition := columnType + " CHARACTER SET " + desiredCharset + " COLLATE " + desiredCollation
		if nullable == "NO" {
			definition += " NOT NULL"
		} else {
			definition += " NULL"
		}
		if columnDefault.Valid {
			definition += " DEFAULT '" + strings.ReplaceAll(columnDefault.String, "'", "''") + "'"
		}
		if strings.Contains(strings.ToLower(extra), "auto_increment") {
			definition += " AUTO_INCREMENT"
		}
		stmt := "ALTER TABLE " + conn.Dialect().QuoteIdent(table) +
			" MODIFY COLUMN " + conn.Dialect().QuoteIdent(column) + " " + definition
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("设置 %s.%s 二进制排序规则失败: %w", table, column, err)
		}
	}
	return nil
}
