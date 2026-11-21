// Package service 实现证书申请、部署、监控与调度等核心业务。
package service

import (
	"strings"

	"lactssl/internal/i18n"
	"lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/meta"
	"lactssl/internal/plugin/proto"
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
	// OptionsSource 非空表示选项由插件动态提供，前端通过专用接口读取。
	OptionsSource string `json:"options_source,omitempty"`
	// Multiple 表示该字段允许选择多个值。
	Multiple bool `json:"multiple,omitempty"`
	// 以下文案表来自 helper manifest，优先于主程序语言包。
	labelI18n       map[string]string
	placeholderI18n map[string]string
	helpI18n        map[string]string
}

// IsSecret 判断字段是否保存密钥，密码输入框默认视为密钥。
func (f FieldDef) IsSecret() bool {
	return f.Secret || f.Type == "password"
}

// Condition 描述字段的显示条件：Value 表示单值匹配，
// Values 非空时表示匹配其中任一值。
type Condition struct {
	Key    string   `json:"key"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

// VisibleIn 判断字段在当前配置下是否应显示。
// 条件字段缺省时使用同组字段的默认值，与服务端表单默认行为保持一致。
func (f FieldDef) VisibleIn(fields []FieldDef, cfg map[string]string) bool {
	if f.ShowIf == nil {
		return true
	}
	value := strings.TrimSpace(cfg[f.ShowIf.Key])
	if value == "" {
		for _, field := range fields {
			if field.Key == f.ShowIf.Key {
				value = strings.TrimSpace(field.Default)
				break
			}
		}
	}
	if len(f.ShowIf.Values) > 0 {
		for _, candidate := range f.ShowIf.Values {
			if value == candidate {
				return true
			}
		}
		return false
	}
	return value == f.ShowIf.Value
}

// Option 是下拉选项。
type Option struct {
	Label    string `json:"label"`
	LabelKey string `json:"-"`
	Value    string `json:"value"`
	// labelI18n 来自插件仓库元数据，优先于主程序语言包。
	labelI18n map[string]string
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
	// Capabilities 是插件 manifest 声明的能力位（例如 plan-v1）。
	// 内置目标没有插件能力，字段为空。
	Capabilities []string `json:"capabilities,omitempty"`
	// Available 表示当前构建或已安装插件是否包含该服务商的实现。
	// 目录中始终保留全部内置服务商，便于历史账号展示与切换构建后继续使用。
	Available bool `json:"available"`
	// Source 是服务商来源：builtin 或 exec。
	Source string `json:"source,omitempty"`
	// Deprecated 表示该定义仅用于兼容历史数据，合并目标可用时不再展示。
	Deprecated bool `json:"deprecated,omitempty"`
	// 以下文案表来自 helper manifest，优先于主程序语言包。
	labelI18n map[string]string
	descI18n  map[string]string
}

// CADef 描述 ACME 证书颁发机构。
type CADef struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	LabelKey string `json:"-"`
	URL      string `json:"url"`
	// RSAURL 是 RSA 私钥使用的独立 ACME 目录；为空时复用 URL。
	// 部分 CA（如 SSL.com）为 ECDSA 与 RSA 提供不同端点。
	RSAURL string `json:"-"`
	// Protocol 是 ACME 协议版本：v2（RFC 8555）或 v1（旧版 ACME）。
	Protocol string `json:"protocol"`
	// Custom 表示该 CA 由用户在「公共CA」中配置，而不是内置 CA。
	Custom  bool   `json:"custom,omitempty"`
	EAB     bool   `json:"eab"`
	Desc    string `json:"desc"`
	DescKey string `json:"-"`
}

// localizedText 从插件元数据文案表中选取当前语言。
// 插件侧文案优先于主程序语言包，避免新增部署目标后主程序漏同步。
func localizedText(locale i18n.Locale, values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	if text := values[string(locale)]; text != "" {
		return text
	}
	if text := values[string(i18n.Default)]; text != "" {
		return text
	}
	base := strings.ToLower(strings.SplitN(string(locale), "-", 2)[0])
	if base != "" {
		if text := values[base]; text != "" {
			return text
		}
		bestKey, bestText := "", ""
		for key, text := range values {
			if text == "" || !strings.HasPrefix(strings.ToLower(key), base+"-") {
				continue
			}
			if bestKey == "" || key < bestKey {
				bestKey, bestText = key, text
			}
		}
		if bestText != "" {
			return bestText
		}
	}
	return ""
}

// Localized 返回翻译后的选项。
func (o Option) Localized(locale i18n.Locale) Option {
	if text := localizedText(locale, o.labelI18n); text != "" {
		o.Label = text
	} else if o.LabelKey != "" {
		o.Label = i18n.T(locale, o.LabelKey, nil)
	}
	return o
}

// Localized 返回翻译后的字段。
func (f FieldDef) Localized(locale i18n.Locale) FieldDef {
	if text := localizedText(locale, f.labelI18n); text != "" {
		f.Label = text
	} else if f.LabelKey != "" {
		f.Label = i18n.T(locale, f.LabelKey, nil)
	}
	if text := localizedText(locale, f.placeholderI18n); text != "" {
		f.Placeholder = text
	} else if f.PlaceholderKey != "" {
		f.Placeholder = i18n.T(locale, f.PlaceholderKey, nil)
	}
	if text := localizedText(locale, f.helpI18n); text != "" {
		f.Help = text
	} else if f.HelpKey != "" {
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
	if text := localizedText(locale, p.labelI18n); text != "" {
		p.Label = text
	} else if p.LabelKey != "" {
		p.Label = i18n.T(locale, p.LabelKey, nil)
	}
	if text := localizedText(locale, p.descI18n); text != "" {
		p.Desc = text
	} else if p.DescKey != "" {
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
// 列表只包含当前已安装且已启用的插件服务商；元数据直接来自 helper manifest。
func LocalizeDNSProviders(locale i18n.Locale) []ProviderDef {
	externals := dnsplugin.ExternalEnabledProviders()
	out := make([]ProviderDef, 0, len(externals))
	for _, external := range externals {
		out = append(out, externalProviderDef(external).Localized(locale))
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
		labelI18n: external.NameI18n,
		descI18n:  external.DescriptionI18n,
	}
	for _, field := range external.Fields {
		item := FieldDef{
			Key:             field.Key,
			Label:           field.Label,
			Type:            field.Type,
			Required:        field.Required,
			Secret:          field.Secret,
			Placeholder:     field.Placeholder,
			Help:            field.Help,
			Default:         field.Default,
			labelI18n:       field.LabelI18n,
			placeholderI18n: field.PlaceholderI18n,
			helpI18n:        field.HelpI18n,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{
				Label:     option.Label,
				Value:     option.Value,
				labelI18n: option.LabelI18n,
			})
		}
		if field.ShowIf != nil {
			item.ShowIf = &Condition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out.Fields = append(out.Fields, item)
	}
	return out
}

// manifestProviderDef 把缓存中的 DNS manifest 条目转换为展示定义。
// 缓存条目来自已卸载或已停用的插件，因此 Available 固定为 false。
func manifestProviderDef(provider pluginproto.DNSProvider) ProviderDef {
	return externalProviderDef(dnsplugin.ExternalProvider{
		ID:              provider.ID,
		Name:            provider.Name,
		NameI18n:        provider.NameI18n,
		Description:     provider.Description,
		DescriptionI18n: provider.DescriptionI18n,
		Aliases:         provider.Aliases,
		Fields:          provider.Fields,
		Enabled:         false,
		FileEnabled:     false,
	})
}

// LocalizeDeployTypes 返回指定语言的部署类型列表。
//
// 列表只包含当前可用的目标：主程序内置的本地文件与 SSH，以及已加载且已启用的
// 部署插件目标。旧版合并目标在规范目标可用时隐藏。
func LocalizeDeployTypes(locale i18n.Locale) []ProviderDef {
	externals := deployplugin.ExternalDeployers()
	out := make([]ProviderDef, 0, len(DeployTypes)+len(externals))
	known := make(map[string]bool, len(DeployTypes))
	for _, provider := range DeployTypes {
		item := provider.Localized(locale)
		item.Available = true
		item.Source = string(deployplugin.SourceBuiltin)
		out = append(out, item)
		known[item.Value] = true
	}
	for _, external := range externals {
		if !external.Enabled {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(external.ID))
		if id == "" || known[id] {
			continue
		}
		// 旧 helper 可能把已合并的旧 ID 当作未知目标上报；规范目标支持
		// 对应部署对象时，仍应隐藏这些兼容条目。
		if _, ok := DeprecatedDeployTypeCanonical[id]; ok && deprecatedDeployTypeReplaced(id) {
			continue
		}
		item := externalDeployerDef(external).Localized(locale)
		item.Available = true
		item.Source = string(deployplugin.SourceExec)
		out = append(out, item)
		known[id] = true
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
		Capabilities: append([]string(nil), external.Capabilities...),
		labelI18n:    external.NameI18n,
		descI18n:     external.DescriptionI18n,
	}
}

// manifestDeployerDef 把缓存中的部署 manifest 条目转换为展示定义。
func manifestDeployerDef(deployer pluginproto.Deployer) ProviderDef {
	return externalDeployerDef(deployplugin.ExternalDeployer{
		ID:              deployer.ID,
		Name:            deployer.Name,
		NameI18n:        deployer.NameI18n,
		Description:     deployer.Description,
		DescriptionI18n: deployer.DescriptionI18n,
		Aliases:         deployer.Aliases,
		Fields:          deployer.Fields,
		DeployFields:    deployer.DeployFields,
		Capabilities:    deployer.Capabilities,
		Enabled:         false,
		FileEnabled:     false,
	})
}

func externalManifestFields(fields []deployplugin.ManifestField) []FieldDef {
	out := make([]FieldDef, 0, len(fields))
	for _, field := range fields {
		item := FieldDef{
			Key:             field.Key,
			Label:           field.Label,
			Type:            field.Type,
			Required:        field.Required,
			Secret:          field.Secret,
			Placeholder:     field.Placeholder,
			Help:            field.Help,
			Default:         field.Default,
			OptionsSource:   field.OptionsSource,
			Multiple:        field.Multiple,
			labelI18n:       field.LabelI18n,
			placeholderI18n: field.PlaceholderI18n,
			helpI18n:        field.HelpI18n,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{Label: option.Label, Value: option.Value, labelI18n: option.LabelI18n})
		}
		if field.ShowIf != nil {
			item.ShowIf = &Condition{Key: field.ShowIf.Key, Value: field.ShowIf.Value, Values: field.ShowIf.Values}
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
	{Value: "litessl", LabelKey: "meta.ca.litessl.label", DescKey: "meta.ca.litessl.desc", URL: "https://acme.litessl.com/acme/v2/directory", EAB: true},
	{Value: "zotrus", LabelKey: "meta.ca.zotrus.label", DescKey: "meta.ca.zotrus.desc", URL: "https://my.zotrus.com/cnacme", EAB: true},
	{Value: "actalis", LabelKey: "meta.ca.actalis.label", DescKey: "meta.ca.actalis.desc", URL: "https://acme-api.actalis.com/acme/directory", EAB: true},
	{Value: "sslcom", LabelKey: "meta.ca.sslcom.label", DescKey: "meta.ca.sslcom.desc", URL: "https://acme.ssl.com/sslcom-dv-ecc", RSAURL: "https://acme.ssl.com/sslcom-dv-rsa", EAB: true},
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
			{Key: "trust_new_host_key", LabelKey: "meta.field.trust_new_host_key.label", HelpKey: "meta.field.trust_new_host_key.help", Type: "switch"},
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

// DNSProviderInfo 查找 DNS 服务商定义（已加载插件或 manifest 缓存）。
func DNSProviderInfo(value string) (ProviderDef, bool) {
	if external, ok := dnsplugin.ExternalProviderInfo(value); ok {
		return externalProviderDef(external), true
	}
	if provider, ok := pluginmeta.Provider(value); ok {
		return manifestProviderDef(provider), true
	}
	return ProviderDef{}, false
}

// DeployTypeInfo 查找部署类型定义（内置、已加载插件或 manifest 缓存）。
// 已迁移到部署插件的目标即使 helper 未安装，也能从缓存返回元数据，
// 便于历史凭据脱敏、参数迁移以及启用插件后继续使用。
func DeployTypeInfo(value string) (ProviderDef, bool) {
	value = strings.TrimSpace(value)
	for _, p := range DeployTypes {
		if p.Value == value {
			return p, true
		}
	}
	lookup := value
	if canonical, ok := DeprecatedDeployTypeCanonical[strings.ToLower(value)]; ok {
		lookup = canonical
	}
	if external, ok := deployplugin.ExternalDeployerInfo(lookup); ok {
		def := externalDeployerDef(external)
		def.Available = external.Enabled
		return def, true
	}
	if deployer, ok := pluginmeta.Deployer(lookup); ok {
		return manifestDeployerDef(deployer), true
	}
	if lookup != value {
		if deployer, ok := pluginmeta.Deployer(value); ok {
			return manifestDeployerDef(deployer), true
		}
	}
	return ProviderDef{}, false
}
