package protocol

// DNSManifest 是 DNS helper 通过 `manifest` 子命令输出的协议描述。
type DNSManifest struct {
	// Protocol 目前固定为 v1。
	Protocol string `json:"protocol"`
	// Capabilities 是 helper 级能力位，例如 verify-v1。
	Capabilities []string `json:"capabilities,omitempty"`
	// Name 是 helper 的可读名称，仅用于日志与展示。
	Name string `json:"name,omitempty"`
	// Version 是 helper 版本号，仅用于展示。
	Version string `json:"version,omitempty"`
	// SHA256 是 helper 可执行文件的 sha256；非空时主程序会校验。
	SHA256 string `json:"sha256,omitempty"`
	// Providers 是该 helper 支持的服务商列表。
	Providers []DNSProvider `json:"providers"`
}

// DNSProvider 描述一个由 helper 提供的 DNS 服务商。
// Name/Description 是默认语言，*I18n 是可选的多语言覆盖表；旧版宿主忽略。
type DNSProvider struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	NameI18n        map[string]string `json:"name_i18n,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionI18n map[string]string `json:"description_i18n,omitempty"`
	Aliases         []string          `json:"aliases,omitempty"`
	Fields          []DNSField        `json:"fields,omitempty"`
	// Capabilities 非空表示该服务商支持 verify 子命令中的只读探测。
	Capabilities []string `json:"capabilities,omitempty"`
}

// DNSField 描述外部服务商的配置字段，与内置 FieldDef 的 JSON 结构保持兼容。
type DNSField struct {
	Key             string            `json:"key"`
	Label           string            `json:"label"`
	LabelI18n       map[string]string `json:"label_i18n,omitempty"`
	Type            string            `json:"type"`
	Required        bool              `json:"required,omitempty"`
	Secret          bool              `json:"secret,omitempty"`
	Placeholder     string            `json:"placeholder,omitempty"`
	PlaceholderI18n map[string]string `json:"placeholder_i18n,omitempty"`
	Help            string            `json:"help,omitempty"`
	HelpI18n        map[string]string `json:"help_i18n,omitempty"`
	Default         string            `json:"default,omitempty"`
	Options         []DNSOption       `json:"options,omitempty"`
	ShowIf          *DNSCondition     `json:"show_if,omitempty"`
}

// DNSOption 是下拉选项。
type DNSOption struct {
	Label     string            `json:"label"`
	LabelI18n map[string]string `json:"label_i18n,omitempty"`
	Value     string            `json:"value"`
}

// DNSCondition 描述字段显示条件。
type DNSCondition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DNSRequest 是 present / cleanup / verify 的 stdin 请求。
type DNSRequest struct {
	Action   string            `json:"action"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Token    string            `json:"token,omitempty"`
	KeyAuth  string            `json:"key_auth,omitempty"`
	// Mode 仅用于 verify：read 表示只读探测。
	Mode string `json:"mode,omitempty"`
	// Locale 让 helper 返回对应语言的检查消息。
	Locale string `json:"locale,omitempty"`
}
