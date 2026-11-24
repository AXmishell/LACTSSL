// 本文件把官方通知渠道的 Native 接口与注册表从 providers module 重新导出。
package notifyplugin

import notifyapi "lactssl/providers/api/notify"

type NativeField = notifyapi.NativeField
type NativeOption = notifyapi.NativeOption
type NativeDefinition = notifyapi.NativeDefinition
type NativeMessage = notifyapi.NativeMessage
type NativeNotifier = notifyapi.NativeNotifier

func RegisterNative(n NativeNotifier)                 { notifyapi.RegisterNative(n) }
func LookupNative(name string) (NativeNotifier, bool) { return notifyapi.LookupNative(name) }
func HasNative(name string) bool                      { return notifyapi.HasNative(name) }
func ListNative() []NativeNotifier                    { return notifyapi.ListNative() }
func SendNative(channel string, cfg map[string]any, msg NativeMessage) error {
	return notifyapi.SendNative(channel, cfg, msg)
}
func ResetNativeForTest() { notifyapi.ResetNativeForTest() }
