//go:build nobuiltin_notify

package builtin

import notifyplugin "lactssl/internal/plugin/notify"

// SyncNotify 在裁剪通知内置实现的构建中清空内置注册。
func SyncNotify(available, enabled bool, ids map[string]bool) error {
	if err := notifyplugin.ReplaceBuiltin(nil); err != nil {
		return err
	}
	notifyplugin.SetBuiltinChannels(nil)
	return nil
}
