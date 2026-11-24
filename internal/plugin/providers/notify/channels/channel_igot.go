package channels

import (
	"net/url"

	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("igot", []notify.NativeField{
		password("key", "notifications.fieldKey", true),
	}, sendIGot))
}

func sendIGot(cfg map[string]any, msg notify.NativeMessage) error {
	key := cfgString(cfg, "key", "")
	if key == "" {
		return missingChannelConfig("igot")
	}
	return notifyForm("https://push.hellyw.com/"+url.PathEscape(key), url.Values{
		"title":   {msg.Title},
		"content": {msg.Body},
	}, nil)
}
