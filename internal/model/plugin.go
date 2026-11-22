package model

import "time"

// PluginInstallation 记录由插件仓库安装的插件文件，供升级、卸载与审计使用。
type PluginInstallation struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Version     string `json:"version"`
	Filename    string `json:"filename"`
	SHA256      string `json:"sha256"`
	AssetSHA256 string `json:"asset_sha256"`
	Source      string `json:"source"`
	RepoURL     string `json:"repo_url"`
	// IndexRevision / IndexDigest / EntryDigest 记录安装时的签名索引身份。
	// 旧记录没有这些字段时为空/0，不影响升级与卸载。
	IndexRevision uint64    `json:"index_revision,omitempty"`
	IndexDigest   string    `json:"index_digest,omitempty"`
	EntryDigest   string    `json:"entry_digest,omitempty"`
	InstalledAt   time.Time `json:"installed_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PluginFileEntry 是 helper 文件声明的单个条目（服务商、部署目标或通知渠道）。
// 名称在文件停用、无法探测 manifest 时仍需要展示，因此随文件元数据一起缓存。
type PluginFileEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// PluginFileMeta 缓存 helper 文件的 manifest 摘要。
// 文件被管理员停用后不会执行 `manifest` 子命令，插件页与白名单清理都依赖这份缓存。
type PluginFileMeta struct {
	Kind      string            `json:"kind"`
	Filename  string            `json:"filename"`
	Name      string            `json:"name,omitempty"`
	Version   string            `json:"version,omitempty"`
	Entries   []PluginFileEntry `json:"entries"`
	SHA256    string            `json:"sha256,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// PluginMetadata 缓存单个插件条目的完整 manifest。
// 用于 helper 文件停用或插件卸载后，历史账号仍能展示字段并按密钥字段脱敏。
// Manifest 是该条目的规范化 JSON，只用于展示与校验，不参与插件执行。
type PluginMetadata struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Version   string    `json:"version,omitempty"`
	Name      string    `json:"name,omitempty"`
	Manifest  string    `json:"manifest"`
	UpdatedAt time.Time `json:"updated_at"`
}
