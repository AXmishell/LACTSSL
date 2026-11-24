// Package dnsplugin 提供 DNS 服务商插件注册表。
//
// 官方实现随主程序编译，定义在 native_registry.go 与 providers/dns 中；
// 上传插件通过一次性 exec helper 提供服务，由 Discover 在启动或手动重扫时注册。
package dnsplugin

import (
	"strings"

	"github.com/go-acme/lego/v4/challenge"

	"lactssl/internal/plugin/runtime/registry"
)

// Source 标识插件来源。
type Source = registry.Source

const (
	// SourceBuiltin 表示编译进主程序的内置插件。
	SourceBuiltin = registry.Builtin
	// SourceExec 表示外部 exec helper 插件。
	SourceExec = registry.Exec
)

// Plugin 是 DNS 服务商插件的统一接口。
type Plugin interface {
	// ID 是插件的规范标识，必须与数据库中的 provider 值一致。
	ID() string
	// Aliases 是兼容别名，例如 acme-dns 与 acmedns。
	Aliases() []string
	// Source 返回插件来源。
	Source() Source
	// New 构造 lego challenge.Provider。cfg 仅外部插件使用，
	// 内置插件通过环境变量传递配置。
	New(cfg map[string]string) (challenge.Provider, error)
}

var plugins = registry.New(registry.Adapter[Plugin]{
	ID:      func(p Plugin) string { return p.ID() },
	Aliases: func(p Plugin) []string { return p.Aliases() },
	Source:  func(p Plugin) Source { return p.Source() },
})

// Register 注册内置插件。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立刻发现插件冲突，而不是在运行期静默覆盖。
func Register(p Plugin) {
	if err := plugins.Register(p); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部插件，返回注册冲突等错误。
func RegisterExternal(p Plugin) error {
	return plugins.Register(p)
}

// ReplaceBuiltin 原子替换全部内置插件。
//
// 它先完整注册新集合，全部成功后才交换到注册表；任一条目冲突或
// ID 非法时返回错误并保留原有内置插件，避免并发重扫期间出现
// 「注销完成、注册失败」的半套状态。外部插件条目不受影响。
func ReplaceBuiltin(items []Plugin) error {
	return plugins.ReplaceBuiltin(items)
}

// Lookup 按 ID 或别名查找插件。
func Lookup(name string) (Plugin, bool) {
	return plugins.Lookup(name)
}

// Has 判断插件是否已注册（含别名）。
func Has(name string) bool {
	return plugins.Has(name)
}

// List 返回按 ID 排序的全部插件。
func List() []Plugin {
	return plugins.List()
}

// ListBySource 返回指定来源的插件。
func ListBySource(source Source) []Plugin {
	return plugins.ListBySource(source)
}

// UnregisterSource 移除指定来源的全部插件，用于外部插件重扫。
func UnregisterSource(source Source) {
	plugins.UnregisterSource(source)
}

// resetForTest 清空注册表，仅供测试使用。
func resetForTest() {
	plugins.Reset()
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
