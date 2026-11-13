// Package db 负责 SQLite 连接与表结构初始化。
package db

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// Open 打开数据库并执行结构迁移。
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 写操作串行化，限制连接数可避免锁竞争。
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	// 限制数据库文件权限，避免其他系统用户读取。
	_ = os.Chmod(path, 0o600)
	if err := migrate(conn); err != nil {
		return nil, err
	}
	return conn, nil
}

func migrate(conn *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret TEXT NOT NULL DEFAULT '',
			totp_enabled INTEGER NOT NULL DEFAULT 0,
			token_version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			revoked_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS password_resets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			used INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS dns_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			config TEXT NOT NULL,
			remark TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			config TEXT NOT NULL,
			remark TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS private_cas (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			common_name TEXT NOT NULL,
			organization TEXT NOT NULL DEFAULT '',
			country TEXT NOT NULL DEFAULT '',
			key_type TEXT NOT NULL DEFAULT 'ecdsa',
			source TEXT NOT NULL DEFAULT 'generated',
			serial TEXT NOT NULL DEFAULT '',
			parent_id INTEGER NOT NULL DEFAULT 0,
			ca_type TEXT NOT NULL DEFAULT 'root',
			path_len INTEGER NOT NULL DEFAULT 1,
			cert_pem TEXT NOT NULL,
			key_pem TEXT NOT NULL,
			not_before DATETIME NOT NULL,
			not_after DATETIME NOT NULL,
			crl_number INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS certificates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL DEFAULT 0,
			domains TEXT NOT NULL,
			primary_domain TEXT NOT NULL,
			issuer TEXT NOT NULL DEFAULT '',
			ca TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'task',
			private_ca_id INTEGER NOT NULL DEFAULT 0,
			acme_order_id INTEGER NOT NULL DEFAULT 0,
			acme_account_id INTEGER NOT NULL DEFAULT 0,
			cert_pem TEXT NOT NULL,
			key_pem TEXT NOT NULL,
			fullchain_pem TEXT NOT NULL,
			serial TEXT NOT NULL DEFAULT '',
			not_before DATETIME NOT NULL,
			not_after DATETIME NOT NULL,
			status TEXT NOT NULL DEFAULT 'valid',
			revoked_at DATETIME,
			revoke_reason INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			domains TEXT NOT NULL,
			challenge TEXT NOT NULL DEFAULT 'dns01',
			dns_account_id INTEGER NOT NULL DEFAULT 0,
			ca TEXT NOT NULL DEFAULT 'letsencrypt',
			key_type TEXT NOT NULL DEFAULT 'ecdsa',
			auto_renew INTEGER NOT NULL DEFAULT 1,
			renew_days INTEGER NOT NULL DEFAULT 30,
			deploy_ids TEXT NOT NULL DEFAULT '[]',
			pipeline TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			message TEXT NOT NULL DEFAULT '',
			last_run_at DATETIME,
			next_run_at DATETIME,
			cert_id INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cert_id INTEGER NOT NULL DEFAULT 0,
			deploy_account_id INTEGER NOT NULL DEFAULT 0,
			account_name TEXT NOT NULL DEFAULT '',
			task_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS monitors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			target TEXT NOT NULL,
			interval_hours INTEGER NOT NULL DEFAULT 24,
			enabled INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			message TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			issuer TEXT NOT NULL DEFAULT '',
			last_check_at DATETIME,
			next_check_at DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			ref_id INTEGER NOT NULL DEFAULT 0,
			level TEXT NOT NULL DEFAULT 'info',
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_cas (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			directory_url TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'v2',
			eab_required INTEGER NOT NULL DEFAULT 0,
			eab_kid TEXT NOT NULL DEFAULT '',
			eab_hmac TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			ca_id INTEGER NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 0,
			eab_required INTEGER NOT NULL DEFAULT 1,
			allowed_domains TEXT NOT NULL DEFAULT '[]',
			allow_wildcard INTEGER NOT NULL DEFAULT 0,
			allow_ip INTEGER NOT NULL DEFAULT 0,
			allowed_ip_cidrs TEXT NOT NULL DEFAULT '[]',
			http01_enabled INTEGER NOT NULL DEFAULT 1,
			dns01_enabled INTEGER NOT NULL DEFAULT 1,
			tls_alpn_01_enabled INTEGER NOT NULL DEFAULT 0,
			allow_private_http01 INTEGER NOT NULL DEFAULT 0,
			allow_private_tls_alpn_01 INTEGER NOT NULL DEFAULT 0,
			http01_allowed_cidrs TEXT NOT NULL DEFAULT '[]',
			tls_alpn_01_allowed_cidrs TEXT NOT NULL DEFAULT '[]',
			validity_days INTEGER NOT NULL DEFAULT 90,
			max_orders_per_hour INTEGER NOT NULL DEFAULT 60,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			endpoint_id INTEGER NOT NULL,
			kid TEXT NOT NULL UNIQUE,
			public_jwk TEXT NOT NULL,
			thumbprint TEXT NOT NULL,
			contact TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'valid',
			eab_id INTEGER NOT NULL DEFAULT 0,
			last_used_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(endpoint_id, thumbprint)
		)`,
		`CREATE TABLE IF NOT EXISTS acme_eab_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			endpoint_id INTEGER NOT NULL,
			kid TEXT NOT NULL UNIQUE,
			label TEXT NOT NULL DEFAULT '',
			hmac_key TEXT NOT NULL,
			used_at DATETIME,
			expires_at DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			endpoint_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			identifiers TEXT NOT NULL DEFAULT '[]',
			replaces TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			certificate_id INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_authorizations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			order_id INTEGER NOT NULL,
			identifier TEXT NOT NULL,
			identifier_type TEXT NOT NULL DEFAULT 'dns',
			wildcard INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS acme_challenges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			authz_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending',
			validated_at DATETIME,
			error TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_check_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_not_after ON certificates(not_after)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_task ON certificates(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_next_run ON tasks(next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_next_run ON monitors(next_check_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created ON logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_cert ON deploy_records(cert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_account_endpoint ON acme_accounts(endpoint_id, thumbprint)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_order_account ON acme_orders(account_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_authz_order ON acme_authorizations(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_challenge_authz ON acme_challenges(authz_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_challenge_next ON acme_challenges(next_check_at)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("初始化数据库表失败: %w", err)
		}
	}
	// 兼容旧数据库：为已有 tasks 表补充任务流程字段。
	if err := ensureColumn(conn, "tasks", "pipeline", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧 ACME 订单表：补充 ARI replaces 字段。
	if err := ensureColumn(conn, "acme_orders", "replaces", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧 ACME 端点表：补充 TLS-ALPN-01 配置。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"tls_alpn_01_enabled", `INTEGER NOT NULL DEFAULT 0`},
		{"allow_private_tls_alpn_01", `INTEGER NOT NULL DEFAULT 0`},
		{"tls_alpn_01_allowed_cidrs", `TEXT NOT NULL DEFAULT '[]'`},
		{"allow_ip", `INTEGER NOT NULL DEFAULT 0`},
		{"allowed_ip_cidrs", `TEXT NOT NULL DEFAULT '[]'`},
	} {
		if err := ensureColumn(conn, "acme_endpoints", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	// 兼容已创建私有 CA 表的数据库：补充上级 CA 关联字段。
	if err := ensureColumn(conn, "private_cas", "parent_id", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容已创建私有 CA 表的数据库：补充 CA 类型与路径长度。
	if err := ensureColumn(conn, "private_cas", "ca_type", `TEXT NOT NULL DEFAULT 'root'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := ensureColumn(conn, "private_cas", "path_len", `INTEGER NOT NULL DEFAULT 1`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧证书表：补充证书来源与 ACME 关联字段。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"source", `TEXT NOT NULL DEFAULT 'task'`},
		{"private_ca_id", `INTEGER NOT NULL DEFAULT 0`},
		{"acme_order_id", `INTEGER NOT NULL DEFAULT 0`},
		{"acme_account_id", `INTEGER NOT NULL DEFAULT 0`},
		{"revoked_at", `DATETIME`},
		{"revoke_reason", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(conn, "certificates", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	if err := ensureColumn(conn, "private_cas", "crl_number", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	return nil
}

// ensureColumn 在缺少指定列时执行 ALTER TABLE，用于轻量级结构迁移。
func ensureColumn(conn *sql.DB, table, column, definition string) error {
	rows, err := conn.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultV   any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryKey); err != nil {
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = conn.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}
