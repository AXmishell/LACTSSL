package dnsplugin

import (
	"sort"
	"strings"
	"sync"
)

// builtinState 保存编译进主程序的 DNS 服务商元数据。
//
// 内置实现可能因为类别或条目未启用而没有注册到运行期注册表，
// 但插件页、历史账号脱敏与切换开关仍需要完整条目清单。
var builtinState = struct {
	sync.RWMutex
	providers map[string]ExternalProvider
}{
	providers: make(map[string]ExternalProvider),
}

// SetBuiltinProviders 替换内置服务商元数据清单。
// enabled 由调用方根据类别/条目开关计算；未启用的条目仍会保留在清单中。
func SetBuiltinProviders(providers []ExternalProvider) {
	builtinState.Lock()
	defer builtinState.Unlock()
	builtinState.providers = make(map[string]ExternalProvider, len(providers))
	for _, provider := range providers {
		id := normalize(provider.ID)
		if id == "" {
			continue
		}
		provider.ID = id
		provider.Source = string(SourceBuiltin)
		provider.FileEnabled = true
		builtinState.providers[id] = provider
	}
}

// BuiltinProviders 返回按 ID 排序的内置服务商元数据。
func BuiltinProviders() []ExternalProvider {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make([]ExternalProvider, 0, len(builtinState.providers))
	for _, provider := range builtinState.providers {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuiltinProviderInfo 查找内置服务商元数据，支持别名。
func BuiltinProviderInfo(id string) (ExternalProvider, bool) {
	key := normalize(id)
	builtinState.RLock()
	defer builtinState.RUnlock()
	if provider, ok := builtinState.providers[key]; ok {
		return provider, true
	}
	for _, provider := range builtinState.providers {
		for _, alias := range provider.Aliases {
			if normalize(alias) == key {
				return provider, true
			}
		}
	}
	return ExternalProvider{}, false
}

// BuiltinProviderIDs 返回全部内置服务商 ID（不含别名），用于保留命名空间。
func BuiltinProviderIDs() map[string]bool {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make(map[string]bool, len(builtinState.providers))
	for id, item := range builtinState.providers {
		out[id] = true
		for _, alias := range item.Aliases {
			if alias = normalize(alias); alias != "" {
				out[alias] = true
			}
		}
	}
	return out
}

// UnregisterBuiltin 移除运行期内置实现，用于类别/条目开关重新注册。
func UnregisterBuiltin() {
	UnregisterSource(SourceBuiltin)
}

// normalizeBuiltinAliases 去重并规范化别名，供适配器构造元数据。
func normalizeBuiltinAliases(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
