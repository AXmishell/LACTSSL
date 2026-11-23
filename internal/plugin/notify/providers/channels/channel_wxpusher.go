package channels

import (
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("wxpusher", []notify.NativeField{
		password("app_token", "notifications.fieldAppToken", true),
		text("uids", "notifications.fieldUids", false),
		text("topic_ids", "notifications.fieldTopicIds", false),
	}, sendWxPusher))
}

func sendWxPusher(cfg map[string]any, msg notify.NativeMessage) error {
	appToken := cfgString(cfg, "app_token", "")
	if appToken == "" {
		return missingChannelConfig("wxpusher")
	}
	return notifyJSON("https://wxpusher.zjiecode.com/api/send/message", map[string]any{
		"appToken":    appToken,
		"content":     channelMessage(msg.Title, msg.Body),
		"summary":     msg.Title,
		"contentType": 1,
		"uids":        cfgList(cfg, "uids"),
		"topicIds":    cfgList(cfg, "topic_ids"),
	}, nil)
}
