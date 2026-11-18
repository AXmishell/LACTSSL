package model

import "time"

// CSR 申请状态。CSR 申请由客户端提交私钥对应的证书请求，
// 服务端只作为中间环节向权威 CA 完成域名验证与签发，因此不会保存私钥。
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
	Status        string   `json:"status"`
	// ErrorCode 是稳定的错误标识，ErrorMessage 是可翻译的错误信封。
	// 两者都由后台 Worker 写入，查询接口按请求语言渲染 ErrorMessage。
	ErrorCode       string     `json:"-"`
	ErrorMessage    string     `json:"-"`
	Retryable       bool       `json:"retryable"`
	Attempts        int        `json:"attempts"`
	CertificateID   int64      `json:"certificate_id,omitempty"`
	UpstreamOrderID string     `json:"-"`
	IdempotencyKey  string     `json:"-"`
	UserID          int64      `json:"-"`
	APIKeyID        int64      `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}
