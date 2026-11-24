package channels

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("ntfy", []notify.NativeField{
		text("url", "notifications.fieldServerUrl", false),
		text("topic", "notifications.fieldTopic", true),
		password("token", "notifications.fieldToken", false),
		number("priority", "notifications.fieldPriority", 3),
	}, sendNtfy))
}

func sendNtfy(cfg map[string]any, msg notify.NativeMessage) error {
	base := cfgString(cfg, "url", "https://ntfy.sh")
	topic := cfgString(cfg, "topic", "")
	if topic == "" {
		return missingChannelConfig("ntfy")
	}
	headers := map[string]string{
		"Title":    msg.Title,
		"Priority": strconv.Itoa(cfgInt(cfg, "priority", 3)),
	}
	for key, value := range bearer(cfgString(cfg, "token", "")) {
		headers[key] = value
	}
	return notifyHTTP(http.MethodPost, strings.TrimRight(base, "/")+"/"+url.PathEscape(topic), []byte(msg.Body), "text/plain", headers)
}
