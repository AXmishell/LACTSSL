// Package dnsplugin 提供 DNS 服务商插件注册表。
//
// 内置服务商由 internal/dnsplugin/providers 下的包装包在 init 中注册；
// 默认主程序不编译任何服务商，全部通过外部 helper 插件提供；使用 dns_extra
// 或 dns_all 构建标签时才会把服务商集合编译进来。
// 外部插件通过一次性 exec helper 提供服务，由 Discover 在启动或手动重扫时注册。
package dnsplugin

import (
	"sort"
	"strings"
	"sync"

	"github.com/go-acme/lego/v4/challenge"
)

// Source 标识插件来源。
type Source string

const (
	// SourceBuiltin 表示编译进主程序的内置插件。
	SourceBuiltin Source = "builtin"
	// SourceExec 表示外部 exec helper 插件。
	SourceExec Source = "exec"
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

var registry = struct {
	sync.RWMutex
	byID  map[string]Plugin
	alias map[string]string
}{
	byID:  make(map[string]Plugin),
	alias: make(map[string]string),
}

// Register 注册内置插件。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立刻发现插件冲突，而不是在运行期静默覆盖。
func Register(p Plugin) {
	if err := register(p); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部插件，返回注册冲突等错误。
func RegisterExternal(p Plugin) error {
	return register(p)
}

func register(p Plugin) error {
	id := normalize(p.ID())
	if id == "" {
		return errInvalidID
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias == "" {
			continue
		}
		if _, exists := registry.byID[alias]; exists {
			return &DuplicateError{ID: alias}
		}
		if _, exists := registry.alias[alias]; exists {
			return &DuplicateError{ID: alias}
		}
	}
	registry.byID[id] = p
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias != "" && alias != id {
			registry.alias[alias] = id
		}
	}
	return nil
}

// Lookup 按 ID 或别名查找插件。
func Lookup(name string) (Plugin, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	registry.RLock()
	defer registry.RUnlock()
	if p, ok := registry.byID[key]; ok {
		return p, true
	}
	if id, ok := registry.alias[key]; ok {
		p, found := registry.byID[id]
		return p, found
	}
	return nil, false
}

// Has 判断插件是否已注册（含别名）。
func Has(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// List 返回按 ID 排序的全部插件。
func List() []Plugin {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]Plugin, 0, len(registry.byID))
	for _, p := range registry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ListBySource 返回指定来源的插件。
func ListBySource(source Source) []Plugin {
	all := List()
	out := all[:0]
	for _, p := range all {
		if p.Source() == source {
			out = append(out, p)
		}
	}
	return out
}

// UnregisterSource 移除指定来源的全部插件，用于外部插件重扫。
func UnregisterSource(source Source) {
	registry.Lock()
	defer registry.Unlock()
	for id, p := range registry.byID {
		if p.Source() != source {
			continue
		}
		delete(registry.byID, id)
		for _, alias := range p.Aliases() {
			alias = normalize(alias)
			if current, ok := registry.alias[alias]; ok && current == id {
				delete(registry.alias, alias)
			}
		}
	}
}

// resetForTest 清空注册表，仅供测试使用。
func resetForTest() {
	registry.Lock()
	defer registry.Unlock()
	registry.byID = make(map[string]Plugin)
	registry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// DuplicateError 表示插件 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "dns plugin already registered: " + e.ID }

type registryError string

func (e registryError) Error() string { return string(e) }

const errInvalidID registryError = "dns plugin id is empty or invalid"
