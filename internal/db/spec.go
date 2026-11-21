package db

// TableSpec 描述一张需要备份/切换的数据表。
type TableSpec struct {
	Name       string
	PrimaryKey []string
	HasID      bool
}

// DataTables 返回需要复制的业务表，顺序已按外键依赖排列。
// 新增表时必须同步维护这里，切换/校验会检查表集合是否一致。
func DataTables() []TableSpec {
	return []TableSpec{
		{Name: "users", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "settings", PrimaryKey: []string{"key"}},
		{Name: "plugin_installations", PrimaryKey: []string{"kind", "id"}},
		{Name: "plugin_file_meta", PrimaryKey: []string{"kind", "filename"}},
		{Name: "plugin_metadata", PrimaryKey: []string{"kind", "id"}},
		{Name: "sessions", PrimaryKey: []string{"id"}},
		{Name: "security_events", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "passkeys", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "api_keys", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "password_resets", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "dns_accounts", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "dns_zone_bindings", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "deploy_accounts", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "private_cas", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "trusted_cas", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "certificates", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "revocations", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "tasks", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "task_certificate_links", PrimaryKey: []string{"task_id", "cert_id"}},
		{Name: "deploy_records", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "monitors", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "logs", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "system_logs", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_cas", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "ca_pools", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "ca_pool_items", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_endpoints", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_accounts", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_eab_credentials", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_orders", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_authorizations", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "acme_challenges", PrimaryKey: []string{"id"}, HasID: true},
		{Name: "certificate_requests", PrimaryKey: []string{"id"}, HasID: true},
	}
}
