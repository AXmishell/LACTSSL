package dnsplugin

import (
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// Manifest 是外部 exec helper 通过 `manifest` 子命令输出的协议描述。
// 结构由 protocol 包统一维护，宿主与插件仓库共用同一份定义。
type Manifest = protocol.DNSManifest

// ManifestProvider 描述一个由 helper 提供的 DNS 服务商。
type ManifestProvider = protocol.DNSProvider

// ManifestField 描述外部服务商的配置字段，与内置 FieldDef 的 JSON 结构保持兼容。
type ManifestField = protocol.DNSField

// ManifestOption 是下拉选项。
type ManifestOption = protocol.DNSOption

// ManifestCondition 描述字段显示条件。
type ManifestCondition = protocol.DNSCondition

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
	// DisabledHelpers 是管理员已停用的 helper 文件名列表。
	// 列出的文件不会被探测或加载；其条目元数据由宿主缓存提供。
	DisabledHelpers []string
	// ExpectedDigests 是仓库安装记录中的文件名到可执行文件 SHA-256 的映射。
	// 命中记录时必须在执行 manifest 前校验，防止安装后的文件被替换。
	ExpectedDigests map[string]string
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
	// DisabledFiles 记录被管理员停用、未探测的 helper 文件名。
	DisabledFiles []string `json:"disabled_files,omitempty"`
}
