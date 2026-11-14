package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
