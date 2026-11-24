// 本文件把官方 DNS provider 的 Native 接口从 providers module 重新导出。
package dnsplugin

import dnsapi "lactssl/providers/api/dns"

type NativeProvider = dnsapi.NativeProvider
type NativeChallengeProvider = dnsapi.NativeChallengeProvider
type NativeVerifier = dnsapi.NativeVerifier

// MarkNativeVerifiable 在 init 中声明服务商支持只读验证。
func MarkNativeVerifiable(ids ...string) { dnsapi.MarkNativeVerifiable(ids...) }

// IsNativeVerifiable 判断服务商是否声明了 verify-v1 能力。
func IsNativeVerifiable(id string) bool { return dnsapi.IsNativeVerifiable(id) }
