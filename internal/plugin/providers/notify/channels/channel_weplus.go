package channels

import (
	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("weplus", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("weplus")))
}
