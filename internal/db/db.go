// Package db 负责数据库连接、方言适配与表结构初始化。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// schemaVersion 是基础表结构的版本，写入 SQLite 的 PRAGMA user_version。
// 结构已是最新时跳过建表与 ensureColumn 检查，避免每次启动重复扫描表结构。
//
// 版本历史：
//   - 4：插件化阶段；
//   - 5：logs 表合并进程日志（category/ref_type/fields/request_id）；
//   - 6：CSR 申请（certificate_requests）与证书来源关联；
//   - 7：独立吊销记录表（revocations），删除证书不再抹掉 CRL/OCSP 吊销状态；
//   - 8：导入的内部 CA 信任链（trusted_cas）；
//   - 9：DNS 账户与目标凭据的可用性验证结果；
//   - 10：CA 调度组（ca_pools / ca_pool_items）与任务 CA 池引用。
//   - 11：目标流程 Beta（tasks.flow_mode / tasks.target_flow）。
//   - 12：目标流程多证书关联（task_certificate_links）。
//   - 13：监控项内网目标授权（monitors.allow_internal）。
//   - 14：API 密钥来源 IP/CIDR 白名单（api_keys.allowed_ips）。
//   - 15：DNS 账户的声明式域名绑定（dns_zone_bindings）与 CSR 解析来源。
//   - 16：API 密钥最近使用来源 IP（api_keys.last_used_ip）。
//   - 17：API 密钥允许使用的 DNS 账户集合（api_keys.allowed_dns_account_ids）。
//   - 18：安全通知事件与投递队列（security_events）。
//   - 19：多 DNS 账户按域名路由（tasks.dns_routes / certificate_requests.dns_routes）。
//   - 20：安全事件保存旧 SMTP 配置快照（security_events.smtp_snapshot）。
//   - 21：任务可选择不保存证书私钥（tasks.no_key_storage）。
//   - 22：插件安装记录保存签名索引身份（plugin_installations.index_revision/index_digest/entry_digest）。
//   - 23：零信手动 DNS 申请订单（zotrus_orders）。
const schemaVersion = 23

func migrate(conn *Handle) error {
	version, err := conn.SchemaVersion()
	if err != nil {
		return fmt.Errorf("读取数据库结构版本失败: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}

	// PostgreSQL/MySQL 需要先创建版本元数据表；SQLite 使用 PRAGMA user_version。
	if conn.Dialect().IsPostgres() || conn.Dialect().IsMySQL() {
		if err := conn.ensureSchemaMeta(); err != nil {
			return fmt.Errorf("初始化结构版本表失败: %w", err)
		}
	}

	// 旧版本的在途 CSR 唯一索引只包含 (csr_sha256, ca, api_key_id)，
	// 加入 DNS 账户维度后必须重建，否则同 CSR 换账户会被旧索引拦截。
	// MySQL 不支持 DROP INDEX IF EXISTS；生成列唯一索引由 ensureMySQLActiveIndex 幂等维护。
	if !conn.Dialect().IsMySQL() {
		if _, err := conn.Exec(`DROP INDEX IF EXISTS idx_cert_request_active_hash`); err != nil {
			return fmt.Errorf("重建 CSR 唯一索引失败: %w", err)
		}
	}

	// 新唯一索引引用 dns_routes_hash；旧库必须先补列，否则建索引会失败。
	// 全新数据库还没有表，跳过这里，由下方建表语句直接包含新列。
	if tableHasColumn(conn, "certificate_requests", "dns_account_id") {
		if err := ensureColumn(conn, "certificate_requests", "dns_routes", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
		if err := ensureColumn(conn, "certificate_requests", "dns_routes_hash", `TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	if tableHasColumn(conn, "tasks", "dns_account_id") {
		if err := ensureColumn(conn, "tasks", "dns_routes", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}

	stmts := schemaStatements(conn.Dialect())

	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			// MySQL 的 CREATE INDEX 不支持 IF NOT EXISTS；迁移中断后重跑时
			// 已存在的索引可以安全跳过。
			upper := strings.ToUpper(strings.TrimSpace(stmt))
			if conn.Dialect().IsMySQL() && isMySQLDuplicateIndexError(err) &&
				(strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX")) {
				continue
			}
			return fmt.Errorf("初始化数据库表失败: %w", err)
		}
	}
	if conn.Dialect().IsMySQL() {
		if err := ensureMySQLActiveIndex(context.Background(), conn); err != nil {
			return err
		}
	}
	// 兼容旧数据库：为已有 tasks 表补充任务流程字段。
	if err := ensureColumn(conn, "tasks", "pipeline", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧数据库：API 密钥的来源 IP/CIDR 白名单与最近使用 IP。
	if err := ensureColumn(conn, "api_keys", "allowed_ips", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := ensureColumn(conn, "api_keys", "last_used_ip", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := ensureColumn(conn, "api_keys", "allowed_dns_account_ids", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧数据库：DNS 账户与目标凭据的可用性验证结果。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"verify_status", `TEXT NOT NULL DEFAULT ''`},
		{"verify_message", `TEXT NOT NULL DEFAULT ''`},
		{"verify_mode", `TEXT NOT NULL DEFAULT ''`},
		{"verified_at", `DATETIME`},
	} {
		if err := ensureColumn(conn, "dns_accounts", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
		if err := ensureColumn(conn, "deploy_accounts", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	// 兼容旧数据库：logs 表合并进程日志所需字段。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"category", `TEXT NOT NULL DEFAULT 'operation'`},
		{"ref_type", `TEXT NOT NULL DEFAULT ''`},
		{"fields", `TEXT NOT NULL DEFAULT ''`},
		{"request_id", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(conn, "logs", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	// 新索引依赖上面的列，必须在 ensureColumn 之后创建；旧库执行到这里时
	// 才具备 category/ref_type/fields/request_id。
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_logs_category_created ON logs(category, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_category_source_created ON logs(category, source, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_category_level_created ON logs(category, level, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_category_id ON logs(category, id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_ref ON logs(ref_type, ref_id, created_at)`,
	} {
		if err := execIndex(conn, stmt); err != nil {
			return fmt.Errorf("创建日志索引失败: %w", err)
		}
	}
	// 兼容旧数据库：为已有 sessions 表补充通行密钥强认证标记。
	if err := ensureColumn(conn, "sessions", "strong_auth", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧数据库：为已有 sessions 表补充会话级再认证有效期。
	if err := ensureColumn(conn, "sessions", "reauth_until", `DATETIME`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧 ACME 订单表：补充 ARI replaces 字段。
	if err := ensureColumn(conn, "acme_orders", "replaces", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧任务表：补充 CA 调度组引用。
	if err := ensureColumn(conn, "tasks", "ca_pool_id", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧任务表：补充目标流程 Beta 的模式与配置。
	if err := ensureColumn(conn, "tasks", "flow_mode", `TEXT NOT NULL DEFAULT 'standard'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := ensureColumn(conn, "tasks", "target_flow", `TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := ensureColumn(conn, "tasks", "no_key_storage", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧监控表：记录调用方是否有权监控内网/回环目标。历史数据默认 0，
	// 由管理员重新保存后再开启内网拨号，避免升级后立即放开内网探测。
	if err := ensureColumn(conn, "monitors", "allow_internal", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	if err := execIndex(conn, `CREATE INDEX IF NOT EXISTS idx_tasks_ca_pool ON tasks(ca_pool_id)`); err != nil {
		return fmt.Errorf("创建任务 CA 调度组索引失败: %w", err)
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
		{"csr_request_id", `INTEGER NOT NULL DEFAULT 0`},
		{"revoked_at", `DATETIME`},
		{"revoke_reason", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(conn, "certificates", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	if err := execIndex(conn, `CREATE INDEX IF NOT EXISTS idx_cert_csr_request ON certificates(csr_request_id)`); err != nil {
		return fmt.Errorf("创建证书申请索引失败: %w", err)
	}
	if err := ensureColumn(conn, "private_cas", "crl_number", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧 CSR 申请表：记录 DNS 账户是显式指定还是按 zone 绑定解析。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"resolved_by", `TEXT NOT NULL DEFAULT ''`},
		{"resolved_zone", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(conn, "certificate_requests", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	// 兼容旧库：通知目标变更事件需要保存旧 SMTP 配置快照（密文）。
	if err := ensureColumn(conn, "security_events", "smtp_snapshot", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("升级数据库结构失败: %w", err)
	}
	// 兼容旧库：插件安装记录保存签名索引 revision 与条目摘要，供审计与回滚排查。
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"index_revision", `INTEGER NOT NULL DEFAULT 0`},
		{"index_digest", `TEXT NOT NULL DEFAULT ''`},
		{"entry_digest", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(conn, "plugin_installations", column.name, column.definition); err != nil {
			return fmt.Errorf("升级数据库结构失败: %w", err)
		}
	}
	// 把旧版本中已吊销的证书回填到独立吊销表，后续删除证书不会丢失吊销状态。
	// 极旧数据库的 certificates 表可能还没有这些列，此时跳过回填（没有可回填的数据）。
	if tableHasColumn(conn, "certificates", "private_ca_id") &&
		tableHasColumn(conn, "certificates", "serial") &&
		tableHasColumn(conn, "certificates", "revoked_at") &&
		tableHasColumn(conn, "certificates", "revoke_reason") &&
		tableHasColumn(conn, "certificates", "created_at") {
		if _, err := conn.Exec(`INSERT INTO revocations(ca_id, serial, revoked_at, reason, created_at)
			SELECT private_ca_id, UPPER(serial), revoked_at, revoke_reason, COALESCE(revoked_at, created_at)
			FROM certificates
			WHERE revoked_at IS NOT NULL AND private_ca_id > 0 AND serial <> ''
			ON CONFLICT(ca_id, serial) DO NOTHING`); err != nil {
			return fmt.Errorf("回填吊销记录失败: %w", err)
		}
	}
	if err := conn.SetSchemaVersion(schemaVersion); err != nil {
		return fmt.Errorf("写入数据库结构版本失败: %w", err)
	}
	return nil
}

// Migrate 执行结构迁移，供切换目标等场景在连接后显式调用。
func (h *Handle) Migrate() error { return migrate(h) }

// CurrentSchemaVersion 返回当前程序的基础表结构版本。
func CurrentSchemaVersion() int { return schemaVersion }

// ensureSchemaMeta 创建非 SQLite 后端的结构版本表。
func (h *Handle) ensureSchemaMeta() error {
	if h.Dialect().IsMySQL() {
		_, err := h.DB.Exec("CREATE TABLE IF NOT EXISTS schema_meta (`key` VARCHAR(64) PRIMARY KEY, `value` LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
		return err
	}
	_, err := h.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

// SchemaVersion 读取基础表结构版本。
func (h *Handle) SchemaVersion() (int, error) {
	if h.Dialect().IsPostgres() || h.Dialect().IsMySQL() {
		if err := h.ensureSchemaMeta(); err != nil {
			return 0, err
		}
		var (
			value string
			query string
		)
		if h.Dialect().IsMySQL() {
			query = "SELECT `value` FROM schema_meta WHERE `key`='schema.version'"
		} else {
			query = `SELECT value FROM schema_meta WHERE key='schema.version'`
		}
		err := h.DB.QueryRow(query).Scan(&value)
		if err == sql.ErrNoRows {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		version := 0
		_, scanErr := fmt.Sscanf(value, "%d", &version)
		return version, scanErr
	}
	var version int
	err := h.DB.QueryRow(`PRAGMA user_version`).Scan(&version)
	return version, err
}

// SetSchemaVersion 写入基础表结构版本。
func (h *Handle) SetSchemaVersion(version int) error {
	if h.Dialect().IsMySQL() {
		_, err := h.Exec("INSERT INTO schema_meta(`key`,`value`) VALUES('schema.version',?) ON DUPLICATE KEY UPDATE `value`=VALUES(`value`)", fmt.Sprintf("%d", version))
		return err
	}
	if h.Dialect().IsPostgres() {
		_, err := h.Exec(`INSERT INTO schema_meta(key,value) VALUES('schema.version',?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("%d", version))
		return err
	}
	_, err := h.DB.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version))
	return err
}

// tableHasColumn 判断表是否已有指定列，用于兼容极旧数据库的迁移。
func tableHasColumn(conn *Handle, table, column string) bool {
	found, err := hasColumn(conn, table, column)
	return err == nil && found
}

func hasColumn(conn *Handle, table, column string) (bool, error) {
	if conn.Dialect().IsMySQL() {
		var count int64
		err := conn.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&count)
		return count > 0, err
	}
	if conn.Dialect().IsPostgres() {
		var exists bool
		err := conn.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name=? AND column_name=?
		)`, table, column).Scan(&exists)
		return exists, err
	}
	rows, err := conn.DB.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
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
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ensureColumn 在缺少指定列时执行 ALTER TABLE，用于轻量级结构迁移。
func ensureColumn(conn *Handle, table, column, definition string) error {
	found, err := hasColumn(conn, table, column)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if conn.Dialect().IsPostgres() {
		definition = datetimePattern.ReplaceAllString(definition, "TIMESTAMPTZ")
		if column == "id" || strings.HasSuffix(column, "_id") {
			definition = strings.Replace(definition, "INTEGER", "BIGINT", 1)
		}
	}
	if conn.Dialect().IsMySQL() {
		definition = datetimePattern.ReplaceAllString(definition, "DATETIME(6)")
		definition = strings.ReplaceAll(definition, "INTEGER", "BIGINT")
		if strings.Contains(strings.ToUpper(definition), "TEXT") {
			if replacement, ok := mysqlTextOverrides[table+"."+column]; ok {
				definition = mysqlTextTypePattern.ReplaceAllString(definition, replacement)
			} else if strings.Contains(strings.ToUpper(definition), "DEFAULT") {
				// 迁移新增列使用带默认值的 VARCHAR，避免 MySQL 5.7 拒绝 TEXT DEFAULT。
				definition = mysqlTextTypePattern.ReplaceAllString(definition, "VARCHAR(2048)")
			} else {
				definition = mysqlTextTypePattern.ReplaceAllString(definition, "LONGTEXT")
			}
		}
	}
	_, err = conn.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	if err != nil {
		return fmt.Errorf("为 %s 添加列 %s 失败: %w", table, column, err)
	}
	return nil
}

// execIndex 执行建索引语句。MySQL 不支持 IF NOT EXISTS，迁移重跑时忽略重复索引错误。
func execIndex(conn *Handle, stmt string) error {
	if conn.Dialect().IsMySQL() {
		stmt = strings.Replace(stmt, "IF NOT EXISTS ", "", 1)
	}
	_, err := conn.Exec(stmt)
	if err != nil && conn.Dialect().IsMySQL() && isMySQLDuplicateIndexError(err) {
		return nil
	}
	return err
}
