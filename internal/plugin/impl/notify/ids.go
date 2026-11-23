package notify

// 官方通知渠道 ID。该列表只用于构建工具生成 manifest，刻意不导入
// notify/providers/channels，避免 go run 构建工具时执行渠道 init()。
// 新增渠道时同步更新本文件；channels 包测试会校验两者一致。
var officialChannelIDs = []string{
	"bark",
	"chronocat",
	"custom",
	"dingtalk",
	"feishu",
	"gocqhttp",
	"gotify",
	"igot",
	"ntfy",
	"openilink",
	"pushdeer",
	"pushme",
	"pushplus",
	"serverchan",
	"synology_chat",
	"telegram",
	"wecom_app",
	"wecom_bot",
	"wecom_smart",
	"weplus",
	"wpush",
	"wxpusher",
	"wxpusher_spt",
}

// OfficialChannelIDs 返回官方通知渠道 ID 的副本。
func OfficialChannelIDs() []string {
	out := make([]string, len(officialChannelIDs))
	copy(out, officialChannelIDs)
	return out
}
