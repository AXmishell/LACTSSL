package webui

// SourceKind 标识前端资源的来源。
type SourceKind string

const (
	// SourceEmbedded 表示前端资源已通过 embed 编译进二进制。
	SourceEmbedded SourceKind = "embedded"
	// SourceLocal 表示前端资源来自本地构建目录，仅用于未启用 embed 标签的开发构建。
	SourceLocal SourceKind = "local"
	// SourceMissing 表示本地构建产物不存在，需要先构建前端。
	SourceMissing SourceKind = "missing"
)

// AssetSource 描述当前进程实际使用的前端资源。
// 无论哪种来源，资源都由 Go 服务直接读取并返回，不经过 Vite 或其它反向代理。
type AssetSource struct {
	Kind SourceKind
	// Path 是 Kind 为 SourceLocal 时的本地目录路径。
	Path string
}
