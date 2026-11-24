package channels

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"time"

	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("feishu", []notify.NativeField{
		password("webhook", "notifications.fieldWebhook", true),
		password("secret", "notifications.fieldSecret", false),
	}, sendFeishu))
}

func sendFeishu(cfg map[string]any, msg notify.NativeMessage) error {
	webhook := cfgString(cfg, "webhook", "")
	if webhook == "" {
		return missingChannelConfig("feishu")
	}
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]any{"text": channelMessage(msg.Title, msg.Body)},
	}
	if secret := cfgString(cfg, "secret", ""); secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
		mac.Write([]byte{})
		payload["timestamp"] = timestamp
		payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	return notifyJSON(webhook, payload, nil)
}
