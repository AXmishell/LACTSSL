// Package notifyapi 定义官方通知渠道的 Native 接口与注册表。
package notifyapi

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// ErrInvalidID 表示通知渠道 ID 为空或非法。
var ErrInvalidID = errors.New("notify channel id is empty or invalid")

// DuplicateError 表示通知渠道 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "notify channel already registered: " + e.ID }

type NativeField = protocol.NotifyField
type NativeOption = protocol.NotifyOption

// NativeDefinition 描述一个通知渠道及其配置字段。
type NativeDefinition struct {
	ID          string        `json:"id"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Aliases     []string      `json:"aliases,omitempty"`
	Fields      []NativeField `json:"fields,omitempty"`
}

// NativeMessage 是一次通知内容。
type NativeMessage struct {
	Title string
	Body  string
}

// NativeNotifier 是通知渠道的统一接口。
type NativeNotifier interface {
	Definition() NativeDefinition
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
		return ErrInvalidID
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

func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

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

func SendNative(channel string, cfg map[string]any, msg NativeMessage) error {
	n, ok := LookupNative(channel)
	if !ok {
		return i18n.E("service.notify.channel_unsupported", "channel", channel)
	}
	return n.Notify(cfg, msg)
}

func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeNotifier)
	nativeRegistry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
