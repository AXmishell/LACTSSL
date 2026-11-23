//go:build nobuiltin_dns

package builtin

import dnsplugin "lactssl/internal/plugin/dns"

// SyncDNS 在裁剪 DNS 内置实现的构建中清空内置注册。
func SyncDNS(available, enabled bool, ids map[string]bool) error {
	if err := dnsplugin.ReplaceBuiltin(nil); err != nil {
		return err
	}
	dnsplugin.SetBuiltinProviders(nil)
	return nil
}
