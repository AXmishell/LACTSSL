// 本文件提供官方通知 helper 内部的渠道注册表。
package notifyplugin

import (
	"sort"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// NativeField 描述通知渠道的配置字段，与协议结构保持一致。
type NativeField = protocol.NotifyField

// NativeOption 是下拉选项。
type NativeOption = protocol.NotifyOption

// NativeDefinition 描述一个通知渠道及其配置字段。
type NativeDefinition struct {
	ID          string        `json:"id"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Aliases     []string      `json:"aliases,omitempty"`
	Fields      []NativeField `json:"fields,omitempty"`
}

// NativeMessage 是一次通知内容。Title 与 Body 均为纯文本。
type NativeMessage struct {
	Title string
	Body  string
}

// NativeNotifier 是通知渠道的统一接口。
type NativeNotifier interface {
	// Definition 返回渠道元数据与配置字段。
	Definition() NativeDefinition
	// Notify 发送一条通知。
	Notify(cfg map[string]any, msg NativeMessage) error
}

var nativeRegistry = struct {
	sync.RWMutex
	byID  map[string]NativeNotifier
	alias map[string]string
}{
	byID:  make(map[string]NativeNotifier),
	alias: make(map[string]string),
}

// RegisterNative 注册内置通知渠道。重复的 ID 或别名会直接 panic。
func RegisterNative(n NativeNotifier) {
	if err := registerNative(n); err != nil {
		panic(err)
	}
}

func registerNative(n NativeNotifier) error {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	def := n.Definition()
	id := normalize(def.ID)
	if id == "" {
		return errInvalidID
	}
	if _, exists := nativeRegistry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := nativeRegistry.alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range def.Aliases {
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
	nativeRegistry.byID[id] = n
	for _, alias := range def.Aliases {
		alias = normalize(alias)
		if alias != "" && alias != id {
			nativeRegistry.alias[alias] = id
		}
	}
	return nil
}

// LookupNative 按 ID 或别名查找通知渠道。
func LookupNative(name string) (NativeNotifier, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	if n, ok := nativeRegistry.byID[key]; ok {
		return n, true
	}
	if id, ok := nativeRegistry.alias[key]; ok {
		n, found := nativeRegistry.byID[id]
		return n, found
	}
	return nil, false
}

// HasNative 判断通知渠道是否已注册（含别名）。
func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

// ListNative 返回按 ID 排序的全部通知渠道。
func ListNative() []NativeNotifier {
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	out := make([]NativeNotifier, 0, len(nativeRegistry.byID))
	for _, n := range nativeRegistry.byID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		return normalize(out[i].Definition().ID) < normalize(out[j].Definition().ID)
	})
	return out
}

// SendNative 按渠道 ID 发送一条通知。
func SendNative(channel string, cfg map[string]any, msg NativeMessage) error {
	n, ok := LookupNative(channel)
	if !ok {
		return i18n.E("service.notify.channel_unsupported", "channel", channel)
	}
	return n.Notify(cfg, msg)
}

// ResetNativeForTest 清空注册表，仅供测试使用。
func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeNotifier)
	nativeRegistry.alias = make(map[string]string)
}
