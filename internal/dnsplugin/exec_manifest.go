package dnsplugin

import "time"

// Manifest 是外部 exec helper 通过 `manifest` 子命令输出的协议描述。
// 一个 helper 可以提供多个服务商，避免为每个服务商单独分发二进制。
type Manifest struct {
	// Protocol 目前固定为 v1。
	Protocol string `json:"protocol"`
	// Name 是 helper 的可读名称，仅用于日志与展示。
	Name string `json:"name,omitempty"`
	// Version 是 helper 版本号，仅用于展示。
	Version string `json:"version,omitempty"`
	// SHA256 是 helper 可执行文件的 sha256；非空时主程序会校验。
	SHA256 string `json:"sha256,omitempty"`
	// Providers 是该 helper 支持的服务商列表。
	Providers []ManifestProvider `json:"providers"`
}

// ManifestProvider 描述一个由 helper 提供的 DNS 服务商。
type ManifestProvider struct {
	ID          string          `json:"id"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Aliases     []string        `json:"aliases,omitempty"`
	Fields      []ManifestField `json:"fields,omitempty"`
}

// ManifestField 描述外部服务商的配置字段，与内置 FieldDef 的 JSON 结构保持兼容。
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

// DiscoverConfig 是外部插件扫描配置。
type DiscoverConfig struct {
	// Dir 是插件目录。
	Dir string
	// MemoryLimitMB 是单个 helper 进程的常驻内存上限，<=0 表示不限制。
	MemoryLimitMB int
	// Timeout 是单次 present/cleanup 调用的超时时间。
	Timeout time.Duration
	// ManifestTimeout 是 manifest 探测超时时间，默认 10 秒。
	ManifestTimeout time.Duration
	// MaxOutputKB 是 helper 输出日志上限，默认 64 KB。
	MaxOutputKB int
	// EnabledProviders 是管理员已启用的服务商 ID 白名单。
	// 未列出的插件服务商默认停用：仍会被探测并保留元数据，但不会注册到运行时注册表。
	EnabledProviders []string
}

func (c DiscoverConfig) normalized() DiscoverConfig {
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
	if c.ManifestTimeout <= 0 {
		c.ManifestTimeout = 10 * time.Second
	}
	if c.MaxOutputKB <= 0 {
		c.MaxOutputKB = 64
	}
	return c
}

// DiscoverResult 是插件扫描结果。
type DiscoverResult struct {
	Dir       string   `json:"dir"`
	Providers int      `json:"providers"`
	Helpers   int      `json:"helpers"`
	Errors    []string `json:"errors,omitempty"`
	// Skipped 记录被内置实现覆盖而忽略的服务商。
	Skipped []string `json:"skipped,omitempty"`
	// Disabled 记录已探测但被管理员停用、未注册到运行时的服务商。
	Disabled []string `json:"disabled,omitempty"`
}
