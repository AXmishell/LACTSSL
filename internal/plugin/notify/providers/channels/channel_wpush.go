package channels

import (
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("wpush", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("wpush")))
}
