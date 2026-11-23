package channels

import (
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("wecom_smart", []notify.NativeField{
		text("url", "notifications.fieldUrl", true),
		password("token", "notifications.fieldToken", false),
	}, sendSimpleBearerNamed("wecom_smart")))
}
