// Package notifyplugin 提供通知渠道的插件注册表。
//
// 官方渠道实现随主程序编译，定义在 native_registry.go 与 providers/ 中；
// 外部渠道通过一次性 exec helper 提供，由 Discover 在启动或手动重扫时注册。
package notifyplugin

import (
	"strings"

	"lactssl/internal/i18n"
	"lactssl/internal/plugin/runtime/registry"
)

// Source 标识通知插件来源。
type Source = registry.Source

const (
	// SourceBuiltin 表示编译进 helper 的内置通知渠道。
	SourceBuiltin = registry.Builtin
	// SourceExec 表示外部 exec helper 插件。
	SourceExec = registry.Exec
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

var plugins = registry.New(registry.Adapter[Notifier]{
	ID:         func(n Notifier) string { return n.Definition().ID },
	Aliases:    func(n Notifier) []string { return n.Definition().Aliases },
	Source:     func(n Notifier) Source { return n.Source() },
	ValidateID: func(id string) bool { return idPattern.MatchString(id) },
})

// Register 注册内置通知渠道。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立即发现冲突。
func Register(n Notifier) {
	if err := plugins.Register(n); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部通知渠道，返回注册冲突等错误。
func RegisterExternal(n Notifier) error {
	return plugins.Register(n)
}

// ReplaceBuiltin 原子替换全部内置通知渠道。
//
// 新集合全部注册成功后才交换注册表；失败时保留原有内置条目，
// 避免并发重扫期间出现半套注册或重复注册 panic。外部插件不受影响。
func ReplaceBuiltin(items []Notifier) error {
	return plugins.ReplaceBuiltin(items)
}

// Lookup 按 ID 或别名查找通知渠道。
func Lookup(name string) (Notifier, bool) {
	return plugins.Lookup(name)
}

// Has 判断通知渠道是否已注册（含别名）。
func Has(name string) bool {
	return plugins.Has(name)
}

// List 返回按 ID 排序的全部已注册通知渠道。
func List() []Notifier {
	return plugins.List()
}

// ListBySource 返回指定来源的通知渠道。
func ListBySource(source Source) []Notifier {
	return plugins.ListBySource(source)
}

// UnregisterSource 移除指定来源的全部通知渠道，用于外部插件重扫。
func UnregisterSource(source Source) {
	plugins.UnregisterSource(source)
}

// replaceExternal 原子替换全部外部通知渠道，避免扫描中途失败留下半套插件。
func replaceExternal(items []Notifier) ([]Notifier, []error) {
	return plugins.ReplaceSourceBestEffort(SourceExec, items)
}

// resetForTest 清空注册表，仅供测试使用。
func resetForTest() {
	plugins.Reset()
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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
