package model

// DNSRoute 描述一张证书中某个域名由哪个 DNS 账户完成 DNS-01 验证。
// 一张证书可以包含多个域名，不同域名允许使用不同账户；同一域名只能路由到一个账户。
type DNSRoute struct {
	Domain       string `json:"domain"`
	Zone         string `json:"zone,omitempty"`
	DNSAccountID int64  `json:"dns_account_id"`
	MatchMode    string `json:"match_mode,omitempty"`
	Priority     int    `json:"priority,omitempty"`
	// ResolvedBy 记录路由来源：explicit 表示显式指定账户，binding 表示按域名绑定解析。
	ResolvedBy string `json:"resolved_by,omitempty"`
}
