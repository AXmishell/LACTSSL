// Package dns 提供官方 DNS helper 内部的服务商注册表。
//
// 每个 provider 包装包在 init 中注册；helper 只加载全部内置实现，
// 不区分「内置 / 外部」，因为 helper 本身就是外部插件。
package dns

import (
	"sort"
	"strings"
	"sync"
)

var registry = struct {
	sync.RWMutex
	byID  map[string]Provider
	alias map[string]string
}{
	byID:  make(map[string]Provider),
	alias: make(map[string]string),
}

// Register 注册内置服务商。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立刻发现冲突。
func Register(p Provider) {
	if err := register(p); err != nil {
		panic(err)
	}
}

func register(p Provider) error {
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

// Lookup 按 ID 或别名查找服务商。
func Lookup(name string) (Provider, bool) {
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

// Has 判断服务商是否已注册（含别名）。
func Has(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// List 返回按 ID 排序的全部服务商。
func List() []Provider {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]Provider, 0, len(registry.byID))
	for _, p := range registry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ResetForTest 清空注册表，仅供测试使用。
func ResetForTest() {
	registry.Lock()
	defer registry.Unlock()
	registry.byID = make(map[string]Provider)
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
