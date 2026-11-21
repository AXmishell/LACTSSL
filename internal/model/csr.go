package model

import "time"

// CSR 申请状态。CSR 申请由客户端提交私钥对应的证书请求，服务端作为
// 中间环节向公共 CA 完成域名验证，或使用私有 CA 直接签名，因此不会保存私钥。
const (
	CSRStatusPending   = "pending"
	CSRStatusRunning   = "running"
	CSRStatusIssued    = "issued"
	CSRStatusFailed    = "failed"
	CSRStatusCancelled = "cancelled"
	CSRStatusExpired   = "expired"
)

// CSRRequest 保存一次客户端 CSR 证书申请。
// CSRPEM 只包含公钥与域名声明，不含私钥；证书签发后私钥仍由客户端持有。
type CSRRequest struct {
	ID            int64    `json:"-"`
	PublicID      string   `json:"id"`
	CSRPEM        string   `json:"-"`
	CSRHash       string   `json:"csr_sha256"`
	Identifiers   []string `json:"identifiers"`
	PrimaryDomain string   `json:"primary_domain"`
	CA            string   `json:"ca"`
	Challenge     string   `json:"challenge"`
	DNSAccountID  int64    `json:"dns_account_id,omitempty"`
	// DNSRoutes 是创建申请时按域名解析出的 DNS 账户快照，跨账户 CSR 使用该字段。
	DNSRoutes []DNSRoute `json:"dns_routes,omitempty"`
	// DNSRoutesHash 是 DNSRoutes 的稳定摘要，用于在途申请去重与幂等判断。
	DNSRoutesHash string `json:"-"`
	Status        string `json:"status"`
	// ErrorCode 是稳定的错误标识，ErrorMessage 是可翻译的错误信封。
	// 两者都由后台 Worker 写入，查询接口按请求语言渲染 ErrorMessage。
	ErrorCode     string `json:"-"`
	ErrorMessage  string `json:"-"`
	Retryable     bool   `json:"retryable"`
	Attempts      int    `json:"attempts"`
	CertificateID int64  `json:"certificate_id,omitempty"`
	// ResolvedBy 记录 DNS 账户的来源：explicit 表示调用方显式指定，
	// binding 表示按管理员维护的 zone 绑定自动解析。
	ResolvedBy string `json:"resolved_by,omitempty"`
	// ResolvedZone 是自动解析时命中的最长 zone，用于审计与排障。
	ResolvedZone    string     `json:"resolved_zone,omitempty"`
	UpstreamOrderID string     `json:"-"`
	IdempotencyKey  string     `json:"-"`
	UserID          int64      `json:"-"`
	APIKeyID        int64      `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}
