// 本文件把官方 DNS provider 注册表从 providers module 重新导出。
package dnsplugin

import dnsapi "lactssl/providers/api/dns"

// RegisterNative 注册内置服务商。重复的 ID 或别名会直接 panic。
func RegisterNative(p NativeProvider) { dnsapi.RegisterNative(p) }

// LookupNative 按 ID 或别名查找服务商。
func LookupNative(name string) (NativeProvider, bool) { return dnsapi.LookupNative(name) }

// HasNative 判断服务商是否已注册（含别名）。
func HasNative(name string) bool { return dnsapi.HasNative(name) }

// ListNative 返回按 ID 排序的全部服务商。
func ListNative() []NativeProvider { return dnsapi.ListNative() }

// ResetNativeForTest 清空注册表，仅供测试使用。
func ResetNativeForTest() { dnsapi.ResetNativeForTest() }
