package channels

import (
	"net/url"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("bark", []notify.Field{
		text("url", "notifications.fieldServerUrl", false),
		password("key", "notifications.fieldKey", true),
		text("sound", "notifications.fieldSound", false),
		text("group", "notifications.fieldGroup", false),
	}, sendBark))
}

func sendBark(cfg map[string]any, msg notify.Message) error {
	base := cfgString(cfg, "url", "https://api.day.app")
	key := cfgString(cfg, "key", "")
	if key == "" {
		return missingChannelConfig("bark")
	}
	payload := map[string]any{"title": msg.Title, "body": msg.Body}
	if sound := cfgString(cfg, "sound", ""); sound != "" {
		payload["sound"] = sound
	}
	if group := cfgString(cfg, "group", ""); group != "" {
		payload["group"] = group
	}
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return i18n.E("service.url_invalid", "url", outbound.RedactURL(base))
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(key)
	return notifyJSON(parsed.String(), payload, nil)
}
