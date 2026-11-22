package model

import "time"

// 零信 ZoTrus 的 dns-01 挑战不是 RFC 8555 的固定 `_acme-challenge`，
// 而是由服务端返回 tokenPath / tokenType，客户端据此写入
// `_<tokenPath>.<domain>`。这些状态在用户手动添加 DNS 记录期间需要持久化。
const (
	ZotrusOrderPending   = "pending"
	ZotrusOrderVerifying = "verifying"
	ZotrusOrderIssued    = "issued"
	ZotrusOrderFailed    = "failed"
)

// ZotrusChallenge 是一条需要用户手动添加的零信 DNS 验证记录。
type ZotrusChallenge struct {
	Domain           string `json:"domain"`
	AuthorizationURL string `json:"authorization_url"`
	ChallengeURL     string `json:"challenge_url"`
	Token            string `json:"token"`
	TokenPath        string `json:"token_path"`
	TokenType        string `json:"token_type"`
	RecordName       string `json:"record_name"`
	RecordType       string `json:"record_type"`
	RecordValue      string `json:"record_value"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
}

// ZotrusOrder 保存一次等待用户完成 DNS 验证的零信订单。
// KeyPEM 与 CSRDER 不直接暴露给接口层；KeyPEM 在 Store 中加密存储。
type ZotrusOrder struct {
	ID             int64             `json:"id"`
	TaskID         int64             `json:"task_id"`
	Domains        []string          `json:"domains"`
	KeyType        string            `json:"key_type"`
	OrderURL       string            `json:"order_url"`
	AccountURI     string            `json:"account_uri,omitempty"`
	FinalizeURL    string            `json:"finalize_url"`
	CertificateURL string            `json:"certificate_url"`
	KeyPEM         string            `json:"-"`
	CSRDER         string            `json:"-"`
	Challenges     []ZotrusChallenge `json:"challenges"`
	Status         string            `json:"status"`
	Error          string            `json:"error"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}
