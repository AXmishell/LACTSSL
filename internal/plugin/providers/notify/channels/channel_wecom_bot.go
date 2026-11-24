package channels

import (
	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("wecom_bot", []notify.NativeField{
		password("webhook", "notifications.fieldWebhook", true),
	}, sendWecomBot))
}

func sendWecomBot(cfg map[string]any, msg notify.NativeMessage) error {
	webhook := cfgString(cfg, "webhook", "")
	if webhook == "" {
		return missingChannelConfig("wecom_bot")
	}
	return notifyJSON(webhook, map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": truncateUTF8Bytes(escapeMarkdown(msg.Title+"\n"+msg.Body), 4000)},
	}, nil)
}
