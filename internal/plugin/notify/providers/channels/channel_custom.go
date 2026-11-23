package channels

import (
	"encoding/json"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("custom", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		selectField("method", "notifications.fieldMethod", "POST",
			opt("POST", "notifications.optionPost"),
			opt("GET", "notifications.optionGet"),
		),
		secretTextarea("headers", "notifications.fieldHeaders", `{"Content-Type":"application/json"}`),
		secretTextarea("body", "notifications.fieldBody", `{"title":"{title}","content":"{content}"}`),
	}, sendCustom))
}

func sendCustom(cfg map[string]any, msg notify.NativeMessage) error {
	endpoint := cfgString(cfg, "url", "")
	if endpoint == "" {
		return missingChannelConfig("custom")
	}
	method := strings.ToUpper(cfgString(cfg, "method", "POST"))
	headers := map[string]string{}
	if raw := cfgString(cfg, "headers", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return i18n.E("service.notify.channel_failed", "error", "invalid headers JSON: "+err.Error())
		}
	}
	bodyTemplate := cfgString(cfg, "body", `{"title":"{title}","content":"{content}"}`)
	contentType := headers["Content-Type"]
	if contentType == "" {
		contentType = "application/json"
	}
	title, body := msg.Title, msg.Body
	if strings.Contains(strings.ToLower(contentType), "json") {
		title, body = escapeJSONString(title), escapeJSONString(body)
	}
	rendered := strings.NewReplacer("{title}", title, "{content}", body).Replace(bodyTemplate)
	return notifyHTTP(method, endpoint, []byte(rendered), contentType, headers)
}
