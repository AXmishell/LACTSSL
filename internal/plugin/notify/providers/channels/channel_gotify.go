package channels

import (
	"net/url"
	"strings"

	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("gotify", []notify.NativeField{
		text("url", "notifications.fieldServerUrl", true),
		password("token", "notifications.fieldToken", true),
		number("priority", "notifications.fieldPriority", 5),
	}, sendGotify))
}

func sendGotify(cfg map[string]any, msg notify.NativeMessage) error {
	base := cfgString(cfg, "url", "")
	token := cfgString(cfg, "token", "")
	if base == "" || token == "" {
		return missingChannelConfig("gotify")
	}
	endpoint := strings.TrimRight(base, "/") + "/message?token=" + url.QueryEscape(token)
	priority := cfgInt(cfg, "priority", 5)
	if priority < 0 || priority > 10 {
		priority = 5
	}
	return notifyJSON(endpoint, map[string]any{
		"title":    msg.Title,
		"message":  msg.Body,
		"priority": priority,
	}, nil)
}
