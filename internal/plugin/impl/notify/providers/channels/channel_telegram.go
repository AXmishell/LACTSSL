package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("telegram", []notify.Field{
		password("bot_token", "notifications.fieldBotToken", true),
		text("chat_id", "notifications.fieldChatId", true),
	}, sendTelegram))
}

func sendTelegram(cfg map[string]any, msg notify.Message) error {
	token := cfgString(cfg, "bot_token", "")
	chatID := cfgString(cfg, "chat_id", "")
	if token == "" || chatID == "" {
		return missingChannelConfig("telegram")
	}
	return notifyJSON("https://api.telegram.org/bot"+token+"/sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     truncateRunes(channelMessage(msg.Title, msg.Body), 4000),
		"disable_web_page_preview": true,
	}, nil)
}
