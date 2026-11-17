package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/catalog"
	plugini18n "github.com/AXmishell/LACTSSL-Plugins/i18n"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
)

func TestLocalizeDNSProvidersMarksAvailability(t *testing.T) {
	defs := LocalizeDNSProviders(i18n.ZhCN)
	builtins := dnsplugin.ListBySource(dnsplugin.SourceBuiltin)
	// 默认构建不编译任何服务商，列表应只包含当前可用的插件服务商。
	// 测试环境未扫描插件目录时，列表为空。
	if len(defs) != len(builtins) {
		t.Fatalf("catalog should only contain available providers: %d definitions, %d built-ins", len(defs), len(builtins))
	}
	byID := make(map[string]ProviderDef, len(defs))
	for _, def := range defs {
		byID[def.Value] = def
	}
	// 所有编译进主程序的内置服务商都应出现在列表中且可用。
	for _, plugin := range builtins {
		def, exists := byID[plugin.ID()]
		if !exists {
			t.Fatalf("built-in provider %s missing from list", plugin.ID())
		}
		if !def.Available || def.Source != string(dnsplugin.SourceBuiltin) {
			t.Fatalf("built-in provider %s = %+v", plugin.ID(), def)
		}
	}
	// 未编译且没有插件提供的服务商不应作为条目出现。
	// 元数据仍保留在 DNSProviders 中，供 helper 与历史账号使用。
	if !dnsplugin.Has("ovh") {
		if _, exists := byID["ovh"]; exists {
			t.Fatal("uncompiled provider without plugin should not be listed")
		}
		if _, exists := DNSProviderInfo("ovh"); !exists {
			t.Fatal("ovh metadata should still be available for accounts")
		}
	}
}

// TestCatalogMetadataUsesPluginTranslations 确保插件目录的元数据直接使用插件仓库文案，
// 而不是依赖主程序语言包。新增部署目标或字段时主程序无需再同步 meta.* 翻译。
func TestCatalogMetadataUsesPluginTranslations(t *testing.T) {
	for _, locale := range i18n.Supported() {
		pluginLocale := plugini18n.Normalize(string(locale))
		if !plugini18n.Has(pluginLocale) {
			t.Fatalf("host locale %s is not supported by plugin i18n", locale)
		}
	}

	check := func(kind string, def ProviderDef, item catalog.Provider) {
		t.Helper()
		for _, locale := range i18n.Supported() {
			pluginLocale := plugini18n.Normalize(string(locale))
			localized := def.Localized(locale)
			if want := item.LocalizedName(pluginLocale); localized.Label != want {
				t.Errorf("%s %s %s label = %q, want %q", locale, kind, item.Value, localized.Label, want)
			}
			if want := item.LocalizedDesc(pluginLocale); localized.Desc != want {
				t.Errorf("%s %s %s desc = %q, want %q", locale, kind, item.Value, localized.Desc, want)
			}
			fields := append(append([]FieldDef{}, localized.Fields...), localized.DeployFields...)
			catalogFields := append(append([]catalog.Field{}, item.Fields...), item.DeployFields...)
			if len(fields) != len(catalogFields) {
				t.Fatalf("%s %s %s field count = %d, want %d", locale, kind, item.Value, len(fields), len(catalogFields))
			}
			for index, field := range fields {
				source := catalogFields[index]
				if want := source.LocalizedLabel(pluginLocale); field.Label != want {
					t.Errorf("%s %s %s.%s label = %q, want %q", locale, kind, item.Value, field.Key, field.Label, want)
				}
				if want := source.LocalizedPlaceholder(pluginLocale); field.Placeholder != want {
					t.Errorf("%s %s %s.%s placeholder = %q, want %q", locale, kind, item.Value, field.Key, field.Placeholder, want)
				}
				if want := source.LocalizedHelp(pluginLocale); field.Help != want {
					t.Errorf("%s %s %s.%s help = %q, want %q", locale, kind, item.Value, field.Key, field.Help, want)
				}
				for optionIndex, option := range field.Options {
					if want := source.Options[optionIndex].LocalizedLabel(pluginLocale); option.Label != want {
						t.Errorf("%s %s %s.%s option %s label = %q, want %q", locale, kind, item.Value, field.Key, option.Value, option.Label, want)
					}
				}
			}
		}
	}

	for _, item := range catalog.DNSProviders() {
		def, found := DNSProviderInfo(item.Value)
		if !found {
			t.Fatalf("DNS provider %s missing from catalog bridge", item.Value)
		}
		check("dns", def, item)
	}
	for _, item := range catalog.Deployers() {
		def, found := PluginDeployTypeInfo(item.Value)
		if !found {
			t.Fatalf("deploy type %s missing from catalog bridge", item.Value)
		}
		check("deploy", def, item)
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
	if dnsplugin.Has("ovh") {
		t.Skip("ovh is compiled in this build")
	}
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
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
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
		t.Skip("ovh is compiled in this build")
	}
	_, _, err := newDNSProvider("ovh", nil)
	if err == nil || !strings.Contains(err.Error(), "ovh") {
		t.Fatalf("unexpected error: %v", err)
	}
}
