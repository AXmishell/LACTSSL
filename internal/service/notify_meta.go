package service

import "lactssl/internal/plugin/host/notify"

// BuiltinNotifyChannelIDs 返回主程序内置、不允许通知插件覆盖的渠道 ID。
// 邮件由主程序直接实现，其余渠道都需要通过通知插件提供。
func BuiltinNotifyChannelIDs() map[string]bool {
	out := notifyplugin.BuiltinChannelIDs()
	out["mail"] = true
	return out
}

// DefaultNotifyChannelIDs 返回从内置渠道迁移到通知插件后默认启用的渠道 ID。
// 迁移前这些渠道直接编译在主程序中，升级后应保持可用。
func DefaultNotifyChannelIDs() []string {
	return []string{
		"gotify",
		"ntfy",
		"gocqhttp",
		"serverchan",
		"pushdeer",
		"bark",
		"telegram",
		"dingtalk",
		"wecom_bot",
		"wecom_app",
		"wecom_smart",
		"igot",
		"pushplus",
		"weplus",
		"wxpusher",
		"wxpusher_spt",
		"openilink",
		"wpush",
		"synology_chat",
		"feishu",
		"pushme",
		"chronocat",
		"custom",
	}
}
