// Package ratelimit 定义管理端与 ACME 入口共用的限流规则。
// 规则名称同时作为 config.yaml 中 security.rate_limit.rules 的键。
package ratelimit

import (
	"sort"
	"time"
)

// Rule 描述一个窗口内允许的最大请求次数。
// Max 为 0 表示不限制；Window 必须为正数才会生效。
type Rule struct {
	Max    int
	Window time.Duration
}

// Config 是运行期使用的限流配置。
// Rules 中缺失的规则会回退到内置默认值，零值 Config 也可以直接使用。
type Config struct {
	// MaxKeys 是内存限流器最多保留的 key 数量，0 表示使用默认值。
	MaxKeys int
	// Rules 是按规则名称覆盖的限流阈值。
	Rules map[string]Rule
}

// DefaultMaxKeys 是管理端内存限流器默认保留的 key 数量。
const DefaultMaxKeys = 10000

// 规则名称。config.yaml 中 security.rate_limit.rules 使用这些键。
const (
	Setup                  = "setup"
	LoginIP                = "login_ip"
	LoginAccount           = "login_account"
	LoginTOTP              = "login_totp"
	ForgotIP               = "forgot_ip"
	ForgotAccount          = "forgot_account"
	PasskeyLoginBegin      = "passkey_login_begin"
	PasskeyLoginFail       = "passkey_login_fail"
	Reauth                 = "reauth"
	APIIP                  = "api_ip"
	APIAnonymous           = "api_anonymous"
	Entry                  = "entry"
	SetupConfig            = "setup_config"
	SetupRestart           = "setup_restart"
	SetupRestore           = "setup_restore"
	MonitorCheck           = "monitor_check"
	PrivateCACreate        = "private_ca_create"
	PrivateCAKeyExport     = "private_ca_key_export"
	CertKeyExport          = "cert_key_export"
	CertKeyView            = "cert_key_view"
	CSRCreate              = "csr_create"
	CredentialVerify       = "credential_verify"
	CredentialVerifyClient = "credential_verify_client"
	BackupDownload         = "backup_download"
	LogExport              = "log_export"
	LogClear               = "log_clear"
	SystemLogExport        = "system_log_export"
	SystemLogClear         = "system_log_clear"
	LogArchiveDownload     = "log_archive_download"
	LogArchiveDelete       = "log_archive_delete"
	DatabaseOperation      = "database_operation"
	SettingsConfigSave     = "settings_config_save"
	SettingsConfigBackup   = "settings_config_backup"
	SettingsConfigDownload = "settings_config_download"
	SettingsConfigRestore  = "settings_config_restore"
	SettingsConfigDelete   = "settings_config_delete"
	SettingsConfigRestart  = "settings_config_restart"
	NotifyTest             = "notify_test"
	EntryChange            = "entry_change"
	DeploymentsClear       = "deployments_clear"
	ACMERequest            = "acme_request"
	ACMEAccount            = "acme_account"
	ACMEPKI                = "acme_pki"
	ACMEOrder              = "acme_order"
)

var defaultRules = map[string]Rule{
	Setup:                  {Max: 5, Window: time.Minute},
	LoginIP:                {Max: 10, Window: 5 * time.Minute},
	LoginAccount:           {Max: 20, Window: 15 * time.Minute},
	LoginTOTP:              {Max: 5, Window: 10 * time.Minute},
	ForgotIP:               {Max: 5, Window: 10 * time.Minute},
	ForgotAccount:          {Max: 3, Window: time.Hour},
	PasskeyLoginBegin:      {Max: 20, Window: 5 * time.Minute},
	PasskeyLoginFail:       {Max: 10, Window: 5 * time.Minute},
	Reauth:                 {Max: 10, Window: 10 * time.Minute},
	APIIP:                  {Max: 600, Window: time.Minute},
	APIAnonymous:           {Max: 120, Window: time.Minute},
	Entry:                  {Max: 60, Window: time.Minute},
	SetupConfig:            {Max: 10, Window: time.Minute},
	SetupRestart:           {Max: 3, Window: time.Minute},
	SetupRestore:           {Max: 5, Window: time.Minute},
	MonitorCheck:           {Max: 20, Window: time.Minute},
	PrivateCACreate:        {Max: 10, Window: time.Minute},
	PrivateCAKeyExport:     {Max: 30, Window: time.Minute},
	CertKeyExport:          {Max: 30, Window: time.Minute},
	CertKeyView:            {Max: 30, Window: time.Minute},
	CSRCreate:              {Max: 20, Window: time.Hour},
	CredentialVerify:       {Max: 5, Window: time.Minute},
	CredentialVerifyClient: {Max: 20, Window: time.Minute},
	BackupDownload:         {Max: 5, Window: 10 * time.Minute},
	LogExport:              {Max: 5, Window: 10 * time.Minute},
	LogClear:               {Max: 3, Window: 10 * time.Minute},
	SystemLogExport:        {Max: 5, Window: 10 * time.Minute},
	SystemLogClear:         {Max: 3, Window: 10 * time.Minute},
	LogArchiveDownload:     {Max: 20, Window: time.Minute},
	LogArchiveDelete:       {Max: 10, Window: time.Minute},
	DatabaseOperation:      {Max: 10, Window: time.Minute},
	SettingsConfigSave:     {Max: 10, Window: time.Minute},
	SettingsConfigBackup:   {Max: 5, Window: 10 * time.Minute},
	SettingsConfigDownload: {Max: 5, Window: 10 * time.Minute},
	SettingsConfigRestore:  {Max: 5, Window: 10 * time.Minute},
	SettingsConfigDelete:   {Max: 5, Window: 10 * time.Minute},
	SettingsConfigRestart:  {Max: 3, Window: time.Minute},
	NotifyTest:             {Max: 20, Window: time.Minute},
	EntryChange:            {Max: 5, Window: time.Minute},
	DeploymentsClear:       {Max: 3, Window: 10 * time.Minute},
	ACMERequest:            {Max: 1200, Window: time.Minute},
	ACMEAccount:            {Max: 30, Window: time.Hour},
	ACMEPKI:                {Max: 600, Window: time.Minute},
	ACMEOrder:              {Max: 60, Window: time.Hour},
}

// DefaultConfig 返回内置默认限流配置。
func DefaultConfig() Config {
	return Config{MaxKeys: DefaultMaxKeys, Rules: DefaultRules()}
}

// DefaultRules 返回内置默认规则的副本。
func DefaultRules() map[string]Rule {
	out := make(map[string]Rule, len(defaultRules))
	for name, rule := range defaultRules {
		out[name] = rule
	}
	return out
}

// For 返回指定名称的规则；未配置或名称为空时回退到内置默认值。
func (c Config) For(name string) Rule {
	if rule, ok := c.Rules[name]; ok {
		return rule
	}
	return defaultRules[name]
}

// Known 判断规则名称是否存在。
func Known(name string) bool {
	_, ok := defaultRules[name]
	return ok
}

// Names 返回按字母排序的全部规则名称。
func Names() []string {
	names := make([]string, 0, len(defaultRules))
	for name := range defaultRules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
