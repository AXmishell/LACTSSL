package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("pushplus", []notify.Field{
		password("token", "notifications.fieldToken", true),
		text("topic", "notifications.fieldTopic", false),
	}, sendPushPlus))
}

func sendPushPlus(cfg map[string]any, msg notify.Message) error {
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
