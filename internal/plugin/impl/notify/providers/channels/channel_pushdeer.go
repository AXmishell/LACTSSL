package channels

import (
	"net/url"
	"strings"

	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("pushdeer", []notify.Field{
		text("url", "notifications.fieldServerUrl", false),
		password("pushkey", "notifications.fieldPushKey", true),
	}, sendPushDeer))
}

func sendPushDeer(cfg map[string]any, msg notify.Message) error {
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
