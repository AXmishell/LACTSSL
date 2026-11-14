// Package service 实现证书申请、部署、监控与调度等核心业务。
package service

import (
	"strings"

	"lactssl/internal/deployplugin"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
)

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
	// Env 是字段对应的 lego 环境变量名，仅在服务端构造 DNS Provider 时使用。
	Env string `json:"-"`
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
	// Available 表示当前构建或已安装插件是否包含该服务商的实现。
	// 目录中始终保留全部内置服务商，便于历史账号展示与切换构建后继续使用。
	Available bool `json:"available"`
	// Source 是服务商来源：builtin 或 exec。
	Source string `json:"source,omitempty"`
	// Deprecated 表示该定义仅用于兼容历史数据，合并目标可用时不再展示。
	Deprecated bool `json:"deprecated,omitempty"`
}

// CADef 描述 ACME 证书颁发机构。
type CADef struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	LabelKey string `json:"-"`
	URL      string `json:"url"`
	// Protocol 是 ACME 协议版本：v2（RFC 8555）或 v1（旧版 ACME）。
	Protocol string `json:"protocol"`
	// Custom 表示该 CA 由用户在「权威CA」中配置，而不是内置 CA。
	Custom  bool   `json:"custom,omitempty"`
	EAB     bool   `json:"eab"`
	Desc    string `json:"desc"`
	DescKey string `json:"-"`
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
	if c.Protocol == "" {
		c.Protocol = "v2"
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
// 列表只包含当前可用的服务商：已编译的内置实现，以及已启用的外部插件；
// 未编译且无插件、或已被停用的插件服务商不会出现在列表中。
// 元数据仍保留在 DNSProviders 中，供 helper 与历史账号展示使用。
func LocalizeDNSProviders(locale i18n.Locale) []ProviderDef {
	out := make([]ProviderDef, 0, len(DNSProviders)+len(dnsplugin.ExternalProviders()))
	for _, provider := range DNSProviders {
		item := provider.Localized(locale)
		if plugin, ok := dnsplugin.Lookup(item.Value); ok && plugin.Source() == dnsplugin.SourceBuiltin {
			item.Available = true
			item.Source = string(dnsplugin.SourceBuiltin)
			out = append(out, item)
			continue
		}
		external, ok := dnsplugin.ExternalProviderInfo(item.Value)
		if !ok || !external.Enabled {
			// 没有编译进主程序，且没有启用中的插件提供该服务商：不展示。
			continue
		}
		item.Source = string(dnsplugin.SourceExec)
		item.Available = true
		out = append(out, item)
	}
	known := make(map[string]bool, len(DNSProviders))
	for _, provider := range DNSProviders {
		known[provider.Value] = true
	}
	for _, external := range dnsplugin.ExternalEnabledProviders() {
		// 内置目录已包含该 ID 时直接复用其字段定义，避免重复条目。
		if known[external.ID] {
			continue
		}
		out = append(out, externalProviderDef(external))
	}
	return out
}

// ExternalDNSProviders 返回已启用外部插件的服务商定义。
func ExternalDNSProviders() []ProviderDef {
	externals := dnsplugin.ExternalEnabledProviders()
	out := make([]ProviderDef, 0, len(externals))
	for _, external := range externals {
		out = append(out, externalProviderDef(external))
	}
	return out
}

// externalProviderDef 把外部插件元数据转换为前端使用的服务商定义。
func externalProviderDef(external dnsplugin.ExternalProvider) ProviderDef {
	label := external.Name
	if label == "" {
		label = external.ID
	}
	out := ProviderDef{
		Value:     external.ID,
		Label:     label,
		Desc:      external.Description,
		Available: external.Enabled,
		Source:    string(dnsplugin.SourceExec),
		Fields:    make([]FieldDef, 0, len(external.Fields)),
	}
	for _, field := range external.Fields {
		item := FieldDef{
			Key:         field.Key,
			Label:       field.Label,
			Type:        field.Type,
			Required:    field.Required,
			Secret:      field.Secret,
			Placeholder: field.Placeholder,
			Help:        field.Help,
			Default:     field.Default,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{Label: option.Label, Value: option.Value})
		}
		if field.ShowIf != nil {
			item.ShowIf = &Condition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out.Fields = append(out.Fields, item)
	}
	return out
}

// LocalizeDeployTypes 返回指定语言的部署类型列表。
//
// 列表只包含当前可用的目标：主程序内置的本地文件与 SSH，以及已加载的
// 部署插件目标。FTP、Webhook、CDN、面板等目标的元数据保留在
// PluginDeployTypes 中，但只有安装了对应 helper 后才会出现在列表里。
func LocalizeDeployTypes(locale i18n.Locale) []ProviderDef {
	out := make([]ProviderDef, 0, len(DeployTypes)+len(PluginDeployTypes)+len(deployplugin.ExternalDeployers()))
	known := make(map[string]bool, len(DeployTypes)+len(PluginDeployTypes))
	for _, provider := range DeployTypes {
		item := provider.Localized(locale)
		item.Available = true
		item.Source = string(deployplugin.SourceBuiltin)
		out = append(out, item)
		known[item.Value] = true
	}
	for _, provider := range PluginDeployTypes {
		// 合并后的目标可用时隐藏仅用于兼容历史数据的旧条目；
		// helper 未升级时仍展示旧条目，避免七牛云、宝塔和 1Panel 不可用。
		if provider.Deprecated && deprecatedDeployTypeReplaced(provider.Value) {
			continue
		}
		if !pluginDeployTypeAvailable(provider.Value) {
			continue
		}
		item := provider.Localized(locale)
		item.Available = true
		item.Source = string(deployplugin.SourceExec)
		out = append(out, item)
		known[item.Value] = true
	}
	for _, external := range deployplugin.ExternalDeployers() {
		// 内置目标与已知插件目标的元数据优先，保持字段翻译与历史数据一致。
		if known[external.ID] || !external.Enabled {
			continue
		}
		out = append(out, externalDeployerDef(external))
	}
	return out
}

// externalDeployerDef 把外部部署插件元数据转换为前端使用的部署类型定义。
func externalDeployerDef(external deployplugin.ExternalDeployer) ProviderDef {
	label := external.Name
	if label == "" {
		label = external.ID
	}
	return ProviderDef{
		Value:        external.ID,
		Label:        label,
		Desc:         external.Description,
		Available:    external.Enabled,
		Source:       string(deployplugin.SourceExec),
		Fields:       externalManifestFields(external.Fields),
		DeployFields: externalManifestFields(external.DeployFields),
	}
}

func externalManifestFields(fields []deployplugin.ManifestField) []FieldDef {
	out := make([]FieldDef, 0, len(fields))
	for _, field := range fields {
		item := FieldDef{
			Key:         field.Key,
			Label:       field.Label,
			Type:        field.Type,
			Required:    field.Required,
			Secret:      field.Secret,
			Placeholder: field.Placeholder,
			Help:        field.Help,
			Default:     field.Default,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{Label: option.Label, Value: option.Value})
		}
		if field.ShowIf != nil {
			item.ShowIf = &Condition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out = append(out, item)
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

// DNSProviders 是 DNS 服务商元数据目录，字段映射到 lego 所需的环境变量。
// 字段的 Env 保存对应的 lego 环境变量名；留空表示由特殊逻辑处理。
//
// 目录中手工维护的条目曾直接编译进主程序；现在改为由 DNS 插件（helper）
// 提供，并在插件白名单中默认启用（见 BuiltinDNSProviderIDs）。
var DNSProviders = []ProviderDef{
	{
		Value: "alidns", LabelKey: "meta.dns.alidns.label", DescKey: "meta.dns.alidns.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true, Env: "ALICLOUD_ACCESS_KEY"},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true, Env: "ALICLOUD_SECRET_KEY"},
			{Key: "region_id", LabelKey: "meta.field.region_id.label", PlaceholderKey: "meta.field.region_id.placeholder", Type: "text", Default: "cn-hangzhou", Env: "ALICLOUD_REGION_ID"},
		},
	},
	{
		Value: "aliesa", LabelKey: "meta.dns.aliesa.label", DescKey: "meta.dns.aliesa.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true, Env: "ALIESA_ACCESS_KEY"},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true, Env: "ALIESA_SECRET_KEY"},
			{Key: "region_id", LabelKey: "meta.field.region_id.label", PlaceholderKey: "meta.field.region_id.placeholder", Type: "text", Default: "cn-hangzhou", Env: "ALIESA_REGION_ID"},
		},
	},
	{
		Value: "cloudflare", LabelKey: "meta.dns.cloudflare.label", DescKey: "meta.dns.cloudflare.desc",
		Fields: []FieldDef{
			{Key: "api_token", LabelKey: "meta.field.api_token.label", HelpKey: "meta.field.api_token.help", Type: "password", Env: "CLOUDFLARE_DNS_API_TOKEN"},
			{Key: "email", LabelKey: "meta.field.email.label", HelpKey: "meta.field.email.help", Type: "text", Env: "CLOUDFLARE_EMAIL"},
			{Key: "api_key", LabelKey: "meta.field.api_key.label", HelpKey: "meta.field.api_key.help", Type: "password", Env: "CLOUDFLARE_API_KEY"},
			{Key: "base_url", LabelKey: "meta.field.base_url.label", HelpKey: "meta.field.base_url.help", Type: "text", PlaceholderKey: "meta.field.base_url.placeholder", Env: "CLOUDFLARE_BASE_URL"},
		},
	},
	{
		Value: "dnspod", LabelKey: "meta.dns.dnspod.label", DescKey: "meta.dns.dnspod.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.dnspod", PlaceholderKey: "meta.field.api_key.placeholder.dnspod", Type: "password", Required: true, Env: "DNSPOD_API_KEY"},
		},
	},
	{
		Value: "tencentcloud", LabelKey: "meta.dns.tencentcloud.label", DescKey: "meta.dns.tencentcloud.desc",
		Fields: []FieldDef{
			{Key: "secret_id", LabelKey: "meta.field.secret_id.label", Type: "text", Required: true, Env: "TENCENTCLOUD_SECRET_ID"},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true, Env: "TENCENTCLOUD_SECRET_KEY"},
			{Key: "region", LabelKey: "meta.field.region.label", PlaceholderKey: "meta.field.region.placeholder", Type: "text", Default: "ap-guangzhou", Env: "TENCENTCLOUD_REGION"},
		},
	},
	{
		Value: "huaweicloud", LabelKey: "meta.dns.huaweicloud.label", DescKey: "meta.dns.huaweicloud.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true, Env: "HUAWEICLOUD_ACCESS_KEY_ID"},
			{Key: "secret_access_key", LabelKey: "meta.field.secret_access_key.label", Type: "password", Required: true, Env: "HUAWEICLOUD_SECRET_ACCESS_KEY"},
			{Key: "region", LabelKey: "meta.field.region.label", PlaceholderKey: "meta.field.region.placeholder.huaweicloud", Type: "text", Required: true, Env: "HUAWEICLOUD_REGION"},
		},
	},
	{
		Value: "volcengine", LabelKey: "meta.dns.volcengine.label", DescKey: "meta.dns.volcengine.desc",
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true, Env: "VOLC_ACCESSKEY"},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true, Env: "VOLC_SECRETKEY"},
			{Key: "region", LabelKey: "meta.field.region.label", PlaceholderKey: "meta.field.region.placeholder.volcengine", Type: "text", Env: "VOLC_REGION"},
			{Key: "api_host", LabelKey: "meta.field.api_host.label", HelpKey: "meta.field.api_host.help", Type: "text", PlaceholderKey: "meta.field.api_host.placeholder", Env: "VOLC_HOST"},
			{Key: "api_scheme", LabelKey: "meta.field.api_scheme.label", Type: "select", Default: "https", Options: []Option{{Label: "https", Value: "https"}, {Label: "http", Value: "http"}}, Env: "VOLC_SCHEME"},
		},
	},
	{
		Value: "baiducloud", LabelKey: "meta.dns.baiducloud.label", DescKey: "meta.dns.baiducloud.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true, Env: "BAIDUCLOUD_ACCESS_KEY_ID"},
			{Key: "secret_access_key", LabelKey: "meta.field.secret_access_key.label", Type: "password", Required: true, Env: "BAIDUCLOUD_SECRET_ACCESS_KEY"},
		},
	},
	{
		Value: "jdcloud", LabelKey: "meta.dns.jdcloud.label", DescKey: "meta.dns.jdcloud.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", PlaceholderKey: "meta.field.access_key_id.placeholder", Type: "text", Required: true, Env: "JDCLOUD_ACCESS_KEY_ID"},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true, Env: "JDCLOUD_ACCESS_KEY_SECRET"},
			{Key: "region_id", LabelKey: "meta.field.region_id.label", PlaceholderKey: "meta.field.region_id.placeholder.jdcloud", Type: "text", Default: "cn-north-1", Env: "JDCLOUD_REGION_ID"},
		},
	},
	{
		Value: "digitalocean", LabelKey: "meta.dns.digitalocean.label", DescKey: "meta.dns.digitalocean.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true, Env: "DO_AUTH_TOKEN"}},
	},
	{
		Value: "godaddy", LabelKey: "meta.dns.godaddy.label", DescKey: "meta.dns.godaddy.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "text", Required: true, Env: "GODADDY_API_KEY"},
			{Key: "api_secret", LabelKey: "meta.field.api_secret.label", Type: "password", Required: true, Env: "GODADDY_API_SECRET"},
		},
	},
	{
		Value: "namecheap", LabelKey: "meta.dns.namecheap.label", DescKey: "meta.dns.namecheap.desc",
		Fields: []FieldDef{
			{Key: "api_user", LabelKey: "meta.field.api_user.label", Type: "text", Required: true, Env: "NAMECHEAP_API_USER"},
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "NAMECHEAP_API_KEY"},
			{Key: "source_ip", LabelKey: "meta.field.source_ip.label", HelpKey: "meta.field.source_ip.help", Type: "text", Env: "NAMECHEAP_SOURCE_IP"},
		},
	},
	{
		Value: "namesilo", LabelKey: "meta.dns.namesilo.label", DescKey: "meta.dns.namesilo.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "NAMESILO_API_KEY"}},
	},
	{
		Value: "porkbun", LabelKey: "meta.dns.porkbun.label", DescKey: "meta.dns.porkbun.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "text", Required: true, Env: "PORKBUN_API_KEY"},
			{Key: "secret_api_key", LabelKey: "meta.field.secret_api_key.label", Type: "password", Required: true, Env: "PORKBUN_SECRET_API_KEY"},
		},
	},
	{
		Value: "spaceship", LabelKey: "meta.dns.spaceship.label", DescKey: "meta.dns.spaceship.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "text", Required: true, Env: "SPACESHIP_API_KEY"},
			{Key: "api_secret", LabelKey: "meta.field.api_secret.label", Type: "password", Required: true, Env: "SPACESHIP_API_SECRET"},
		},
	},
	{
		Value: "cloudns", LabelKey: "meta.dns.cloudns.label", DescKey: "meta.dns.cloudns.desc",
		Fields: []FieldDef{
			{Key: "auth_id", LabelKey: "meta.field.auth_id.label", Type: "text", Required: true, Env: "CLOUDNS_AUTH_ID"},
			{Key: "sub_auth_id", LabelKey: "meta.field.sub_auth_id.label", HelpKey: "meta.field.sub_auth_id.help", Type: "text", Env: "CLOUDNS_SUB_AUTH_ID"},
			{Key: "auth_password", LabelKey: "meta.field.auth_password.label", Type: "password", Required: true, Env: "CLOUDNS_AUTH_PASSWORD"},
		},
	},
	{
		Value: "bunny", LabelKey: "meta.dns.bunny.label", DescKey: "meta.dns.bunny.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "BUNNY_API_KEY"}},
	},
	{
		Value: "rainyun", LabelKey: "meta.dns.rainyun.label", DescKey: "meta.dns.rainyun.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "RAINYUN_API_KEY"}},
	},
	{
		Value: "westcn", LabelKey: "meta.dns.westcn.label", DescKey: "meta.dns.westcn.desc",
		Fields: []FieldDef{
			{Key: "username", LabelKey: "meta.field.username.label", Type: "text", Required: true, Env: "WESTCN_USERNAME"},
			{Key: "password", LabelKey: "meta.field.password.label", Type: "password", Required: true, Env: "WESTCN_PASSWORD"},
		},
	},
	{
		Value: "pdns", LabelKey: "meta.dns.pdns.label", DescKey: "meta.dns.pdns.desc",
		Fields: []FieldDef{
			{Key: "api_url", LabelKey: "meta.field.api_url.label", HelpKey: "meta.field.api_url.help", PlaceholderKey: "meta.field.api_url.placeholder", Type: "text", Required: true, Env: "PDNS_API_URL"},
			{Key: "api_key", LabelKey: "meta.field.api_key.label.pdns", Type: "password", Required: true, Env: "PDNS_API_KEY"},
			{Key: "server_name", LabelKey: "meta.field.server_name.label", HelpKey: "meta.field.server_name.help", Type: "text", Default: "localhost", Env: "PDNS_SERVER_NAME"},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", HelpKey: "meta.field.api_version.help.pdns", Type: "number", Default: "0", Env: "PDNS_API_VERSION"},
		},
	},
	{
		Value: "dnsimple", LabelKey: "meta.dns.dnsimple.label", DescKey: "meta.dns.dnsimple.desc",
		Fields: []FieldDef{
			{Key: "oauth_token", LabelKey: "meta.field.oauth_token.label", Type: "password", Required: true, Env: "DNSIMPLE_OAUTH_TOKEN"},
			{Key: "base_url", LabelKey: "meta.field.base_url.label", HelpKey: "meta.field.base_url.help", Type: "text", PlaceholderKey: "meta.field.base_url.placeholder", Env: "DNSIMPLE_BASE_URL"},
		},
	},
	{
		Value: "dnsmadeeasy", LabelKey: "meta.dns.dnsmadeeasy.label", DescKey: "meta.dns.dnsmadeeasy.desc",
		Fields: []FieldDef{
			{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "text", Required: true, Env: "DNSMADEEASY_API_KEY"},
			{Key: "api_secret", LabelKey: "meta.field.api_secret.label", Type: "password", Required: true, Env: "DNSMADEEASY_API_SECRET"},
			{Key: "sandbox", LabelKey: "meta.field.sandbox.label", HelpKey: "meta.field.sandbox.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.boolean.false", Value: "false"}, {LabelKey: "meta.field.boolean.true", Value: "true"}}, Env: "DNSMADEEASY_SANDBOX"},
		},
	},
	{
		Value: "route53", LabelKey: "meta.dns.route53.label", DescKey: "meta.dns.route53.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", HelpKey: "meta.field.access_key_id.help.route53", Type: "text", Env: "AWS_ACCESS_KEY_ID"},
			{Key: "secret_access_key", LabelKey: "meta.field.secret_access_key.label", HelpKey: "meta.field.secret_access_key.help.route53", Type: "password", Env: "AWS_SECRET_ACCESS_KEY"},
			{Key: "region", LabelKey: "meta.field.region.label", PlaceholderKey: "meta.field.region.placeholder.aws", Type: "text", Env: "AWS_REGION"},
			{Key: "hosted_zone_id", LabelKey: "meta.field.hosted_zone_id.label", HelpKey: "meta.field.hosted_zone_id.help", Type: "text", Env: "AWS_HOSTED_ZONE_ID"},
			{Key: "private_zone", LabelKey: "meta.field.private_zone.label", HelpKey: "meta.field.private_zone.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.boolean.false", Value: "false"}, {LabelKey: "meta.field.boolean.true", Value: "true"}}, Env: "AWS_PRIVATE_ZONE"},
		},
	},
	{
		Value: "gcloud", LabelKey: "meta.dns.gcloud.label", DescKey: "meta.dns.gcloud.desc",
		Fields: []FieldDef{
			{Key: "service_account", LabelKey: "meta.field.service_account.label", HelpKey: "meta.field.service_account.help", Type: "textarea", Secret: true, Env: "GCE_SERVICE_ACCOUNT"},
			{Key: "project", LabelKey: "meta.field.project.label", HelpKey: "meta.field.project.help", Type: "text", Env: "GCE_PROJECT"},
			{Key: "allow_private_zone", LabelKey: "meta.field.allow_private_zone.label", HelpKey: "meta.field.allow_private_zone.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.boolean.false", Value: "false"}, {LabelKey: "meta.field.boolean.true", Value: "true"}}, Env: "GCE_ALLOW_PRIVATE_ZONE"},
		},
	},
	{
		Value: "azuredns", LabelKey: "meta.dns.azuredns.label", DescKey: "meta.dns.azuredns.desc",
		Fields: []FieldDef{
			{Key: "tenant_id", LabelKey: "meta.field.tenant_id.label", HelpKey: "meta.field.azure_credentials.help", Type: "text", Env: "AZURE_TENANT_ID"},
			{Key: "client_id", LabelKey: "meta.field.client_id.label", HelpKey: "meta.field.azure_credentials.help", Type: "text", Env: "AZURE_CLIENT_ID"},
			{Key: "client_secret", LabelKey: "meta.field.client_secret.label", HelpKey: "meta.field.azure_credentials.help", Type: "password", Env: "AZURE_CLIENT_SECRET"},
			{Key: "subscription_id", LabelKey: "meta.field.subscription_id.label", HelpKey: "meta.field.azure_credentials.help", Type: "text", Env: "AZURE_SUBSCRIPTION_ID"},
			{Key: "resource_group", LabelKey: "meta.field.resource_group.label", HelpKey: "meta.field.azure_credentials.help", Type: "text", Env: "AZURE_RESOURCE_GROUP"},
			{Key: "zone_name", LabelKey: "meta.field.zone_name.label", HelpKey: "meta.field.zone_name.help", Type: "text", Env: "AZURE_ZONE_NAME"},
			{Key: "azure_environment", LabelKey: "meta.field.azure_environment.label", Type: "select", Default: "public", Options: []Option{{Label: "public", Value: "public"}, {Label: "china", Value: "china"}, {Label: "usgovernment", Value: "usgovernment"}}, Env: "AZURE_ENVIRONMENT"},
			{Key: "private_zone", LabelKey: "meta.field.private_zone.label", HelpKey: "meta.field.private_zone.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.boolean.false", Value: "false"}, {LabelKey: "meta.field.boolean.true", Value: "true"}}, Env: "AZURE_PRIVATE_ZONE"},
		},
	},
	{
		Value: "netlify", LabelKey: "meta.dns.netlify.label", DescKey: "meta.dns.netlify.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true, Env: "NETLIFY_TOKEN"}},
	},
	{
		Value: "vercel", LabelKey: "meta.dns.vercel.label", DescKey: "meta.dns.vercel.desc",
		Fields: []FieldDef{
			{Key: "api_token", LabelKey: "meta.field.api_token.label", Type: "password", Required: true, Env: "VERCEL_API_TOKEN"},
			{Key: "team_id", LabelKey: "meta.field.team_id.label", HelpKey: "meta.field.team_id.help", Type: "text", Env: "VERCEL_TEAM_ID"},
		},
	},
	{
		Value: "duckdns", LabelKey: "meta.dns.duckdns.label", DescKey: "meta.dns.duckdns.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true, Env: "DUCKDNS_TOKEN"}},
	},
	{
		Value: "freemyip", LabelKey: "meta.dns.freemyip.label", DescKey: "meta.dns.freemyip.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true, Env: "FREEMYIP_TOKEN"}},
	},
	{
		Value: "vultr", LabelKey: "meta.dns.vultr.label", DescKey: "meta.dns.vultr.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "VULTR_API_KEY"}},
	},
	{
		Value: "linode", LabelKey: "meta.dns.linode.label", DescKey: "meta.dns.linode.desc",
		Fields: []FieldDef{{Key: "token", LabelKey: "meta.field.token.label", Type: "password", Required: true, Env: "LINODE_TOKEN"}},
	},
	{
		Value: "hetzner", LabelKey: "meta.dns.hetzner.label", DescKey: "meta.dns.hetzner.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "HETZNER_API_KEY"}},
	},
	{
		Value: "gandi", LabelKey: "meta.dns.gandi.label", DescKey: "meta.dns.gandi.desc",
		Fields: []FieldDef{{Key: "api_key", LabelKey: "meta.field.api_key.label.godaddy", Type: "password", Required: true, Env: "GANDI_API_KEY"}},
	},
	{
		Value: "gandiv5", LabelKey: "meta.dns.gandiv5.label", DescKey: "meta.dns.gandiv5.desc",
		Fields: []FieldDef{{Key: "personal_access_token", LabelKey: "meta.field.personal_access_token.label", Type: "password", Required: true, Env: "GANDIV5_PERSONAL_ACCESS_TOKEN"}},
	},
	{
		Value: "rfc2136", LabelKey: "meta.dns.rfc2136.label", DescKey: "meta.dns.rfc2136.desc",
		Fields: []FieldDef{
			{Key: "nameserver", LabelKey: "meta.field.nameserver.label", HelpKey: "meta.field.nameserver.help", PlaceholderKey: "meta.field.nameserver.placeholder", Type: "text", Required: true, Env: "DNSUPDATE_NAMESERVER"},
			{Key: "tsig_key", LabelKey: "meta.field.tsig_key.label", HelpKey: "meta.field.tsig_key.help", Type: "text", Env: "DNSUPDATE_TSIG_KEY"},
			{Key: "tsig_secret", LabelKey: "meta.field.tsig_secret.label", Type: "password", Env: "DNSUPDATE_TSIG_SECRET"},
			{Key: "tsig_algorithm", LabelKey: "meta.field.tsig_algorithm.label", HelpKey: "meta.field.tsig_algorithm.help", Type: "text", PlaceholderKey: "meta.field.tsig_algorithm.placeholder", Env: "DNSUPDATE_TSIG_ALGORITHM"},
			{Key: "zones", LabelKey: "meta.field.zones.label", HelpKey: "meta.field.zones.help", Type: "text", Env: "DNSUPDATE_ZONES"},
		},
	},
}

// builtinDNSProviderIDs 记录迁移前直接编译进主程序的 DNS 服务商 ID。
// 这些服务商现在由 DNS 插件提供，但仍默认启用，保证升级后行为不变。
var builtinDNSProviderIDs []string

// BuiltinDNSProviderIDs 返回迁移前内置的 DNS 服务商 ID（meta.go 手工维护的条目），
// 供插件启用白名单初始化默认值。
func BuiltinDNSProviderIDs() []string {
	return append([]string(nil), builtinDNSProviderIDs...)
}

func init() {
	builtinDNSProviderIDs = make([]string, 0, len(DNSProviders))
	known := make(map[string]struct{}, len(DNSProviders))
	for _, provider := range DNSProviders {
		builtinDNSProviderIDs = append(builtinDNSProviderIDs, provider.Value)
		known[provider.Value] = struct{}{}
	}
	for _, provider := range generatedDNSProviders {
		if _, ok := known[provider.Value]; ok {
			continue
		}
		known[provider.Value] = struct{}{}
		DNSProviders = append(DNSProviders, provider)
	}
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

// DNSProviderInfo 查找 DNS 服务商定义（内置目录或外部插件）。
func DNSProviderInfo(value string) (ProviderDef, bool) {
	for _, p := range DNSProviders {
		if p.Value == value {
			return p, true
		}
	}
	if external, ok := dnsplugin.ExternalProviderInfo(value); ok {
		return externalProviderDef(external), true
	}
	return ProviderDef{}, false
}

// DeployTypeInfo 查找部署类型定义（内置或插件目标）。
// 已迁移到部署插件的目标即使 helper 未安装也能返回元数据，
// 便于历史凭据脱敏、参数迁移以及启用插件后继续使用。
func DeployTypeInfo(value string) (ProviderDef, bool) {
	value = strings.TrimSpace(value)
	for _, p := range DeployTypes {
		if p.Value == value {
			return p, true
		}
	}
	if p, ok := PluginDeployTypeInfo(value); ok {
		p.Available = pluginDeployTypeAvailable(value)
		p.Source = string(deployplugin.SourceExec)
		return p, true
	}
	if external, ok := deployplugin.ExternalDeployerInfo(value); ok {
		return externalDeployerDef(external), true
	}
	return ProviderDef{}, false
}
