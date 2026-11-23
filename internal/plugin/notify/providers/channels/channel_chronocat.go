package channels

import (
	"strings"

	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("chronocat", []notify.NativeField{
		text("url", "notifications.fieldServerUrl", false),
		password("token", "notifications.fieldToken", false),
		selectField("target_type", "notifications.fieldTargetType", "private",
			opt("private", "notifications.optionPrivate"),
			opt("group", "notifications.optionGroup"),
		),
		text("target", "notifications.fieldTarget", true),
	}, sendChronoCat))
}

func sendChronoCat(cfg map[string]any, msg notify.NativeMessage) error {
	endpoint := cfgString(cfg, "url", "http://127.0.0.1:5500")
	target := cfgString(cfg, "target", "")
	if target == "" {
		return missingChannelConfig("chronocat")
	}
	return notifyJSON(strings.TrimRight(endpoint, "/")+"/api/message/send", map[string]any{
		"target":      target,
		"target_type": cfgString(cfg, "target_type", "private"),
		"message":     channelMessage(msg.Title, msg.Body),
	}, bearer(cfgString(cfg, "token", "")))
}
