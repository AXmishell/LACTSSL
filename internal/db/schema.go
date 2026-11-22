package db

import "regexp"

// sqliteStatements 是基础表结构与索引定义。PostgreSQL 版本由
// schemaStatements 在保持列语义的前提下自动转换。
func sqliteStatements() []string {
	return []string{
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
		`CREATE TABLE IF NOT EXISTS plugin_installations (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			version TEXT NOT NULL,
			filename TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			asset_sha256 TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			repo_url TEXT NOT NULL DEFAULT '',
			index_revision INTEGER NOT NULL DEFAULT 0,
			index_digest TEXT NOT NULL DEFAULT '',
			entry_digest TEXT NOT NULL DEFAULT '',
			installed_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (kind, id)
		)`,
		`CREATE TABLE IF NOT EXISTS plugin_file_meta (
			kind TEXT NOT NULL,
			filename TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			entries TEXT NOT NULL DEFAULT '[]',
			sha256 TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (kind, filename)
		)`,
		`CREATE TABLE IF NOT EXISTS plugin_metadata (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			manifest TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (kind, id)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			strong_auth INTEGER NOT NULL DEFAULT 0,
			reauth_until DATETIME,
			created_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			revoked_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS security_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			fingerprint TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '{}',
			recipients TEXT NOT NULL DEFAULT '[]',
			channels TEXT NOT NULL DEFAULT '[]',
			notify INTEGER NOT NULL DEFAULT 1,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			deliveries TEXT NOT NULL DEFAULT '{}',
			smtp_snapshot TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			next_attempt_at DATETIME,
			delivered_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS passkeys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			credential_id TEXT NOT NULL UNIQUE,
			credential TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			last_used_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			scopes TEXT NOT NULL DEFAULT '[]',
			allowed_ips TEXT NOT NULL DEFAULT '[]',
			allowed_dns_account_ids TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1,
			expires_at DATETIME,
			last_used_at DATETIME,
			last_used_ip TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
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
			verify_status TEXT NOT NULL DEFAULT '',
			verify_message TEXT NOT NULL DEFAULT '',
			verify_mode TEXT NOT NULL DEFAULT '',
			verified_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// dns_zone_bindings 是“域名 → DNS 账户”的声明式映射。
		// zone 已规范化（小写、无尾点、无通配符），match_mode=suffix 表示
		// 覆盖该 zone 及其子域，exact 表示只匹配该域名。
		`CREATE TABLE IF NOT EXISTS dns_zone_bindings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			dns_account_id INTEGER NOT NULL,
			zone TEXT NOT NULL,
			match_mode TEXT NOT NULL DEFAULT 'suffix',
			priority INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'manual',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(zone, dns_account_id)
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			config TEXT NOT NULL,
			remark TEXT NOT NULL DEFAULT '',
			verify_status TEXT NOT NULL DEFAULT '',
			verify_message TEXT NOT NULL DEFAULT '',
			verify_mode TEXT NOT NULL DEFAULT '',
			verified_at DATETIME,
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
		`CREATE TABLE IF NOT EXISTS trusted_cas (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			subject TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			serial TEXT NOT NULL DEFAULT '',
			fingerprint TEXT NOT NULL UNIQUE,
			cert_pem TEXT NOT NULL,
			not_before DATETIME NOT NULL,
			not_after DATETIME NOT NULL,
			created_at DATETIME NOT NULL
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
		// 吊销记录独立于证书行：删除/清理证书不能把 CRL/OCSP 中的吊销状态一起删掉。
		`CREATE TABLE IF NOT EXISTS revocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ca_id INTEGER NOT NULL,
			serial TEXT NOT NULL,
			revoked_at DATETIME NOT NULL,
			reason INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			UNIQUE(ca_id, serial)
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			domains TEXT NOT NULL,
			challenge TEXT NOT NULL DEFAULT 'dns01',
			dns_account_id INTEGER NOT NULL DEFAULT 0,
			dns_routes TEXT NOT NULL DEFAULT '[]',
			ca TEXT NOT NULL DEFAULT 'letsencrypt',
			ca_pool_id INTEGER NOT NULL DEFAULT 0,
			key_type TEXT NOT NULL DEFAULT 'ecdsa',
			no_key_storage INTEGER NOT NULL DEFAULT 0,
			auto_renew INTEGER NOT NULL DEFAULT 1,
			renew_days INTEGER NOT NULL DEFAULT 30,
			deploy_ids TEXT NOT NULL DEFAULT '[]',
			pipeline TEXT NOT NULL DEFAULT '[]',
			flow_mode TEXT NOT NULL DEFAULT 'standard',
			target_flow TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			message TEXT NOT NULL DEFAULT '',
			last_run_at DATETIME,
			next_run_at DATETIME,
			cert_id INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_certificate_links (
			task_id INTEGER NOT NULL,
			cert_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (task_id, cert_id)
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
			allow_internal INTEGER NOT NULL DEFAULT 0,
			last_check_at DATETIME,
			next_check_at DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			category TEXT NOT NULL DEFAULT 'operation',
			source TEXT NOT NULL,
			ref_type TEXT NOT NULL DEFAULT '',
			ref_id INTEGER NOT NULL DEFAULT 0,
			level TEXT NOT NULL DEFAULT 'info',
			message TEXT NOT NULL,
			fields TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		// system_logs 已合并到 logs(category='runtime')。保留旧表一个版本，
		// 仅用于降级/回滚；新代码不再读写，历史数据由 upgrade.logs_unified 复制。
		`CREATE TABLE IF NOT EXISTS system_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			component TEXT NOT NULL DEFAULT 'app',
			message TEXT NOT NULL,
			fields TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS ca_pools (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'round_robin',
			cursor INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS ca_pool_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pool_id INTEGER NOT NULL,
			ca_value TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(pool_id, ca_value),
			FOREIGN KEY(pool_id) REFERENCES ca_pools(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ca_pool_items_pool ON ca_pool_items(pool_id, priority, id)`,
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
		// certificate_requests 保存客户端提交的 CSR 申请。服务端只保存 CSR
		// 公钥与域名声明，不保存私钥；证书签发后通过 certificate_id 关联。
		// zotrus_orders 保存等待用户手动添加 DNS 记录的零信订单。
		// 私钥在 Store 层加密；CSR 为公开数据，按 DER 的 base64url 保存。
		`CREATE TABLE IF NOT EXISTS zotrus_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			domains TEXT NOT NULL DEFAULT '[]',
			key_type TEXT NOT NULL DEFAULT 'ecdsa',
			order_url TEXT NOT NULL DEFAULT '',
			account_uri TEXT NOT NULL DEFAULT '',
			finalize_url TEXT NOT NULL DEFAULT '',
			certificate_url TEXT NOT NULL DEFAULT '',
			key_pem TEXT NOT NULL DEFAULT '',
			csr_der TEXT NOT NULL DEFAULT '',
			challenges TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'pending',
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS certificate_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			public_id TEXT NOT NULL UNIQUE,
			csr_pem TEXT NOT NULL,
			csr_sha256 TEXT NOT NULL,
			identifiers TEXT NOT NULL DEFAULT '[]',
			primary_domain TEXT NOT NULL DEFAULT '',
			ca TEXT NOT NULL,
			challenge TEXT NOT NULL DEFAULT 'dns01',
			dns_account_id INTEGER NOT NULL DEFAULT 0,
			dns_routes TEXT NOT NULL DEFAULT '[]',
			dns_routes_hash TEXT NOT NULL DEFAULT '',
			resolved_by TEXT NOT NULL DEFAULT '',
			resolved_zone TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			retryable INTEGER NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			certificate_id INTEGER NOT NULL DEFAULT 0,
			upstream_order_id TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT '',
			user_id INTEGER NOT NULL DEFAULT 0,
			api_key_id INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			expires_at DATETIME,
			completed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_created ON security_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_user_created ON security_events(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_user_fingerprint ON security_events(user_id, fingerprint, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_pending ON security_events(notify, next_attempt_at, delivered_at)`,
		`CREATE INDEX IF NOT EXISTS idx_passkeys_user ON passkeys(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_not_after ON certificates(not_after)`,
		`CREATE INDEX IF NOT EXISTS idx_revocations_ca ON revocations(ca_id, revoked_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_task ON certificates(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_cert_link_cert ON task_certificate_links(cert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_next_run ON tasks(next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_next_run ON monitors(next_check_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created ON logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_source_created ON logs(source, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created ON system_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_level_created ON system_logs(level, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_component_created ON system_logs(component, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_cert ON deploy_records(cert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_account_endpoint ON acme_accounts(endpoint_id, thumbprint)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_order_account ON acme_orders(account_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_authz_order ON acme_authorizations(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_challenge_authz ON acme_challenges(authz_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acme_challenge_next ON acme_challenges(next_check_at)`,
		`CREATE INDEX IF NOT EXISTS idx_zotrus_orders_task ON zotrus_orders(task_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_request_status_created ON certificate_requests(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cert_request_hash ON certificate_requests(csr_sha256, ca)`,
		`CREATE INDEX IF NOT EXISTS idx_dns_zone_bindings_account ON dns_zone_bindings(dns_account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dns_zone_bindings_zone ON dns_zone_bindings(zone)`,
		// 同一调用方对同一 CSR、CA 与 DNS 账户只允许一个在途申请，避免并发提交
		// 产生重复订单；已签发/失败/取消后允许重新提交。
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cert_request_active_hash ON certificate_requests(csr_sha256, ca, api_key_id, dns_account_id, dns_routes_hash) WHERE status IN ('pending','running')`,
	}
}

var (
	autoIncrementPattern = regexp.MustCompile(`(?m)\bINTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT\b`)
	idColumnPattern      = regexp.MustCompile(`(?m)^(\s*)(id|[a-z][a-z0-9_]*_id)\s+INTEGER\b`)
	datetimePattern      = regexp.MustCompile(`\bDATETIME\b`)
)

// schemaStatements 返回指定方言的建表语句。
func schemaStatements(d Dialect) []string {
	switch {
	case d.IsPostgres():
		base := sqliteStatements()
		out := make([]string, 0, len(base))
		for _, stmt := range base {
			out = append(out, translatePostgresDDL(stmt))
		}
		return out
	case d.IsMySQL():
		return mysqlStatements()
	default:
		return sqliteStatements()
	}
}

// translatePostgresDDL 把 SQLite 的基础 DDL 转换为 PostgreSQL 兼容写法。
// 只处理本项目实际使用的简单语法，不追求通用 SQL 翻译。
func translatePostgresDDL(stmt string) string {
	stmt = autoIncrementPattern.ReplaceAllString(stmt, "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY")
	stmt = idColumnPattern.ReplaceAllString(stmt, "${1}${2} BIGINT")
	stmt = datetimePattern.ReplaceAllString(stmt, "TIMESTAMPTZ")
	return stmt
}
