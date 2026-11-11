// Package model 定义数据库实体与接口返回结构。
package model

import "time"

// User 是系统唯一的个人账户。
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	TokenVersion int       `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Session 是一次登录对应的服务端会话记录。
// ID 与 JWT 的 jti 对应，撤销后该令牌立即失效。
type Session struct {
	ID         string     `json:"id"`
	UserID     int64      `json:"-"`
	IP         string     `json:"ip"`
	UserAgent  string     `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"-"`
	Current    bool       `json:"current"`
}

// DNSAccount 保存 DNS 服务商凭据，Config 在落库前会加密。
type DNSAccount struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Provider  string            `json:"provider"`
	Config    map[string]string `json:"config"`
	Remark    string            `json:"remark"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// PrivateCA 是本地生成或导入的私有证书颁发机构。
// KeyPEM 不会通过列表接口返回，仅用于下载。
type PrivateCA struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	CommonName   string    `json:"common_name"`
	Organization string    `json:"organization"`
	Country      string    `json:"country"`
	KeyType      string    `json:"key_type"`
	Source       string    `json:"source"`
	Serial       string    `json:"serial"`
	ParentID     int64     `json:"parent_id"`
	ParentName   string    `json:"parent_name"`
	CAType       string    `json:"ca_type"`
	PathLen      int       `json:"path_len"`
	CertPEM      string    `json:"cert_pem,omitempty"`
	KeyPEM       string    `json:"-"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	CRLNumber    int64     `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DeployAccount 保存证书目标凭据的认证与连接信息。
// 证书路径、站点、域名等部署参数保存在任务流程的部署节点中。
type DeployAccount struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Config    map[string]string `json:"config"`
	Remark    string            `json:"remark"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Task 表示一个证书申请/续期任务。
type Task struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Domains      []string       `json:"domains"`
	Challenge    string         `json:"challenge"`
	DNSAccountID int64          `json:"dns_account_id"`
	CA           string         `json:"ca"`
	KeyType      string         `json:"key_type"`
	AutoRenew    bool           `json:"auto_renew"`
	RenewDays    int            `json:"renew_days"`
	DeployIDs    []int64        `json:"deploy_ids"`
	Pipeline     []PipelineNode `json:"pipeline"`
	Enabled      bool           `json:"enabled"`
	Status       string         `json:"status"`
	Message      string         `json:"message"`
	LastRunAt    *time.Time     `json:"last_run_at"`
	NextRunAt    *time.Time     `json:"next_run_at"`
	CertID       int64          `json:"cert_id"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Certificate 保存签发得到的证书内容。
type Certificate struct {
	ID            int64      `json:"id"`
	TaskID        int64      `json:"task_id"`
	Domains       []string   `json:"domains"`
	PrimaryDomain string     `json:"primary_domain"`
	Issuer        string     `json:"issuer"`
	CA            string     `json:"ca"`
	Source        string     `json:"source"`
	PrivateCAID   int64      `json:"private_ca_id,omitempty"`
	ACMEOrderID   int64      `json:"acme_order_id,omitempty"`
	ACMEAccountID int64      `json:"acme_account_id,omitempty"`
	CertPEM       string     `json:"cert_pem"`
	KeyPEM        string     `json:"-"`
	FullchainPEM  string     `json:"fullchain_pem"`
	Serial        string     `json:"serial"`
	NotBefore     time.Time  `json:"not_before"`
	NotAfter      time.Time  `json:"not_after"`
	Status        string     `json:"status"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokeReason  int        `json:"revoke_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// DeployRecord 记录一次部署结果。
type DeployRecord struct {
	ID              int64     `json:"id"`
	CertID          int64     `json:"cert_id"`
	DeployAccountID int64     `json:"deploy_account_id"`
	AccountName     string    `json:"account_name"`
	TaskID          int64     `json:"task_id"`
	Status          string    `json:"status"`
	Message         string    `json:"message"`
	CreatedAt       time.Time `json:"created_at"`
}

// Monitor 表示一个外部站点证书监控项。
type Monitor struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Target       string     `json:"target"`
	IntervalHour int        `json:"interval_hours"`
	Enabled      bool       `json:"enabled"`
	Status       string     `json:"status"`
	Message      string     `json:"message"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Issuer       string     `json:"issuer"`
	LastCheckAt  *time.Time `json:"last_check_at"`
	NextCheckAt  *time.Time `json:"next_check_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// LogEntry 是系统操作日志。
type LogEntry struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	RefID     int64     `json:"ref_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Dashboard 是首页统计数据。
type Dashboard struct {
	CertTotal     int64         `json:"cert_total"`
	CertExpiring  int64         `json:"cert_expiring"`
	CertExpired   int64         `json:"cert_expired"`
	TaskTotal     int64         `json:"task_total"`
	TaskEnabled   int64         `json:"task_enabled"`
	MonitorTotal  int64         `json:"monitor_total"`
	MonitorIssue  int64         `json:"monitor_issue"`
	DeployTotal   int64         `json:"deploy_total"`
	SuccessRate   float64       `json:"success_rate"`
	ExpiryBuckets []Bucket      `json:"expiry_buckets"`
	Trend         []TrendPoint  `json:"trend"`
	RecentTasks   []Task        `json:"recent_tasks"`
	RecentCerts   []Certificate `json:"recent_certs"`
	RecentLogs    []LogEntry    `json:"recent_logs"`
}

// Bucket 表示到期时间分布。
type Bucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// TrendPoint 表示每日签发数量。
type TrendPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}
