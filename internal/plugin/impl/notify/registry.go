// Package notify 提供官方通知 helper 内部的渠道注册表。
package notify

import (
	"sort"
	"strings"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// Field 描述通知渠道的配置字段，与协议结构保持一致。
type Field = protocol.NotifyField

// Option 是下拉选项。
type Option = protocol.NotifyOption

// Definition 描述一个通知渠道及其配置字段。
type Definition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Fields      []Field  `json:"fields,omitempty"`
}

// Message 是一次通知内容。Title 与 Body 均为纯文本。
type Message struct {
	Title string
	Body  string
}

// Notifier 是通知渠道的统一接口。
type Notifier interface {
	// Definition 返回渠道元数据与配置字段。
	Definition() Definition
	// Notify 发送一条通知。
	Notify(cfg map[string]any, msg Message) error
}

var registry = struct {
	sync.RWMutex
	byID  map[string]Notifier
	alias map[string]string
}{
	byID:  make(map[string]Notifier),
	alias: make(map[string]string),
}

// Register 注册内置通知渠道。重复的 ID 或别名会直接 panic。
func Register(n Notifier) {
	if err := register(n); err != nil {
		panic(err)
	}
}

func register(n Notifier) error {
	registry.Lock()
	defer registry.Unlock()
	def := n.Definition()
	id := normalize(def.ID)
	if id == "" {
		return errInvalidID
	}
	if _, exists := registry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := registry.alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range def.Aliases {
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
	registry.byID[id] = n
	for _, alias := range def.Aliases {
		alias = normalize(alias)
		if alias != "" && alias != id {
			registry.alias[alias] = id
		}
	}
	return nil
}

// Lookup 按 ID 或别名查找通知渠道。
func Lookup(name string) (Notifier, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	registry.RLock()
	defer registry.RUnlock()
	if n, ok := registry.byID[key]; ok {
		return n, true
	}
	if id, ok := registry.alias[key]; ok {
		n, found := registry.byID[id]
		return n, found
	}
	return nil, false
}

// Has 判断通知渠道是否已注册（含别名）。
func Has(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// List 返回按 ID 排序的全部通知渠道。
func List() []Notifier {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]Notifier, 0, len(registry.byID))
	for _, n := range registry.byID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return normalize(out[i].Definition().ID) < normalize(out[j].Definition().ID) })
	return out
}

// Send 按渠道 ID 发送一条通知。
func Send(channel string, cfg map[string]any, msg Message) error {
	n, ok := Lookup(channel)
	if !ok {
		return i18n.E("service.notify.channel_unsupported", "channel", channel)
	}
	return n.Notify(cfg, msg)
}

// ResetForTest 清空注册表，仅供测试使用。
func ResetForTest() {
	registry.Lock()
	defer registry.Unlock()
	registry.byID = make(map[string]Notifier)
	registry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// DuplicateError 表示通知渠道 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "notify plugin already registered: " + e.ID }

type registryError string

func (e registryError) Error() string { return string(e) }

const errInvalidID registryError = "notify plugin id is empty or invalid"
