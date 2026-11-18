package service

import (
	"encoding/json"
	"strings"

	"lactssl/internal/plugin/notify"
)

// NotifyChannelConfig 是单个通知渠道的配置。
type NotifyChannelConfig map[string]any

// LoadNotifyChannels 读取所有通知渠道配置。
func (m *Manager) LoadNotifyChannels() map[string]NotifyChannelConfig {
	result := make(map[string]NotifyChannelConfig)
	raw, _ := m.Store.GetSecret("notify.channels")
	if raw == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

// SaveNotifyChannels 保存所有通知渠道配置。
func (m *Manager) SaveNotifyChannels(channels map[string]NotifyChannelConfig) error {
	data, err := json.Marshal(channels)
	if err != nil {
		return err
	}
	return m.Store.SetSecret("notify.channels", string(data))
}

// legacyNotifySecretFields 是迁移到通知插件前已知渠道的敏感字段。
// helper 未安装时插件元数据不可用，仍需依靠这份列表避免把旧密钥回传到前端。
var legacyNotifySecretFields = map[string][]string{
	"gotify":        {"token"},
	"ntfy":          {"token"},
	"gocqhttp":      {"access_token"},
	"serverchan":    {"sendkey"},
	"pushdeer":      {"pushkey"},
	"bark":          {"key"},
	"telegram":      {"bot_token"},
	"dingtalk":      {"webhook", "secret"},
	"wecom_bot":     {"webhook"},
	"wecom_app":     {"corp_secret"},
	"wecom_smart":   {"token"},
	"igot":          {"key"},
	"pushplus":      {"token"},
	"weplus":        {"token"},
	"wxpusher":      {"app_token"},
	"wxpusher_spt":  {"spt"},
	"openilink":     {"token"},
	"wpush":         {"token"},
	"synology_chat": {"url"},
	"feishu":        {"webhook", "secret"},
	"pushme":        {"push_key"},
	"chronocat":     {"token"},
	"custom":        {"headers"},
}

// NotifyChannelsForUI 返回脱敏后的渠道配置，避免把密钥回传到前端。
func (m *Manager) NotifyChannelsForUI() map[string]NotifyChannelConfig {
	channels := m.LoadNotifyChannels()
	for name, cfg := range channels {
		secrets := notifyplugin.SecretFields(name)
		secrets = append(secrets, legacyNotifySecretFields[name]...)
		if _, known := notifyplugin.DefinitionByID(name); !known {
			// 渠道定义缺失时无法判断哪些字段是密钥，保守地清空全部字段。
			for key := range cfg {
				secrets = append(secrets, key)
			}
		}
		for _, field := range secrets {
			if _, exists := cfg[field]; exists {
				cfg[field] = ""
			}
		}
	}
	return channels
}

// IsNotifyChannel 判断渠道名称是否受支持（含已停用的插件渠道）。
func IsNotifyChannel(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "mail" {
		return true
	}
	_, found := notifyplugin.DefinitionByID(name)
	return found
}

// MergeNotifyChannelConfig 合并前端提交的配置，空密钥表示保留旧值。
func MergeNotifyChannelConfig(channel string, existing, incoming NotifyChannelConfig) NotifyChannelConfig {
	if existing == nil {
		existing = NotifyChannelConfig{}
	}
	_, known := notifyplugin.DefinitionByID(channel)
	for key, value := range incoming {
		secret := notifyplugin.IsSecretField(channel, key) || isLegacyNotifySecretField(channel, key)
		if !known {
			// 定义缺失（例如 helper 被卸载）时，空字符串一律视为保留旧值。
			secret = true
		}
		if secret {
			text, ok := value.(string)
			if ok && strings.TrimSpace(text) == "" {
				continue
			}
		}
		existing[key] = value
	}
	return existing
}

func isLegacyNotifySecretField(channel, field string) bool {
	for _, item := range legacyNotifySecretFields[channel] {
		if item == field {
			return true
		}
	}
	return false
}

// SendNotifyChannel 按渠道类型发送一条通知。
func SendNotifyChannel(name string, cfg NotifyChannelConfig, title, body string) error {
	return notifyplugin.Send(name, cfg, notifyplugin.Message{Title: title, Body: body})
}

// MigrateNotifyChannels 把旧版 Gotify / Server酱 设置迁移到通用渠道配置。
// 迁移只补齐缺失的渠道，不会覆盖用户已经保存的插件配置。
func (m *Manager) MigrateNotifyChannels() error {
	channels := m.LoadNotifyChannels()
	changed := false

	if _, exists := channels["gotify"]; !exists {
		url, _ := m.Store.GetSetting("gotify.url")
		token, _ := m.Store.GetSecret("gotify.token")
		priority, _ := m.Store.GetSetting("gotify.priority")
		enabled, _ := m.Store.GetSetting("gotify.enabled")
		if strings.TrimSpace(url) != "" || strings.TrimSpace(token) != "" {
			cfg := NotifyChannelConfig{
				"url":     strings.TrimSpace(url),
				"token":   strings.TrimSpace(token),
				"enabled": enabled == "true",
			}
			if priority != "" {
				cfg["priority"] = priority
			}
			channels["gotify"] = cfg
			changed = true
		}
	}
	if _, exists := channels["serverchan"]; !exists {
		sendKey, _ := m.Store.GetSecret("serverchan.sendkey")
		version, _ := m.Store.GetSetting("serverchan.version")
		enabled, _ := m.Store.GetSetting("serverchan.enabled")
		if strings.TrimSpace(sendKey) != "" {
			if version == "" {
				version = "auto"
			}
			channels["serverchan"] = NotifyChannelConfig{
				"sendkey": strings.TrimSpace(sendKey),
				"version": version,
				"enabled": enabled == "true",
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return m.SaveNotifyChannels(channels)
}

// NotifyChannelDefinitions 返回当前已启用的插件通知渠道定义，供前端渲染配置表单。
func (m *Manager) NotifyChannelDefinitions() []notifyplugin.Definition {
	channels := notifyplugin.ExternalEnabledChannels()
	out := make([]notifyplugin.Definition, 0, len(channels))
	for _, channel := range channels {
		out = append(out, notifyplugin.Definition{
			ID:          channel.ID,
			Name:        channel.Name,
			Description: channel.Description,
			Aliases:     channel.Aliases,
			Fields:      channel.Fields,
		})
	}
	return out
}

// NotifyChannelConfigEnabled 判断渠道配置是否已启用。
func NotifyChannelConfigEnabled(cfg NotifyChannelConfig) bool {
	switch value := cfg["enabled"].(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
}
