package channels

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("dingtalk", []notify.Field{
		password("webhook", "notifications.fieldWebhook", true),
		password("secret", "notifications.fieldSecret", false),
	}, sendDingTalk))
}

func sendDingTalk(cfg map[string]any, msg notify.Message) error {
	webhook := cfgString(cfg, "webhook", "")
	if webhook == "" {
		return missingChannelConfig("dingtalk")
	}
	if secret := cfgString(cfg, "secret", ""); secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "\n" + secret))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		parsed, err := url.Parse(webhook)
		if err != nil {
			return i18n.E("service.url_invalid", "url", outbound.RedactURL(webhook))
		}
		query := parsed.Query()
		query.Set("timestamp", timestamp)
		query.Set("sign", sign)
		parsed.RawQuery = query.Encode()
		webhook = parsed.String()
	}
	title := escapeMarkdown(msg.Title)
	return notifyJSON(webhook, map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"title": title, "text": "### " + title + "\n\n" + escapeMarkdown(msg.Body)},
	}, nil)
}
