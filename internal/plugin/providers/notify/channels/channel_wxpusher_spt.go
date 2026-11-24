package channels

import (
	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("wxpusher_spt", []notify.NativeField{
		password("spt", "notifications.fieldSpt", true),
	}, sendWxPusherSPT))
}

func sendWxPusherSPT(cfg map[string]any, msg notify.NativeMessage) error {
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
