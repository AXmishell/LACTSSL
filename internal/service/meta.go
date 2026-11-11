// Package service 实现证书申请、部署、监控与调度等核心业务。
package service

import "lactssl/internal/i18n"

// FieldDef 描述前端动态表单字段。
type FieldDef struct {
	Key            string     `json:"key"`
	Label          string     `json:"label"`
	LabelKey       string     `json:"-"`
	Type           string     `json:"type"` // text / password / textarea / number / select / switch
	Required       bool       `json:"required"`
	Secret         bool       `json:"secret,omitempty"` // 密钥字段：接口返回时脱敏，编辑时留空表示保留原值
	Placeholder    string     `json:"placeholder"`
	PlaceholderKey string     `json:"-"`
	Help           string     `json:"help"`
	HelpKey        string     `json:"-"`
	Options        []Option   `json:"options,omitempty"`
	Default        string     `json:"default,omitempty"`
	ShowIf         *Condition `json:"show_if,omitempty"`
}

// IsSecret 判断字段是否保存密钥，密码输入框默认视为密钥。
func (f FieldDef) IsSecret() bool {
	return f.Secret || f.Type == "password"
}

// Condition 描述字段的显示条件：当 Key 对应的值等于 Value 时才显示。
type Condition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Option 是下拉选项。
type Option struct {
	Label    string `json:"label"`
	LabelKey string `json:"-"`
	Value    string `json:"value"`
}

// ProviderDef 描述 DNS 服务商或部署类型。
// Fields 保存认证与连接信息；DeployFields 保存任务流程部署节点中的部署参数。
type ProviderDef struct {
	Value        string     `json:"value"`
	Label        string     `json:"label"`
	LabelKey     string     `json:"-"`
	Desc         string     `json:"desc"`
	DescKey      string     `json:"-"`
	Fields       []FieldDef `json:"fields"`
	DeployFields []FieldDef `json:"deploy_fields,omitempty"`
}

// CADef 描述 ACME 证书颁发机构。
type CADef struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	LabelKey string `json:"-"`
	URL      string `json:"url"`
	EAB      bool   `json:"eab"`
	Desc     string `json:"desc"`
	DescKey  string `json:"-"`
}

// Localized 返回翻译后的选项。
func (o Option) Localized(locale i18n.Locale) Option {
	if o.LabelKey != "" {
		o.Label = i18n.T(locale, o.LabelKey, nil)
	}
	return o
}

// Localized 返回翻译后的字段。
func (f FieldDef) Localized(locale i18n.Locale) FieldDef {
	if f.LabelKey != "" {
		f.Label = i18n.T(locale, f.LabelKey, nil)
	}
	if f.PlaceholderKey != "" {
		f.Placeholder = i18n.T(locale, f.PlaceholderKey, nil)
	}
	if f.HelpKey != "" {
		f.Help = i18n.T(locale, f.HelpKey, nil)
	}
	options := make([]Option, len(f.Options))
	for index, option := range f.Options {
		options[index] = option.Localized(locale)
	}
	f.Options = options
	return f
}

// Localized 返回翻译后的服务商定义。
func (p ProviderDef) Localized(locale i18n.Locale) ProviderDef {
	if p.LabelKey != "" {
		p.Label = i18n.T(locale, p.LabelKey, nil)
	}
	if p.DescKey != "" {
		p.Desc = i18n.T(locale, p.DescKey, nil)
	}
	fields := make([]FieldDef, len(p.Fields))
	for index, field := range p.Fields {
		fields[index] = field.Localized(locale)
	}
	p.Fields = fields
	deployFields := make([]FieldDef, len(p.DeployFields))
	for index, field := range p.DeployFields {
		deployFields[index] = field.Localized(locale)
	}
	p.DeployFields = deployFields
	return p
}

// Localized 返回翻译后的 CA 定义。
func (c CADef) Localized(locale i18n.Locale) CADef {
	if c.LabelKey != "" {
		c.Label = i18n.T(locale, c.LabelKey, nil)
	}
	if c.DescKey != "" {
		c.Desc = i18n.T(locale, c.DescKey, nil)
	}
	return c
}

// LocalizeCADefs 返回指定语言的 CA 列表。
func LocalizeCADefs(locale i18n.Locale) []CADef {
	out := make([]CADef, len(CADefs))
	for index, ca := range CADefs {
		out[index] = ca.Localized(locale)
	}
	return out
}

// LocalizeDNSProviders 返回指定语言的 DNS 服务商列表。
func LocalizeDNSProviders(locale i18n.Locale) []ProviderDef {
	out := make([]ProviderDef, len(DNSProviders))
	for index, provider := range DNSProviders {
		out[index] = provider.Localized(locale)
	}
	return out
}

// LocalizeDeployTypes 返回指定语言的部署类型列表。
func LocalizeDeployTypes(locale i18n.Locale) []ProviderDef {
	out := make([]ProviderDef, len(DeployTypes))
	for index, provider := range DeployTypes {
		out[index] = provider.Localized(locale)
	}
	return out
}

// CADefs 是内置支持的证书颁发机构。
var CADefs = []CADef{
	{Value: "letsencrypt", LabelKey: "meta.ca.letsencrypt.label", DescKey: "meta.ca.letsencrypt.desc", URL: "https://acme-v02.api.letsencrypt.org/directory"},
	{Value: "zerossl", LabelKey: "meta.ca.zerossl.label", DescKey: "meta.ca.zerossl.desc", URL: "https://acme.zerossl.com/v2/DV90", EAB: true},
	{Value: "google", LabelKey: "meta.ca.google.label", DescKey: "meta.ca.google.desc", URL: "https://dv.acme-v02.api.pki.goog/directory", EAB: true},
	{Value: "buypass", LabelKey: "meta.ca.buypass.label", DescKey: "meta.ca.buypass.desc", URL: "https://api.buypass.com/acme/directory"},
	{Value: "litessl", LabelKey: "meta.ca.litessl.label", DescKey: "meta.ca.litessl.desc", URL: "https://acme.litessl.com/acme/v2/directory", EAB: true},
}

// DNSProviders 是内置 DNS 服务商，字段映射到 lego 所需的环境变量。
var DNSProviders = []ProviderDef{
	{
		Value: "alidns", LabelKey: "meta.dns.alidns.label", DescKey: "meta.dns.alidns.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true},
			{Key: "region_id", LabelKey: "meta.field.region_id.label", PlaceholderKey: "meta.field.region_id.placeholder", Type: "text", Default: "cn-hangzhou"},
		},
	},
	{
		Value: "cloudflare", LabelKey: "meta.dns.cloudflare.label", DescKey: "meta.dns.cloudflare.desc",
		Fields: []FieldDef{
			{Key: "api_token", LabelKey: "meta.field.api_token.label", HelpKey: "meta.field.api_token.help", Type: "password"},
			{Key: "email", LabelKey: "meta.field.email.label", HelpKey: "meta.field.email.help", Type: "text"},
			{Key: "api_key", LabelKey: "meta.field.api_key.label", HelpKey: "meta.field.api_key.help", Type: "password"},
		},
	},
	{
		Value: "dnspod", LabelKey: "meta.dns.dnspod.label", DescKey: "meta.dns.dnspod.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.dnspod", PlaceholderKey: "meta.field.api_key.placeholder.dnspod", Type: "password", Required: true},
		},
	},
	{
		Value: "tencentcloud", LabelKey: "meta.dns.tencentcloud.label", DescKey: "meta.dns.tencentcloud.desc",
		Fields: []FieldDef{
			{Key: "secret_id", LabelKey: "meta.field.secret_id.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
			{Key: "region", LabelKey: "meta.field.region.label", PlaceholderKey: "meta.field.region.placeholder", Type: "text", Default: "ap-guangzhou"},
		},
	},
	{
		Value: "digitalocean", LabelKey: "meta.dns.digitalocean.label", DescKey: "meta.dns.digitalocean.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true}},
	},
	{
		Value: "godaddy", LabelKey: "meta.dns.godaddy.label", DescKey: "meta.dns.godaddy.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.api_secret.label", Type: "password", Required: true},
		},
	},
	{
		Value: "namecheap", LabelKey: "meta.dns.namecheap.label", DescKey: "meta.dns.namecheap.desc",
		Fields: []FieldDef{
			{Key: "api_user", LabelKey: "meta.field.api_user.label", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true},
			{Key: "source_ip", LabelKey: "meta.field.source_ip.label", HelpKey: "meta.field.source_ip.help", Type: "text"},
		},
	},
	{
		Value: "vultr", LabelKey: "meta.dns.vultr.label", DescKey: "meta.dns.vultr.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true}},
	},
	{
		Value: "linode", LabelKey: "meta.dns.linode.label", DescKey: "meta.dns.linode.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true}},
	},
	{
		Value: "hetzner", LabelKey: "meta.dns.hetzner.label", DescKey: "meta.dns.hetzner.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true}},
	},
	{
		Value: "gandi", LabelKey: "meta.dns.gandi.label", DescKey: "meta.dns.gandi.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true}},
	},
}

// DeployTypes 是内置目标凭据。
// Fields 只包含认证与连接信息，部署路径、域名、站点等参数放在 DeployFields，
// 由任务流程中的部署节点填写。
var DeployTypes = []ProviderDef{
	{
		Value: "local", LabelKey: "meta.deploy.local.label", DescKey: "meta.deploy.local.desc",
		DeployFields: []FieldDef{
			{Key: "cert_path", LabelKey: "meta.field.cert_path.label", PlaceholderKey: "meta.field.cert_path.placeholder", Type: "text", Required: true},
			{Key: "key_path", LabelKey: "meta.field.key_path.label", PlaceholderKey: "meta.field.key_path.placeholder", Type: "text", Required: true},
			{Key: "reload_command", LabelKey: "meta.field.reload_command.label", PlaceholderKey: "meta.field.reload_command.placeholder", HelpKey: "meta.field.reload_command.help", Type: "text"},
		},
	},
	{
		Value: "ssh", LabelKey: "meta.deploy.ssh.label", DescKey: "meta.deploy.ssh.desc",
		Fields: []FieldDef{
			{Key: "host", LabelKey: "meta.field.host.label", PlaceholderKey: "meta.field.host.placeholder", Type: "text", Required: true},
			{Key: "port", LabelKey: "meta.field.port.label", Type: "number", Default: "22"},
			{Key: "user", LabelKey: "meta.field.user.label", PlaceholderKey: "meta.field.user.placeholder", Type: "text", Required: true},
			{Key: "auth_type", LabelKey: "meta.field.auth_type.label", Type: "select", Default: "password", Options: []Option{{LabelKey: "meta.field.auth_type.option.password", Value: "password"}, {LabelKey: "meta.field.auth_type.option.key", Value: "key"}}},
			{Key: "password", LabelKey: "meta.field.password.label", Type: "password", ShowIf: &Condition{Key: "auth_type", Value: "password"}},
			{Key: "private_key", LabelKey: "meta.field.private_key.label", HelpKey: "meta.field.private_key.help", Type: "textarea", Secret: true, ShowIf: &Condition{Key: "auth_type", Value: "key"}},
			{Key: "host_key_fingerprint", LabelKey: "meta.field.host_key_fingerprint.label", HelpKey: "meta.field.host_key_fingerprint.help", Type: "text"},
		},
		DeployFields: []FieldDef{
			{Key: "cert_path", LabelKey: "meta.field.remote_cert_path.label", PlaceholderKey: "meta.field.remote_cert_path.placeholder", Type: "text", Required: true},
			{Key: "key_path", LabelKey: "meta.field.remote_key_path.label", PlaceholderKey: "meta.field.remote_key_path.placeholder", Type: "text", Required: true},
			{Key: "reload_command", LabelKey: "meta.field.reload_command.label", PlaceholderKey: "meta.field.reload_command.placeholder", Type: "text"},
		},
	},
	{
		Value: "ftp", LabelKey: "meta.deploy.ftp.label", DescKey: "meta.deploy.ftp.desc",
		Fields: []FieldDef{
			{Key: "host", LabelKey: "meta.field.host.label", PlaceholderKey: "meta.field.host.placeholder", Type: "text", Required: true},
			{Key: "port", LabelKey: "meta.field.ftp_port.label", Type: "number", Default: "21"},
			{Key: "user", LabelKey: "meta.field.user.label", PlaceholderKey: "meta.field.user.placeholder", Type: "text", Required: true},
			{Key: "password", LabelKey: "meta.field.password.label", Type: "password", Required: true},
			{Key: "tls", LabelKey: "meta.field.ftp_tls.label", HelpKey: "meta.field.ftp_tls.help", Type: "select", Default: "explicit", Options: []Option{
				{LabelKey: "meta.field.ftp_tls.option.none", Value: "none"},
				{LabelKey: "meta.field.ftp_tls.option.explicit", Value: "explicit"},
				{LabelKey: "meta.field.ftp_tls.option.implicit", Value: "implicit"},
			}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", HelpKey: "meta.field.ftp_skip_verify.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "cert_path", LabelKey: "meta.field.remote_cert_path.label", PlaceholderKey: "meta.field.remote_cert_path.placeholder", Type: "text", Required: true},
			{Key: "key_path", LabelKey: "meta.field.remote_key_path.label", PlaceholderKey: "meta.field.remote_key_path.placeholder", Type: "text", Required: true},
		},
	},
	{
		Value: "webhook", LabelKey: "meta.deploy.webhook.label", DescKey: "meta.deploy.webhook.desc",
		Fields: []FieldDef{
			{Key: "url", LabelKey: "meta.field.url.label", PlaceholderKey: "meta.field.url.placeholder", Type: "text", Required: true},
			{Key: "auth_header", LabelKey: "meta.field.auth_header.label", HelpKey: "meta.field.auth_header.help", Type: "password"},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
	},
	{
		Value: "aliyun_cdn", LabelKey: "meta.deploy.aliyun_cdn.label", DescKey: "meta.deploy.aliyun_cdn.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", Type: "text", Required: true},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.domains.label", HelpKey: "meta.field.domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "qiniu_cdn", LabelKey: "meta.deploy.qiniu_cdn.label", DescKey: "meta.deploy.qiniu_cdn.desc",
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.domains.label", HelpKey: "meta.field.domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "qiniu_kodo", LabelKey: "meta.deploy.qiniu_kodo.label", DescKey: "meta.deploy.qiniu_kodo.desc",
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.kodo_domains.label", HelpKey: "meta.field.kodo_domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "baota_site", LabelKey: "meta.deploy.baota_site.label", DescKey: "meta.deploy.baota_site.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.panel_api_secret.label", HelpKey: "meta.field.panel_api_secret.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "sites", LabelKey: "meta.field.sites.label", HelpKey: "meta.field.sites.help", Type: "text"},
		},
	},
	{
		Value: "baota_panel", LabelKey: "meta.deploy.baota_panel.label", DescKey: "meta.deploy.baota_panel.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.panel_api_secret.label", HelpKey: "meta.field.panel_api_secret.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
	},
	{
		Value: "1panel_site", LabelKey: "meta.deploy.1panel_site.label", DescKey: "meta.deploy.1panel_site.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.panel_api_key.label", HelpKey: "meta.field.panel_api_key.help", Type: "password", Required: true},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", Type: "select", Default: "v2", Options: []Option{{Label: "v2", Value: "v2"}, {Label: "v1", Value: "v1"}}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "website", LabelKey: "meta.field.website.label", HelpKey: "meta.field.website.help", Type: "text"},
		},
	},
	{
		Value: "1panel_panel", LabelKey: "meta.deploy.1panel_panel.label", DescKey: "meta.deploy.1panel_panel.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.panel_api_key.label", HelpKey: "meta.field.panel_api_key.help", Type: "password", Required: true},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", Type: "select", Default: "v2", Options: []Option{{Label: "v2", Value: "v2"}, {Label: "v1", Value: "v1"}}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "ssl_mode", LabelKey: "meta.field.ssl_mode.label", HelpKey: "meta.field.ssl_mode.help", Type: "select", Default: "Enable", Options: []Option{{LabelKey: "meta.field.ssl_mode.option.enable", Value: "Enable"}, {LabelKey: "meta.field.ssl_mode.option.mux", Value: "Mux"}}},
		},
	},
	{
		Value: "safeline", LabelKey: "meta.deploy.safeline.label", DescKey: "meta.deploy.safeline.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_token", LabelKey: "meta.field.panel_api_token.label", HelpKey: "meta.field.panel_api_token.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "cert_ids", LabelKey: "meta.field.cert_ids.label", HelpKey: "meta.field.cert_ids.help", Type: "text"},
		},
	},
}

// CAInfo 根据标识查找 CA。
func CAInfo(value string) (CADef, bool) {
	for _, ca := range CADefs {
		if ca.Value == value {
			return ca, true
		}
	}
	return CADef{}, false
}

// CAEABKidSettingKey 返回指定 CA 的 EAB Key ID 设置键。
func CAEABKidSettingKey(value string) string {
	return "acme.ca." + value + ".eab_kid"
}

// CAEABHMACSettingKey 返回指定 CA 的 EAB HMAC Key 设置键。
func CAEABHMACSettingKey(value string) string {
	return "acme.ca." + value + ".eab_hmac"
}

// CAAccountKeySettingKey 返回指定 CA 的 ACME 账户密钥设置键。
func CAAccountKeySettingKey(value string) string {
	return "acme.account_key." + value
}

// DNSProviderInfo 查找 DNS 服务商定义。
func DNSProviderInfo(value string) (ProviderDef, bool) {
	for _, p := range DNSProviders {
		if p.Value == value {
			return p, true
		}
	}
	return ProviderDef{}, false
}

// DeployTypeInfo 查找部署类型定义。
func DeployTypeInfo(value string) (ProviderDef, bool) {
	for _, p := range DeployTypes {
		if p.Value == value {
			return p, true
		}
	}
	return ProviderDef{}, false
}
