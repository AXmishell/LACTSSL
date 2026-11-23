// Package dnsplugin 提供 DNS 服务商插件注册表。
//
// 官方实现随主程序编译，定义在 native_*.go 中；上传插件通过一次性
// exec helper 提供服务，由 Discover 在启动或手动重扫时注册。
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
	registry.Lock()
	defer registry.Unlock()
	return registerInto(registry.byID, registry.alias, p)
}

// registerInto 把插件写入给定的 id/alias 映射，调用方负责加锁。
// 先校验全部冲突再写入，避免失败时留下半条记录。
func registerInto(byID map[string]Plugin, alias map[string]string, p Plugin) error {
	id := normalize(p.ID())
	if id == "" {
		return errInvalidID
	}
	if _, exists := byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	// 新 ID 不能占用其他插件已经注册的别名，否则 Lookup 会优先命中
	// byID，导致旧插件的凭据被新 helper 接收。
	if _, exists := alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, item := range p.Aliases() {
		item = normalize(item)
		if item == "" {
			continue
		}
		if _, exists := byID[item]; exists {
			return &DuplicateError{ID: item}
		}
		if _, exists := alias[item]; exists {
			return &DuplicateError{ID: item}
		}
	}
	byID[id] = p
	for _, item := range p.Aliases() {
		item = normalize(item)
		if item != "" && item != id {
			alias[item] = id
		}
	}
	return nil
}

// ReplaceBuiltin 原子替换全部内置插件。
//
// 它先在一个新的映射中完整注册新集合，全部成功后才交换到注册表；
// 任一条目冲突或 ID 非法时返回错误并保留原有内置插件，避免并发重扫
// 期间出现「注销完成、注册失败」的半套状态。外部插件条目不受影响。
func ReplaceBuiltin(plugins []Plugin) error {
	registry.Lock()
	defer registry.Unlock()
	return replaceSourceLocked(SourceBuiltin, plugins)
}

func replaceSourceLocked(source Source, plugins []Plugin) error {
	byID := make(map[string]Plugin, len(registry.byID)+len(plugins))
	for id, p := range registry.byID {
		if p.Source() != source {
			byID[id] = p
		}
	}
	// 别名表按保留下来的插件重建，删除已消失来源的旧别名。
	alias := make(map[string]string, len(byID))
	for id, p := range byID {
		for _, item := range p.Aliases() {
			if item = normalize(item); item != "" && item != id {
				alias[item] = id
			}
		}
	}
	for _, p := range plugins {
		if p.Source() != source {
			return registryError("plugin source does not match replacement target")
		}
		if err := registerInto(byID, alias, p); err != nil {
			return err
		}
	}
	registry.byID = byID
	registry.alias = alias
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
