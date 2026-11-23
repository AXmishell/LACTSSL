// 本文件提供官方 DNS helper 内部的服务商注册表。
//
// 每个 provider 包装包在 init 中注册；helper 只加载全部内置实现，
// 不区分「内置 / 外部」，因为 helper 本身就是外部插件。
package dnsplugin

import (
	"sort"
	"sync"
)

var nativeRegistry = struct {
	sync.RWMutex
	byID  map[string]NativeProvider
	alias map[string]string
}{
	byID:  make(map[string]NativeProvider),
	alias: make(map[string]string),
}

// RegisterNative 注册内置服务商。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立刻发现冲突。
func RegisterNative(p NativeProvider) {
	if err := registerNative(p); err != nil {
		panic(err)
	}
}

func registerNative(p NativeProvider) error {
	id := normalize(p.ID())
	if id == "" {
		return errInvalidID
	}
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	if _, exists := nativeRegistry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias == "" {
			continue
		}
		if _, exists := nativeRegistry.byID[alias]; exists {
			return &DuplicateError{ID: alias}
		}
		if _, exists := nativeRegistry.alias[alias]; exists {
			return &DuplicateError{ID: alias}
		}
	}
	nativeRegistry.byID[id] = p
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias != "" && alias != id {
			nativeRegistry.alias[alias] = id
		}
	}
	return nil
}

// LookupNative 按 ID 或别名查找服务商。
func LookupNative(name string) (NativeProvider, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	if p, ok := nativeRegistry.byID[key]; ok {
		return p, true
	}
	if id, ok := nativeRegistry.alias[key]; ok {
		p, found := nativeRegistry.byID[id]
		return p, found
	}
	return nil, false
}

// HasNative 判断服务商是否已注册（含别名）。
func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

// ListNative 返回按 ID 排序的全部服务商。
func ListNative() []NativeProvider {
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	out := make([]NativeProvider, 0, len(nativeRegistry.byID))
	for _, p := range nativeRegistry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ResetNativeForTest 清空注册表，仅供测试使用。
func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeProvider)
	nativeRegistry.alias = make(map[string]string)
}
