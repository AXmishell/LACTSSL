package channels

import (
	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("openilink", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("openilink")))
}
