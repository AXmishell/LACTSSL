package db

import (
	"context"
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
	mysqlCreateTablePattern = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*\(`)
	mysqlColumnLinePattern  = regexp.MustCompile(`^(\s*)([a-z_][a-z0-9_]*)\s+(.+)$`)
	mysqlTextTypePattern    = regexp.MustCompile(`\bTEXT\b`)
	mysqlTextDefaultPattern = regexp.MustCompile(`(?i)\s+DEFAULT\s+'(?:[^'\\]|\\.|'')*'`)
)

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
		definition := match[3]
		definition = mysqlTextTypePattern.ReplaceAllStringFunc(definition, func(string) string {
			if replacement, ok := mysqlTextOverrides[table+"."+column]; ok {
				return replacement
			}
			return "LONGTEXT"
		})
		if _, ok := mysqlTextOverrides[table+"."+column]; !ok && strings.Contains(strings.ToUpper(match[3]), "TEXT") {
			// MySQL 5.7 的 TEXT 列不允许 DEFAULT。
			definition = mysqlTextDefaultPattern.ReplaceAllString(definition, "")
		}
		definition = strings.ReplaceAll(definition, "INTEGER", "BIGINT")
		definition = strings.ReplaceAll(definition, "DATETIME", "DATETIME(6)")
		lines[index] = match[1] + "`" + match[2] + "` " + strings.TrimRight(definition, " \t")
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
		if _, err := conn.ExecContext(ctx, `ALTER TABLE certificate_requests ADD COLUMN active_request_key CHAR(64) CHARACTER SET ascii
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
