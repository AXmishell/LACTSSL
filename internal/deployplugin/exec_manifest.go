package deployplugin

import "time"

// FeatureMergedDeployers 告知 helper 只输出合并后的规范部署目标。
// 旧版主程序不会传入该特性，helper 会继续输出旧 ID，保证前后端版本错配时可用。
const FeatureMergedDeployers = "merged-deployers-v1"

// Manifest 是外部 exec helper 通过 `manifest` 子命令输出的协议描述。
// 一个 helper 可以提供多个部署目标，避免为每个目标单独分发二进制。
type Manifest struct {
	// Protocol 目前固定为 v1。
	Protocol string `json:"protocol"`
	// Name 是 helper 的可读名称，仅用于日志与展示。
	Name string `json:"name,omitempty"`
	// Version 是 helper 版本号，仅用于展示。
	Version string `json:"version,omitempty"`
	// SHA256 是 helper 可执行文件的 sha256；非空时主程序会校验。
	SHA256 string `json:"sha256,omitempty"`
	// Deployers 是该 helper 支持的部署目标列表。
	Deployers []ManifestDeployer `json:"deployers"`
}

// ManifestDeployer 描述一个由 helper 提供的部署目标。
type ManifestDeployer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	// Fields 是目标凭据中的认证与连接字段。
	Fields []ManifestField `json:"fields,omitempty"`
	// DeployFields 是任务流程部署节点中的参数。
	DeployFields []ManifestField `json:"deploy_fields,omitempty"`
}

// ManifestField 描述外部部署目标的配置字段，与内置 FieldDef 的 JSON 结构保持兼容。
type ManifestField struct {
	Key         string             `json:"key"`
	Label       string             `json:"label"`
	Type        string             `json:"type"`
	Required    bool               `json:"required,omitempty"`
	Secret      bool               `json:"secret,omitempty"`
	Placeholder string             `json:"placeholder,omitempty"`
	Help        string             `json:"help,omitempty"`
	Default     string             `json:"default,omitempty"`
	Options     []ManifestOption   `json:"options,omitempty"`
	ShowIf      *ManifestCondition `json:"show_if,omitempty"`
}

// ManifestOption 是下拉选项。
type ManifestOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ManifestCondition 描述字段显示条件。
type ManifestCondition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DiscoverConfig 是外部部署插件扫描配置。
type DiscoverConfig struct {
	// Dir 是插件目录。
	Dir string
	// MemoryLimitMB 是单个 helper 进程的常驻内存上限，<=0 表示不限制。
	MemoryLimitMB int
	// Timeout 是单次 deploy 调用的超时时间。
	Timeout time.Duration
	// ManifestTimeout 是 manifest 探测超时时间，默认 10 秒。
	ManifestTimeout time.Duration
	// MaxOutputKB 是 helper 输出日志上限，默认 64 KB。
	MaxOutputKB int
	// KnownIDs 是主程序内置的部署类型 ID，用于避免插件覆盖内置实现。
	KnownIDs map[string]bool
	// EnabledDeployers 是管理员已启用的部署目标 ID 白名单。
	// 未列出的插件目标仍会被探测并保留元数据，但不会注册到运行时注册表。
	EnabledDeployers []string
}

func (c DiscoverConfig) normalized() DiscoverConfig {
	if c.Timeout <= 0 {
		c.Timeout = 300 * time.Second
	}
	if c.ManifestTimeout <= 0 {
		c.ManifestTimeout = 10 * time.Second
	}
	if c.MaxOutputKB <= 0 {
		c.MaxOutputKB = 64
	}
	return c
}

func (c DiscoverConfig) isReserved(deployer ManifestDeployer) bool {
	if len(c.KnownIDs) == 0 {
		return false
	}
	if c.KnownIDs[normalize(deployer.ID)] {
		return true
	}
	for _, alias := range deployer.Aliases {
		if c.KnownIDs[normalize(alias)] {
			return true
		}
	}
	return false
}

// DiscoverResult 是部署插件扫描结果。
type DiscoverResult struct {
	Dir string `json:"dir"`
	// Deployers 是成功加载的部署目标数量。
	Deployers int `json:"deployers"`
	// Helpers 是成功加载的 helper 数量。
	Helpers int      `json:"helpers"`
	Errors  []string `json:"errors,omitempty"`
	// Skipped 记录被内置实现覆盖而忽略的部署目标。
	Skipped []string `json:"skipped,omitempty"`
	// Disabled 记录已探测但被管理员停用、未注册到运行时的部署目标。
	Disabled []string `json:"disabled,omitempty"`
}
