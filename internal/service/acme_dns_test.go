package service

import (
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/catalog"
	"lactssl/internal/i18n"
)

func TestDNSProvidersFromCatalog(t *testing.T) {
	items := catalog.DNSProviders()
	if len(items) != 194 {
		t.Fatalf("catalog providers = %d, want 194", len(items))
	}
	if len(DNSProviders) != len(items) {
		t.Fatalf("bridged providers = %d, want %d", len(DNSProviders), len(items))
	}
	for _, def := range DNSProviders {
		if _, ok := DNSProviderInfo(def.Value); !ok {
			t.Errorf("provider %s not found in DNSProviders", def.Value)
		}
		if len(def.Fields) == 0 {
			t.Errorf("provider %s has no fields", def.Value)
		}
		for _, field := range def.Fields {
			if field.Env == "" {
				t.Errorf("provider %s field %s has no env", def.Value, field.Key)
			}
		}
		for _, key := range []string{def.LabelKey, def.DescKey} {
			if key == "" {
				t.Errorf("provider %s has no label or description key", def.Value)
			}
		}
		// 文案由插件仓库 catalog 直接提供，不再依赖主程序语言包。
		if localized := def.Localized(i18n.ZhCN); localized.Label == "" || localized.Desc == "" {
			t.Errorf("provider %s has no localized metadata", def.Value)
		}
	}
}

func TestBuiltinDNSProviderIDsResolve(t *testing.T) {
	ids := BuiltinDNSProviderIDs()
	if len(ids) != 35 {
		t.Fatalf("builtin DNS providers = %d, want 35", len(ids))
	}
	for _, id := range ids {
		if _, ok := DNSProviderInfo(id); !ok {
			t.Errorf("default provider %s missing from catalog", id)
		}
	}
}
