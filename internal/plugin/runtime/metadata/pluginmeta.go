// Package pluginmeta 缓存已发现插件的 manifest 条目元数据。
//
// 缓存只用于展示、脱敏与字段校验，不参与插件执行。它解决两个场景：
//   - helper 文件被管理员停用后不再执行 manifest，但历史账号仍需要字段定义；
//   - 插件被卸载后，历史账号仍需要知道哪些字段是密钥。
//
// 运行期数据来自 API 层的发现/安装流程，持久化由 plugin_metadata 表负责；
// 本包只维护进程内快照，避免 service 层反向依赖 store 或 api。
package pluginmeta

import (
	"encoding/json"
	"strings"
	"sync"

	"lactssl/internal/plugin/proto"
)

// Entry 是单个插件条目的 manifest 快照。
type Entry struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Version string          `json:"version,omitempty"`
	Name    string          `json:"name,omitempty"`
	Raw     json.RawMessage `json:"manifest,omitempty"`
}

var state = struct {
	sync.RWMutex
	byKey map[string]Entry
}{
	byKey: make(map[string]Entry),
}

func cacheKey(kind, id string) string {
	return normalize(kind) + "\x00" + normalize(id)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// Reset 清空缓存，仅供测试或显式重载使用。
func Reset() {
	state.Lock()
	defer state.Unlock()
	state.byKey = make(map[string]Entry)
}

// ReplaceKind 用一批条目替换某个 kind 的全部缓存。
// 持久化 tombstone 由调用方在替换前重新注入。
func ReplaceKind(kind string, entries []Entry) {
	kind = normalize(kind)
	state.Lock()
	defer state.Unlock()
	for key, entry := range state.byKey {
		if entry.Kind == kind {
			delete(state.byKey, key)
		}
	}
	for _, entry := range entries {
		entry.Kind = kind
		entry.ID = normalize(entry.ID)
		if entry.ID == "" {
			continue
		}
		state.byKey[cacheKey(kind, entry.ID)] = entry
	}
}

// Put 把任意 manifest 条目编码后写入缓存。
func Put(kind, id, version, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	Upsert(Entry{Kind: kind, ID: id, Version: version, Name: name, Raw: raw})
	return nil
}

// Upsert 写入或覆盖单个缓存条目。
func Upsert(entry Entry) {
	kind := normalize(entry.Kind)
	id := normalize(entry.ID)
	if kind == "" || id == "" {
		return
	}
	entry.Kind = kind
	entry.ID = id
	state.Lock()
	defer state.Unlock()
	if state.byKey == nil {
		state.byKey = make(map[string]Entry)
	}
	state.byKey[cacheKey(kind, id)] = entry
}

// Get 返回指定条目的缓存。
func Get(kind, id string) (Entry, bool) {
	state.RLock()
	defer state.RUnlock()
	entry, ok := state.byKey[cacheKey(kind, id)]
	return entry, ok
}

// findAlias 在精确 ID 未命中时扫描条目别名。
func findAlias(kind, id string) (Entry, bool) {
	state.RLock()
	defer state.RUnlock()
	kind = normalize(kind)
	id = normalize(id)
	for _, entry := range state.byKey {
		if entry.Kind != kind || len(entry.Raw) == 0 {
			continue
		}
		var aliases struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal(entry.Raw, &aliases); err != nil {
			continue
		}
		for _, alias := range aliases.Aliases {
			if normalize(alias) == id {
				return entry, true
			}
		}
	}
	return Entry{}, false
}

func lookup(kind, id string) (Entry, bool) {
	if entry, ok := Get(kind, id); ok {
		return entry, true
	}
	return findAlias(kind, id)
}

// Delete 删除单个缓存条目。
func Delete(kind, id string) {
	state.Lock()
	defer state.Unlock()
	delete(state.byKey, cacheKey(kind, id))
}

// Provider 返回 DNS 服务商缓存。
func Provider(id string) (pluginproto.DNSProvider, bool) {
	entry, ok := lookup("dns", id)
	if !ok || len(entry.Raw) == 0 {
		return pluginproto.DNSProvider{}, false
	}
	var value pluginproto.DNSProvider
	if err := json.Unmarshal(entry.Raw, &value); err != nil {
		return pluginproto.DNSProvider{}, false
	}
	if value.ID == "" {
		value.ID = entry.ID
	}
	return value, true
}

// Deployer 返回部署目标缓存。
func Deployer(id string) (pluginproto.Deployer, bool) {
	entry, ok := lookup("deploy", id)
	if !ok || len(entry.Raw) == 0 {
		return pluginproto.Deployer{}, false
	}
	var value pluginproto.Deployer
	if err := json.Unmarshal(entry.Raw, &value); err != nil {
		return pluginproto.Deployer{}, false
	}
	if value.ID == "" {
		value.ID = entry.ID
	}
	return value, true
}

// Channel 返回通知渠道缓存。
func Channel(id string) (pluginproto.NotifyChannel, bool) {
	entry, ok := lookup("notify", id)
	if !ok || len(entry.Raw) == 0 {
		return pluginproto.NotifyChannel{}, false
	}
	var value pluginproto.NotifyChannel
	if err := json.Unmarshal(entry.Raw, &value); err != nil {
		return pluginproto.NotifyChannel{}, false
	}
	if value.ID == "" {
		value.ID = entry.ID
	}
	return value, true
}
