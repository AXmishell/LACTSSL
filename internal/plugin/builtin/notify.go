//go:build !nobuiltin_notify

// Package builtin 把合并进主仓库的官方插件适配到宿主注册表。
//
// 内置实现只在对应类别/条目启用时注册，未启用条目仍保留元数据，
// 供插件页展示、历史配置脱敏和后续切换。
package builtin

import (
	notifyplugin "lactssl/internal/plugin/notify"
	"lactssl/internal/plugin/notify/providers/channels"
	pluginproto "lactssl/internal/plugin/proto"
	"strings"
	"sync"
)

var notifyOnce sync.Once

func loadNotify() {
	notifyOnce.Do(func() {
		channels.RegisterAll()
	})
}

type builtinNotifier struct {
	inner notifyplugin.NativeNotifier
	def   notifyplugin.Definition
	info  notifyplugin.ExternalChannel
}

func (n *builtinNotifier) Definition() notifyplugin.Definition { return n.def }
func (n *builtinNotifier) Source() notifyplugin.Source         { return notifyplugin.SourceBuiltin }
func (n *builtinNotifier) Notify(cfg map[string]any, msg notifyplugin.Message) error {
	return n.inner.Notify(cfg, notifyplugin.NativeMessage{Title: msg.Title, Body: msg.Body})
}
func (n *builtinNotifier) Info() notifyplugin.ExternalChannel { return n.info }

// SyncNotify 重建内置通知渠道注册。
// 返回注册错误；调用方应保留原有元数据并把错误暴露给管理员。
func SyncNotify(available, enabled bool, ids map[string]bool) error {
	if !available {
		if err := notifyplugin.ReplaceBuiltin(nil); err != nil {
			return err
		}
		notifyplugin.SetBuiltinChannels(nil)
		return nil
	}
	loadNotify()
	all := notifyplugin.ListNative()
	infos := make([]notifyplugin.ExternalChannel, 0, len(all))
	adapters := make([]*builtinNotifier, 0, len(all))
	for _, notifier := range all {
		definition := notifier.Definition()
		id := strings.ToLower(strings.TrimSpace(definition.ID))
		if id == "" {
			continue
		}
		active := enabled && ids[id]
		hostDef, fields := buildNotifyDefinition(definition)
		info := notifyplugin.ExternalChannel{
			ID:          id,
			Name:        hostDef.Name,
			Description: hostDef.Description,
			Aliases:     hostDef.Aliases,
			Fields:      fields,
			Version:     "builtin",
			Enabled:     active,
			Source:      string(notifyplugin.SourceBuiltin),
		}
		infos = append(infos, info)
		if active {
			adapters = append(adapters, &builtinNotifier{inner: notifier, def: hostDef, info: info})
		}
	}
	channels := make([]notifyplugin.Notifier, 0, len(adapters))
	for _, adapter := range adapters {
		channels = append(channels, adapter)
	}
	if err := notifyplugin.ReplaceBuiltin(channels); err != nil {
		return err
	}
	notifyplugin.SetBuiltinChannels(infos)
	return nil
}

func buildNotifyDefinition(definition notifyplugin.NativeDefinition) (notifyplugin.Definition, []notifyplugin.Field) {
	fields := make([]notifyplugin.Field, 0, len(definition.Fields))
	for _, field := range definition.Fields {
		options := make([]notifyplugin.Option, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, notifyplugin.Option{
				Label:    option.Label,
				LabelKey: option.LabelKey,
				Value:    option.Value,
			})
		}
		fields = append(fields, pluginproto.NotifyField{
			Key:         field.Key,
			Label:       field.Label,
			LabelKey:    field.LabelKey,
			Type:        field.Type,
			Required:    field.Required,
			Secret:      field.Secret,
			Placeholder: field.Placeholder,
			Help:        field.Help,
			Default:     field.Default,
			Options:     options,
		})
	}
	return notifyplugin.Definition{
		ID:          strings.ToLower(strings.TrimSpace(definition.ID)),
		Name:        definition.Name,
		Description: definition.Description,
		Aliases:     definition.Aliases,
		Fields:      fields,
	}, fields
}
