package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("wpush", []notify.Field{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("wpush")))
}
