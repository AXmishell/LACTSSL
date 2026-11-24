package channels

import (
	"net/url"
	"strings"

	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("pushme", []notify.NativeField{
		text("url", "notifications.fieldServerUrl", false),
		password("push_key", "notifications.fieldPushKey", true),
	}, sendPushMe))
}

func sendPushMe(cfg map[string]any, msg notify.NativeMessage) error {
	base := cfgString(cfg, "url", "https://push.i-i.me")
	key := cfgString(cfg, "push_key", "")
	if key == "" {
		return missingChannelConfig("pushme")
	}
	return notifyForm(strings.TrimRight(base, "/")+"/"+url.PathEscape(key), url.Values{
		"title":   {msg.Title},
		"content": {msg.Body},
	}, nil)
}
