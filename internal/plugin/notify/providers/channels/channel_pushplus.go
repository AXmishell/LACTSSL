package channels

import (
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("pushplus", []notify.NativeField{
		password("token", "notifications.fieldToken", true),
		text("topic", "notifications.fieldTopic", false),
	}, sendPushPlus))
}

func sendPushPlus(cfg map[string]any, msg notify.NativeMessage) error {
	token := cfgString(cfg, "token", "")
	if token == "" {
		return missingChannelConfig("pushplus")
	}
	payload := map[string]any{"token": token, "title": msg.Title, "content": escapeMarkdown(msg.Body), "template": "markdown"}
	if topic := cfgString(cfg, "topic", ""); topic != "" {
		payload["topic"] = topic
	}
	return notifyJSON("https://www.pushplus.plus/send", payload, nil)
}
