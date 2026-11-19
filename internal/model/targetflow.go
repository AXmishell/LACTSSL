package model

import "time"

// 任务流程模式。standard 是原有的「先申请、再部署」流程；
// target_beta 会先读取目标站点，再按证书分配策略申请与部署。
const (
	FlowStandard   = "standard"
	FlowTargetBeta = "target_beta"
)

// 目标流程 Beta 的证书分配策略。
const (
	StrategySmart         = "smart"
	StrategyPerSite       = "per_site"
	StrategyPerBaseDomain = "per_base_domain"
	StrategySingle        = "single"
	StrategyPerSource     = "per_source"
	StrategyCustom        = "custom"
)

// 站点在预览结果中的状态。
const (
	SiteStatusReady     = "ready"
	SiteStatusCovered   = "covered"
	SiteStatusDuplicate = "duplicate"
	SiteStatusInvalid   = "invalid"
	SiteStatusSkipped   = "skipped"
)

// 站点选取模式。all 会动态纳入新增站点，selected 只保留用户显式选中的稳定键。
const (
	SelectionAll      = "all"
	SelectionSelected = "selected"
	SelectionSmart    = "smart"
)

// TargetFlowConfig 是目标流程 Beta 的持久化配置。
// 只保存部署参数，不保存任何目标凭据密钥。
type TargetFlowConfig struct {
	Sources   []TargetSource `json:"sources,omitempty"`
	Selection SiteSelection  `json:"selection"`
	Policy    CertPolicy     `json:"policy"`
	// Overrides 用于「自定义分组」：每个分组显式列出站点稳定键。
	Overrides []CertGroupOverride `json:"group_overrides,omitempty"`
	// LastResult 只保留最近一次执行的摘要，供任务列表展示。
	LastResult *TargetFlowResult `json:"last_result,omitempty"`
}

// TargetSource 描述一个用于读取站点的部署目标。
type TargetSource struct {
	DeployAccountID int64             `json:"deploy_account_id"`
	DeployConfig    map[string]string `json:"deploy_config,omitempty"`
}

// SiteSelection 描述站点选取规则。
type SiteSelection struct {
	// all | selected | smart
	Mode           string   `json:"mode"`
	Keys           []string `json:"keys,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

// CertPolicy 描述证书分配策略。
type CertPolicy struct {
	// smart | per_site | per_base_domain | single | per_source | custom
	Strategy          string              `json:"strategy"`
	MaxDomainsPerCert int                 `json:"max_domains_per_cert,omitempty"`
	ExtraDomains      map[string][]string `json:"extra_domains,omitempty"`
}

// CertGroupOverride 是自定义分组中的一个证书组。
type CertGroupOverride struct {
	Key     string   `json:"key"`
	Label   string   `json:"label,omitempty"`
	Domains []string `json:"domains,omitempty"`
	Sites   []string `json:"sites,omitempty"`
}

// TargetFlowResult 是最近一次目标流程执行的摘要。
type TargetFlowResult struct {
	DiscoveredAt  time.Time `json:"discovered_at"`
	SiteCount     int       `json:"site_count"`
	GroupCount    int       `json:"group_count"`
	CertCount     int       `json:"cert_count"`
	DeployedCount int       `json:"deployed_count"`
	FailedCount   int       `json:"failed_count"`
	Warnings      []string  `json:"warnings,omitempty"`
}

// TargetFlowSummary 是任务列表使用的轻量摘要。
type TargetFlowSummary struct {
	SiteCount  int `json:"site_count"`
	GroupCount int `json:"group_count"`
	CertCount  int `json:"cert_count"`
}

// TargetSite 是目标系统中读取到的一个可部署站点。
type TargetSite struct {
	// Key 是跨次发现的稳定键：accountID|identifier，缺省时退回 accountID|value。
	Key            string   `json:"key"`
	AccountID      int64    `json:"account_id"`
	AccountName    string   `json:"account_name,omitempty"`
	AccountType    string   `json:"account_type,omitempty"`
	Value          string   `json:"value"`
	Name           string   `json:"name,omitempty"`
	Domains        []string `json:"domains"`
	Selected       bool     `json:"selected"`
	Status         string   `json:"status"`
	Warnings       []string `json:"warnings,omitempty"`
	SiteField      string   `json:"site_field,omitempty"`
	MultipleValues bool     `json:"multiple_values,omitempty"`
}

// CertGroup 是一张待申请/待复用证书及其关联站点。
type CertGroup struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Domains     []string     `json:"domains"`
	Sites       []TargetSite `json:"sites"`
	Warnings    []string     `json:"warnings,omitempty"`
	ReuseCertID int64        `json:"reuse_cert_id,omitempty"`
}

// StrategyRecommendation 是智能策略推荐结果。
type StrategyRecommendation struct {
	Strategy string   `json:"strategy"`
	Reasons  []string `json:"reasons,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// TargetFlowPreview 是预览接口返回的站点与证书分组。
type TargetFlowPreview struct {
	DiscoveredAt   time.Time              `json:"discovered_at"`
	Sites          []TargetSite           `json:"sites"`
	Groups         []CertGroup            `json:"groups"`
	Recommendation StrategyRecommendation `json:"recommendation"`
	Warnings       []string               `json:"warnings,omitempty"`
	Summary        TargetFlowSummary      `json:"summary"`
}
