package channels

import (
	"encoding/json"
	"net/url"

	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("synology_chat", []notify.NativeField{
		password("url", "notifications.fieldWebhook", true),
	}, sendSynologyChat))
}

func sendSynologyChat(cfg map[string]any, msg notify.NativeMessage) error {
	endpoint := cfgString(cfg, "url", "")
	if endpoint == "" {
		return missingChannelConfig("synology_chat")
	}
	payload, _ := json.Marshal(map[string]string{"text": channelMessage(msg.Title, msg.Body)})
	return notifyForm(endpoint, url.Values{"payload": {string(payload)}}, nil)
}
