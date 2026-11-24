package channels

import (
	"net/url"
	"strings"

	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("pushdeer", []notify.NativeField{
		text("url", "notifications.fieldServerUrl", false),
		password("pushkey", "notifications.fieldPushKey", true),
	}, sendPushDeer))
}

func sendPushDeer(cfg map[string]any, msg notify.NativeMessage) error {
	base := cfgString(cfg, "url", "https://api2.pushdeer.com")
	pushKey := cfgString(cfg, "pushkey", "")
	if pushKey == "" {
		return missingChannelConfig("pushdeer")
	}
	return notifyForm(strings.TrimRight(base, "/")+"/message/push", url.Values{
		"pushkey": {pushKey},
		"text":    {msg.Title},
		"desp":    {msg.Body},
	}, nil)
}
