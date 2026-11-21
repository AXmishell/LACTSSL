// Package model 定义数据库实体与接口返回结构。
package model

import (
	"strings"
	"time"
)

// User 是系统唯一的个人账户。
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	TOTPSecret   string `json:"-"`
	TOTPEnabled  bool   `json:"totp_enabled"`
	TokenVersion int    `json:"-"`
	// StrongAuth 表示当前请求的会话已通过通行密钥登录。
	// 仅用于接口返回，不落库到用户表。
	StrongAuth bool      `json:"strong_auth,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Passkey 保存一个通行密钥（WebAuthn 凭据）。
// Credential 是 webauthn.Credential 的 JSON 序列化结果，包含公钥、计数器等非敏感信息。
type Passkey struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"-"`
	Name         string     `json:"name"`
	CredentialID string     `json:"credential_id"`
	Credential   string     `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

// APIKey 是供程序调用的长期访问密钥。
// TokenHash 只保存密钥摘要，明文仅在创建或轮换时返回一次。
type APIKey struct {
	ID        int64    `json:"id"`
	UserID    int64    `json:"-"`
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	TokenHash string   `json:"-"`
	Scopes    []string `json:"scopes"`
	// AllowedIPs 是允许使用该密钥的来源 IP/CIDR 白名单；空数组表示不限制。
	AllowedIPs []string `json:"allowed_ips"`
	// AllowedDNSAccountIDs 限制该密钥可以使用的 DNS 账户；空数组表示不限制。
	// 该限制同时约束 CSR 自动解析、任务创建/运行以及 DNS 账户管理接口。
	AllowedDNSAccountIDs []int64    `json:"allowed_dns_account_ids"`
	Enabled              bool       `json:"enabled"`
	ExpiresAt            *time.Time `json:"expires_at"`
	LastUsedAt           *time.Time `json:"last_used_at"`
	// LastUsedIP 是最近一次成功认证时的来源 IP，用于辅助配置白名单。
	LastUsedIP string    `json:"last_used_ip"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// HasScope 判断密钥是否拥有指定权限。支持 "*" 与 "resource:*" 通配。
func (k *APIKey) HasScope(scope string) bool {
	if k == nil {
		return false
	}
	for _, value := range k.Scopes {
		value = strings.TrimSpace(value)
		if value == "*" || value == scope {
			return true
		}
		if strings.HasSuffix(value, ":*") && strings.HasPrefix(scope, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

// DNSAccountRestricted 表示该密钥只能使用 AllowedDNSAccountIDs 中的账户。
func (k *APIKey) DNSAccountRestricted() bool {
	return k != nil && len(k.AllowedDNSAccountIDs) > 0
}

// AllowsDNSAccount 判断密钥是否可以使用指定 DNS 账户。
// 未配置允许列表（或非 API 密钥身份）时返回 true。
func (k *APIKey) AllowsDNSAccount(id int64) bool {
	if k == nil || len(k.AllowedDNSAccountIDs) == 0 {
		return true
	}
	for _, allowed := range k.AllowedDNSAccountIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

// Session 是一次登录对应的服务端会话记录。
// ID 与 JWT 的 jti 对应，撤销后该令牌立即失效。
type Session struct {
	ID         string     `json:"id"`
	UserID     int64      `json:"-"`
	IP         string     `json:"ip"`
	UserAgent  string     `json:"user_agent"`
	StrongAuth bool       `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"-"`
	Current    bool       `json:"current"`
}

// DNSAccount 保存 DNS 服务商凭据，Config 在落库前会加密。
type DNSAccount struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config"`
	Remark   string            `json:"remark"`
	// 以下字段记录最近一次可用性验证结果，仅用于展示与预检，不参与签发。
	VerifyStatus  string     `json:"verify_status"`
	VerifyMessage string     `json:"verify_message"`
	VerifyMode    string     `json:"verify_mode"`
	VerifiedAt    *time.Time `json:"verified_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// DNSZoneBinding 声明一个 DNS 账户负责管理的域名后缀或精确域名。
// 服务端据此把 CSR/Task 中的域名解析到唯一 DNS 账户，避免调用方猜 ID。
type DNSZoneBinding struct {
	ID           int64 `json:"id"`
	DNSAccountID int64 `json:"dns_account_id"`
	// Zone 是规范化后的域名：小写、无尾点、不含通配符。
	Zone string `json:"zone"`
	// MatchMode 为 suffix 时覆盖该 zone 及其子域；exact 时只匹配该域名。
	MatchMode string `json:"match_mode"`
	// Priority 在 zone 长度相同时决定优先级，数值越大越优先。
	Priority int `json:"priority"`
	// Source 记录绑定来源：manual、verify（探测回填）或 import。
	Source    string    `json:"source"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// TrustedCA 是导入到系统 TLS 信任链的 CA 公钥证书。
// 仅保存公钥证书，不包含私钥；用于校验证书监控、通知与目标凭据插件访问的内部 HTTPS 服务。
type TrustedCA struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	Serial      string    `json:"serial"`
	Fingerprint string    `json:"fingerprint"`
	CertPEM     string    `json:"cert_pem"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	CreatedAt   time.Time `json:"created_at"`
}

// DeployAccount 保存证书目标凭据的认证与连接信息。
// 证书路径、站点、域名等部署参数保存在任务流程的部署节点中。
type DeployAccount struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
	Remark string            `json:"remark"`
	// 以下字段记录最近一次可用性验证结果，仅用于展示与预检，不参与部署。
	VerifyStatus  string     `json:"verify_status"`
	VerifyMessage string     `json:"verify_message"`
	VerifyMode    string     `json:"verify_mode"`
	VerifiedAt    *time.Time `json:"verified_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Task 表示一个证书申请/续期任务。
type Task struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Domains      []string `json:"domains"`
	Challenge    string   `json:"challenge"`
	DNSAccountID int64    `json:"dns_account_id"`
	// DNSRoutes 是申请时按域名解析出的 DNS 账户快照。
	// 单账户任务可以只依赖 DNSAccountID；跨账户任务使用该字段。
	DNSRoutes []DNSRoute `json:"dns_routes,omitempty"`
	CA        string     `json:"ca"`
	CAPoolID  int64      `json:"ca_pool_id"`
	KeyType   string     `json:"key_type"`
	// NoKeyStorage 表示任务签发后不保存证书私钥；私钥仅在本次执行期间驻留内存。
	NoKeyStorage bool           `json:"no_key_storage"`
	AutoRenew    bool           `json:"auto_renew"`
	RenewDays    int            `json:"renew_days"`
	DeployIDs    []int64        `json:"deploy_ids"`
	Pipeline     []PipelineNode `json:"pipeline"`
	// FlowMode 为空时按普通流程处理，兼容旧任务与旧客户端。
	FlowMode   string           `json:"flow_mode,omitempty"`
	TargetFlow TargetFlowConfig `json:"target_flow,omitempty"`
	// TargetFlowSummary 是接口层按最近一次执行结果填充的展示字段，不落库。
	TargetFlowSummary *TargetFlowSummary `json:"target_flow_summary,omitempty"`
	Enabled           bool               `json:"enabled"`
	Status            string             `json:"status"`
	Message           string             `json:"message"`
	LastRunAt         *time.Time         `json:"last_run_at"`
	NextRunAt         *time.Time         `json:"next_run_at"`
	CertID            int64              `json:"cert_id"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// Certificate 保存签发得到的证书内容。
type Certificate struct {
	ID            int64    `json:"id"`
	TaskID        int64    `json:"task_id"`
	Domains       []string `json:"domains"`
	PrimaryDomain string   `json:"primary_domain"`
	Issuer        string   `json:"issuer"`
	CA            string   `json:"ca"`
	Source        string   `json:"source"`
	PrivateCAID   int64    `json:"private_ca_id,omitempty"`
	ACMEOrderID   int64    `json:"acme_order_id,omitempty"`
	ACMEAccountID int64    `json:"acme_account_id,omitempty"`
	// CSRRequestID 关联通过 CSR 申请签发的来源记录。
	CSRRequestID int64 `json:"csr_request_id,omitempty"`
	// KeyAvailable 表示服务端是否保存了证书私钥。
	// CSR 申请与 ACME Server 签发的证书没有私钥，前端据此隐藏私钥下载入口。
	KeyAvailable bool       `json:"key_available"`
	CertPEM      string     `json:"cert_pem"`
	KeyPEM       string     `json:"-"`
	FullchainPEM string     `json:"fullchain_pem"`
	Serial       string     `json:"serial"`
	NotBefore    time.Time  `json:"not_before"`
	NotAfter     time.Time  `json:"not_after"`
	Status       string     `json:"status"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason int        `json:"revoke_reason,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
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
	// AllowInternal 表示该监控项由具备内网监控权限的调用方创建/更新，
	// 允许在拨号阶段连接到私网/回环地址。
	AllowInternal bool       `json:"allow_internal"`
	LastCheckAt   *time.Time `json:"last_check_at"`
	NextCheckAt   *time.Time `json:"next_check_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// 日志分类。operation 是业务/操作日志，runtime 是进程运行日志。
// 两类日志共用 logs 表，通过 category 区分，保留策略与清空操作按分类隔离。
const (
	LogCategoryOperation = "operation"
	LogCategoryRuntime   = "runtime"
)

// LogEntry 是统一的日志记录。业务日志与进程日志都落入 logs 表：
//   - 业务日志：Category=operation，Source 为业务来源，Message 是 i18n 信封；
//   - 进程日志：Category=runtime，Source 为组件名，Fields 是结构化字段 JSON。
type LogEntry struct {
	ID        int64     `json:"id"`
	Category  string    `json:"category,omitempty"`
	Source    string    `json:"source"`
	RefType   string    `json:"ref_type,omitempty"`
	RefID     int64     `json:"ref_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Fields    string    `json:"-"`
	RequestID string    `json:"request_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SystemLogEntry 是进程底层日志，供「系统日志」页面查看。
// Fields 是附加字段的 JSON 文本，接口层解析后返回给前端。
type SystemLogEntry struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Component string `json:"component"`
	Message   string `json:"message"`
	Fields    string `json:"-"`
	// RefType/RefID 从 Fields 中的 task_id、monitor_id、cert_id 提取，
	// 便于任务详情和运行日志按对象关联；不直接暴露给旧接口。
	RefType   string    `json:"-"`
	RefID     int64     `json:"-"`
	RequestID string    `json:"-"`
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
	// ServerTime 由接口在响应前填充，供仪表盘显示 LACTSSL 系统时间。
	ServerTime          time.Time `json:"server_time"`
	ServerTimezone      string    `json:"server_timezone"`
	ServerOffsetSeconds int       `json:"server_utc_offset"`
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
