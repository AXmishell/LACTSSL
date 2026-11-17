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
	// 以下文案表来自插件仓库 catalog 或 helper manifest，优先于主程序语言包。
	labelI18n       map[string]string
	placeholderI18n map[string]string
	helpI18n        map[string]string
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
	// Available 表示当前构建或已安装插件是否包含该服务商的实现。
	// 目录中始终保留全部内置服务商，便于历史账号展示与切换构建后继续使用。
	Available bool `json:"available"`
	// Source 是服务商来源：builtin 或 exec。
	Source string `json:"source,omitempty"`
	// Deprecated 表示该定义仅用于兼容历史数据，合并目标可用时不再展示。
	Deprecated bool `json:"deprecated,omitempty"`
	// 以下文案表来自插件仓库 catalog 或 helper manifest，优先于主程序语言包。
	labelI18n map[string]string
	descI18n  map[string]string
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
		// 旧 helper 可能把已合并的旧 ID 当作未知目标上报；规范目标支持
		// 对应部署对象时，仍应隐藏这些兼容条目。
		if _, ok := DeprecatedDeployTypeCanonical[external.ID]; ok && deprecatedDeployTypeReplaced(external.ID) {
			continue
		}
		out = append(out, externalDeployerDef(external).Localized(locale))
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
		labelI18n:    external.NameI18n,
		descI18n:     external.DescriptionI18n,
	}
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
			labelI18n:       field.LabelI18n,
			placeholderI18n: field.PlaceholderI18n,
			helpI18n:        field.HelpI18n,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{Label: option.Label, Value: option.Value, labelI18n: option.LabelI18n})
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
