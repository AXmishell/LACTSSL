package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("wxpusher_spt", []notify.Field{
		password("spt", "notifications.fieldSpt", true),
	}, sendWxPusherSPT))
}

func sendWxPusherSPT(cfg map[string]any, msg notify.Message) error {
	spt := cfgString(cfg, "spt", "")
	if spt == "" {
		return missingChannelConfig("wxpusher_spt")
	}
	return notifyJSON("https://wxpusher.zjiecode.com/api/send/message/simple-push", map[string]any{
		"spt":     spt,
		"content": channelMessage(msg.Title, msg.Body),
		"summary": msg.Title,
	}, nil)
}
