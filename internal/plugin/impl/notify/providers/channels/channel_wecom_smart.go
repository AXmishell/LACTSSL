package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("wecom_smart", []notify.Field{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("wecom_smart")))
}
