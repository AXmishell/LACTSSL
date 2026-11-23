// Package notifyplugin 提供通知渠道的插件注册表。
//
// 主程序只内置邮件通知；Gotify、ServerChan、Telegram、Webhook 等渠道由
// 通知插件通过一次性 exec helper 提供，由 Discover 在启动或手动重扫时注册。
package notifyplugin

import (
	"sort"
	"strings"
	"sync"

	"lactssl/internal/i18n"
)

// Source 标识通知插件来源。
type Source string

const (
	// SourceBuiltin 表示编译进 helper 的内置通知渠道。
	SourceBuiltin Source = "builtin"
	// SourceExec 表示外部 exec helper 插件。
	SourceExec Source = "exec"
)

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
	// Source 返回实现来源。
	Source() Source
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

// Register 注册内置通知渠道。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立即发现冲突。
func Register(n Notifier) {
	if err := register(n); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部通知渠道，返回注册冲突等错误。
func RegisterExternal(n Notifier) error {
	return register(n)
}

func register(n Notifier) error {
	registry.Lock()
	defer registry.Unlock()
	return registerLocked(n)
}

func registerLocked(n Notifier) error {
	return registerInto(registry.byID, registry.alias, n)
}

// registerInto 把通知渠道写入给定的 id/alias 映射，调用方负责加锁。
func registerInto(byID map[string]Notifier, alias map[string]string, n Notifier) error {
	def := n.Definition()
	id := normalize(def.ID)
	if id == "" || !idPattern.MatchString(id) {
		return errInvalidID
	}
	if _, exists := byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, item := range def.Aliases {
		item = normalize(item)
		if item == "" {
			continue
		}
		if !idPattern.MatchString(item) {
			return errInvalidID
		}
		if _, exists := byID[item]; exists {
			return &DuplicateError{ID: item}
		}
		if _, exists := alias[item]; exists {
			return &DuplicateError{ID: item}
		}
	}
	byID[id] = n
	for _, item := range def.Aliases {
		item = normalize(item)
		if item != "" && item != id {
			alias[item] = id
		}
	}
	return nil
}

// ReplaceBuiltin 原子替换全部内置通知渠道。
//
// 新集合全部注册成功后才交换注册表；失败时保留原有内置条目，
// 避免并发重扫期间出现半套注册或重复注册 panic。外部插件不受影响。
func ReplaceBuiltin(channels []Notifier) error {
	registry.Lock()
	defer registry.Unlock()
	return replaceSourceLocked(SourceBuiltin, channels)
}

func replaceSourceLocked(source Source, channels []Notifier) error {
	byID := make(map[string]Notifier, len(registry.byID)+len(channels))
	for id, n := range registry.byID {
		if n.Source() != source {
			byID[id] = n
		}
	}
	alias := make(map[string]string, len(byID))
	for id, n := range byID {
		for _, item := range n.Definition().Aliases {
			if item = normalize(item); item != "" && item != id {
				alias[item] = id
			}
		}
	}
	for _, n := range channels {
		if n.Source() != source {
			return registryError("plugin source does not match replacement target")
		}
		if err := registerInto(byID, alias, n); err != nil {
			return err
		}
	}
	registry.byID = byID
	registry.alias = alias
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

// List 返回按 ID 排序的全部已注册通知渠道。
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

// ListBySource 返回指定来源的通知渠道。
func ListBySource(source Source) []Notifier {
	all := List()
	out := all[:0]
	for _, n := range all {
		if n.Source() == source {
			out = append(out, n)
		}
	}
	return out
}

// UnregisterSource 移除指定来源的全部通知渠道，用于外部插件重扫。
func UnregisterSource(source Source) {
	registry.Lock()
	defer registry.Unlock()
	clearLocked(source)
}

func clearLocked(source Source) {
	for id, n := range registry.byID {
		if n.Source() != source {
			continue
		}
		delete(registry.byID, id)
		for _, alias := range n.Definition().Aliases {
			alias = normalize(alias)
			if current, ok := registry.alias[alias]; ok && current == id {
				delete(registry.alias, alias)
			}
		}
	}
}

// replaceExternal 原子替换全部外部通知渠道，避免扫描中途失败留下半套插件。
func replaceExternal(channels []Notifier) ([]Notifier, []error) {
	registry.Lock()
	defer registry.Unlock()
	clearLocked(SourceExec)
	loaded := make([]Notifier, 0, len(channels))
	var errs []error
	for _, n := range channels {
		if err := registerLocked(n); err != nil {
			errs = append(errs, err)
			continue
		}
		loaded = append(loaded, n)
	}
	return loaded, errs
}

// DefinitionByID 查找渠道元数据，支持别名，包括已停用的外部渠道。
func DefinitionByID(id string) (Definition, bool) {
	if info, found := ExternalChannelInfo(id); found {
		return Definition{
			ID:          info.ID,
			Name:        info.Name,
			Description: info.Description,
			Aliases:     info.Aliases,
			Fields:      info.Fields,
		}, true
	}
	n, found := Lookup(id)
	if !found {
		return Definition{}, false
	}
	return n.Definition(), true
}

// SecretFields 返回渠道需要加密保存、接口脱敏的字段列表。
func SecretFields(id string) []string {
	def, found := DefinitionByID(id)
	if !found {
		return nil
	}
	fields := make([]string, 0, 4)
	for _, field := range def.Fields {
		if field.Secret || field.Type == "password" {
			fields = append(fields, field.Key)
		}
	}
	return fields
}

// IsSecretField 判断渠道字段是否为敏感字段。
func IsSecretField(channel, field string) bool {
	for _, item := range SecretFields(channel) {
		if item == field {
			return true
		}
	}
	return false
}

// Send 按渠道 ID 发送一条通知。
func Send(channel string, cfg map[string]any, msg Message) error {
	n, ok := Lookup(channel)
	if !ok {
		return i18n.E("service.notify.channel_unsupported", "channel", channel)
	}
	return n.Notify(cfg, msg)
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
