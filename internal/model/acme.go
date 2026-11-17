package model

import "time"

// ACMECA 是「权威CA」中用户配置的通用 ACME 上游。
// 一个系统可以配置多个上游，使用不同的目录地址或协议版本。
type ACMECA struct {
	ID int64 `json:"id"`
	// PublicID 是对外展示的随机标识，避免暴露自增 ID。
	PublicID string `json:"public_id"`
	Name     string `json:"name"`
	// DirectoryURL 是上游 ACME 目录（directory）地址。
	DirectoryURL string `json:"directory_url"`
	// Protocol 为 v1 或 v2，默认 v2。
	Protocol string `json:"protocol"`
	// EABRequired 表示注册账户时必须使用 External Account Binding。
	EABRequired bool   `json:"eab_required"`
	EABKid      string `json:"eab_kid"`
	// EABHMAC 只在服务端内部使用，接口响应不返回明文。
	EABHMAC    string    `json:"-"`
	EABHMACSet bool      `json:"eab_hmac_set"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ACMEEndpoint 是一个对外提供 ACME 协议的端点，绑定到指定的私有 CA。
type ACMEEndpoint struct {
	ID                    int64     `json:"id"`
	PublicID              string    `json:"public_id"`
	Name                  string    `json:"name"`
	Slug                  string    `json:"slug"`
	CAID                  int64     `json:"ca_id"`
	Enabled               bool      `json:"enabled"`
	EABRequired           bool      `json:"eab_required"`
	AllowedDomains        []string  `json:"allowed_domains"`
	AllowWildcard         bool      `json:"allow_wildcard"`
	AllowIP               bool      `json:"allow_ip"`
	AllowedIPCIDRs        []string  `json:"allowed_ip_cidrs"`
	HTTP01Enabled         bool      `json:"http01_enabled"`
	DNS01Enabled          bool      `json:"dns01_enabled"`
	TLSALPN01Enabled      bool      `json:"tls_alpn_01_enabled"`
	AllowPrivateHTTP01    bool      `json:"allow_private_http01"`
	AllowPrivateTLSALPN01 bool      `json:"allow_private_tls_alpn_01"`
	HTTP01AllowedCIDRs    []string  `json:"http01_allowed_cidrs"`
	TLSALPN01AllowedCIDRs []string  `json:"tls_alpn_01_allowed_cidrs"`
	ValidityDays          int       `json:"validity_days"`
	MaxOrdersPerHour      int       `json:"max_orders_per_hour"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// ACMEAccount 是 ACME 客户端注册的账户。服务端只保存公钥，不保存账户私钥。
type ACMEAccount struct {
	ID         int64      `json:"id"`
	PublicID   string     `json:"public_id"`
	EndpointID int64      `json:"endpoint_id"`
	Kid        string     `json:"kid"`
	PublicJWK  string     `json:"-"`
	Thumbprint string     `json:"thumbprint"`
	Contact    []string   `json:"contact"`
	Status     string     `json:"status"`
	EABID      int64      `json:"eab_id"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ACMEEABCredential 是 ACME External Account Binding 凭据。
// HMAC 明文只在创建时返回一次，落库前会加密。
type ACMEEABCredential struct {
	ID         int64      `json:"id"`
	PublicID   string     `json:"public_id"`
	EndpointID int64      `json:"endpoint_id"`
	Kid        string     `json:"kid"`
	Label      string     `json:"label"`
	HMAC       string     `json:"-"`
	HMACSet    bool       `json:"hmac_set"`
	UsedAt     *time.Time `json:"used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ACMEOrder 是 ACME 订单。订单、授权与挑战端点会在后续阶段接入。
type ACMEOrder struct {
	ID            int64      `json:"id"`
	PublicID      string     `json:"public_id"`
	EndpointID    int64      `json:"endpoint_id"`
	AccountID     int64      `json:"account_id"`
	Status        string     `json:"status"`
	Identifiers   []string   `json:"identifiers"`
	Replaces      string     `json:"replaces,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CertificateID int64      `json:"certificate_id"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ACMEAuthorization 是订单中某个标识的授权状态。
type ACMEAuthorization struct {
	ID             int64      `json:"id"`
	PublicID       string     `json:"public_id"`
	OrderID        int64      `json:"order_id"`
	Identifier     string     `json:"identifier"`
	IdentifierType string     `json:"identifier_type"`
	Wildcard       bool       `json:"wildcard"`
	Status         string     `json:"status"`
	ExpiresAt      *time.Time `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// Challenges 仅用于创建订单时的事务写入，不直接返回给前端。
	Challenges []ACMEChallenge `json:"-"`
}

// ACMEChallenge 是授权下的一个验证挑战。
type ACMEChallenge struct {
	ID          int64      `json:"id"`
	PublicID    string     `json:"public_id"`
	AuthzID     int64      `json:"authz_id"`
	Type        string     `json:"type"`
	Token       string     `json:"token"`
	Status      string     `json:"status"`
	ValidatedAt *time.Time `json:"validated_at"`
	Error       string     `json:"error"`
	Attempts    int        `json:"attempts"`
	NextCheckAt *time.Time `json:"next_check_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
