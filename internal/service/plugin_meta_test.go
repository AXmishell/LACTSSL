package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/i18n"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/meta"
)

func TestLocalizeDNSProvidersMarksAvailability(t *testing.T) {
	dnsplugin.UnregisterSource(dnsplugin.SourceExec)
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	defs := LocalizeDNSProviders(i18n.ZhCN)
	// 没有已启用插件时，列表为空；历史账号的元数据由 manifest 缓存提供。
	if len(defs) != 0 {
		t.Fatalf("expected no available DNS providers, got %d", len(defs))
	}
	if _, exists := DNSProviderInfo("ovh"); !exists {
		t.Fatal("ovh metadata should still be available from manifest cache")
	}
}

// TestCachedProviderMetadataTranslations 验证 manifest 缓存中的多语言字段
// 能正确转换为主程序 ProviderDef，不再依赖插件仓库的 catalog/i18n。
func TestCachedProviderMetadataTranslations(t *testing.T) {
	provider, ok := pluginmeta.Provider("ovh")
	if !ok {
		t.Fatal("cached ovh metadata missing")
	}
	def := manifestProviderDef(provider)
	for _, tc := range []struct {
		locale i18n.Locale
		label  string
		desc   string
		field  string
		help   string
		option string
	}{
		{i18n.ZhCN, "OVH", "OVH DNS", "接入点", "选择 OVH 区域", "欧洲"},
		{i18n.EnUS, "OVH Cloud", "OVH DNS API", "Endpoint", "Select the OVH region", "Europe"},
	} {
		localized := def.Localized(tc.locale)
		if localized.Label != tc.label || localized.Desc != tc.desc {
			t.Fatalf("%s label/desc = %q/%q", tc.locale, localized.Label, localized.Desc)
		}
		if len(localized.Fields) != 1 {
			t.Fatalf("%s fields = %+v", tc.locale, localized.Fields)
		}
		field := localized.Fields[0]
		if field.Label != tc.field || field.Help != tc.help {
			t.Fatalf("%s field label/help = %q/%q", tc.locale, field.Label, field.Help)
		}
		if len(field.Options) != 1 || field.Options[0].Label != tc.option {
			t.Fatalf("%s options = %+v", tc.locale, field.Options)
		}
	}
}

func TestBuiltinDNSProviderIDs(t *testing.T) {
	ids := BuiltinDNSProviderIDs()
	if len(ids) < 30 {
		t.Fatalf("unexpected built-in provider count: %d", len(ids))
	}
	found := false
	for _, id := range ids {
		if id == "cloudflare" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cloudflare should be part of the migrated built-in provider IDs")
	}
	// 返回副本，调用方修改不应影响内部状态。
	ids[0] = "changed"
	if BuiltinDNSProviderIDs()[0] == "changed" {
		t.Fatal("BuiltinDNSProviderIDs should return a copy")
	}
}

func TestLocalizeDNSProvidersHidesDisabledPluginProviders(t *testing.T) {
	dnsplugin.UnregisterSource(dnsplugin.SourceExec)
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","providers":[{"id":"ovh","name":"OVH"},{"id":"external-only","name":"External Only"}]}
JSON
  ;;
esac
`
	path := filepath.Join(dir, "helper")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	// 未启用白名单：两个插件服务商默认都停用。
	result := dnsplugin.Discover(dnsplugin.DiscoverConfig{Dir: dir})
	if result.Providers != 0 || len(result.Disabled) != 2 {
		t.Fatalf("discover result: %+v", result)
	}

	byID := make(map[string]ProviderDef)
	for _, def := range LocalizeDNSProviders(i18n.ZhCN) {
		byID[def.Value] = def
	}
	if _, exists := byID["ovh"]; exists {
		t.Fatal("disabled plugin provider should not be listed")
	}
	if _, exists := byID["external-only"]; exists {
		t.Fatal("disabled external-only provider should not be listed")
	}
	// 元数据仍需保留，供历史账户展示名称与脱敏使用。
	if _, ok := DNSProviderInfo("ovh"); !ok {
		t.Fatal("ovh metadata should remain available for accounts")
	}
	// 重新启用后应恢复展示。
	dnsplugin.Discover(dnsplugin.DiscoverConfig{Dir: dir, EnabledProviders: []string{"ovh", "external-only"}})
	byID = make(map[string]ProviderDef)
	for _, def := range LocalizeDNSProviders(i18n.ZhCN) {
		byID[def.Value] = def
	}
	ovh, ok := byID["ovh"]
	if !ok || !ovh.Available || ovh.Source != string(dnsplugin.SourceExec) {
		t.Fatalf("enabled ovh = %+v", ovh)
	}
	if _, exists := byID["external-only"]; !exists {
		t.Fatal("enabled external-only provider should be listed")
	}
}

func TestNewDNSProviderUnavailable(t *testing.T) {
	if dnsplugin.Has("ovh") {
		t.Skip("ovh is registered in this test")
	}
	_, _, err := newDNSProvider("ovh", nil)
	if err == nil || !strings.Contains(err.Error(), "ovh") {
		t.Fatalf("unexpected error: %v", err)
	}
}
