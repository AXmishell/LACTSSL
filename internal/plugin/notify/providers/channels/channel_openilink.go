package channels

import (
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("openilink", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("openilink")))
}
