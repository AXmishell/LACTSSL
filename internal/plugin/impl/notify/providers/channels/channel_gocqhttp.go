package channels

import (
	"strings"

	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("gocqhttp", []notify.Field{
		text("url", "notifications.fieldServerUrl", false),
		password("access_token", "notifications.fieldAccessToken", false),
		selectField("target_type", "notifications.fieldTargetType", "private",
			opt("private", "notifications.optionPrivate"),
			opt("group", "notifications.optionGroup"),
		),
		text("target_id", "notifications.fieldTargetId", true),
	}, sendGoCQHTTP))
}

func sendGoCQHTTP(cfg map[string]any, msg notify.Message) error {
	base := cfgString(cfg, "url", "http://127.0.0.1:5700")
	targetID := cfgString(cfg, "target_id", "")
	if targetID == "" {
		return missingChannelConfig("gocqhttp")
	}
	endpoint := strings.TrimRight(base, "/") + "/send_private_msg"
	key := "user_id"
	if cfgString(cfg, "target_type", "private") == "group" {
		endpoint = strings.TrimRight(base, "/") + "/send_group_msg"
		key = "group_id"
	}
	return notifyJSON(endpoint, map[string]any{key: targetID, "message": channelMessage(msg.Title, msg.Body)}, bearer(cfgString(cfg, "access_token", "")))
}
