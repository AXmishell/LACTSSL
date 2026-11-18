package pluginproto

// DeployManifest 是部署 helper 通过 `manifest` 子命令输出的协议描述。
type DeployManifest struct {
	Protocol string `json:"protocol"`
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// Deployers 是该 helper 支持的部署目标列表。
	Deployers []Deployer `json:"deployers"`
}

// Deployer 描述一个由 helper 提供的部署目标。
// Name 与 Description 是默认语言，*I18n 是可选的多语言覆盖表；
// 旧版宿主忽略这些字段，新版宿主按当前语言优先取覆盖值。
type Deployer struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	NameI18n        map[string]string `json:"name_i18n,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionI18n map[string]string `json:"description_i18n,omitempty"`
	Aliases         []string          `json:"aliases,omitempty"`
	Fields          []DeployField     `json:"fields,omitempty"`
	// DeployFields 是任务流程部署节点中的参数。
	DeployFields []DeployField `json:"deploy_fields,omitempty"`
}

// DeployField 描述外部部署目标的配置字段。
type DeployField struct {
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
	Options         []DeployOption    `json:"options,omitempty"`
	ShowIf          *DeployCondition  `json:"show_if,omitempty"`
}

// DeployOption 是下拉选项。
type DeployOption struct {
	Label     string            `json:"label"`
	LabelI18n map[string]string `json:"label_i18n,omitempty"`
	Value     string            `json:"value"`
}

// DeployCondition 描述字段显示条件。
type DeployCondition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DeployRequest 是 deploy 的 stdin 请求。
type DeployRequest struct {
	Action      string            `json:"action"`
	Protocol    string            `json:"protocol,omitempty"`
	Provider    string            `json:"provider"`
	Auth        map[string]string `json:"auth,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Certificate Certificate       `json:"certificate"`
}

// Certificate 是交给插件的最小证书材料。
type Certificate struct {
	Domain       string   `json:"domain"`
	Domains      []string `json:"domains,omitempty"`
	Issuer       string   `json:"issuer,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	NotBefore    string   `json:"not_before,omitempty"`
	NotAfter     string   `json:"not_after,omitempty"`
	CertPEM      string   `json:"cert_pem"`
	FullchainPEM string   `json:"fullchain_pem"`
	KeyPEM       string   `json:"key_pem"`
}
