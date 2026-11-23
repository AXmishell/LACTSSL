package channels

import (
	"lactssl/internal/plugin/impl/notify"
)

func init() {
	registerChannel(newChannel("openilink", []notify.Field{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("openilink")))
}
