package api

import (
	"testing"

	"lactssl/internal/plugin/meta"
	"lactssl/internal/service"
)

// TestPluginMetadataSurvivesDisabledHelper 验证 helper 被停用后，
// 历史账号仍能从 plugin_metadata 缓存获得字段定义并正确脱敏。
func TestPluginMetadataSurvivesDisabledHelper(t *testing.T) {
	t.Cleanup(pluginmeta.Reset)
	s, _, _ := newPluginTestServer(t)
	s.DiscoverPlugins()

	rows, err := s.Store.ListPluginMetadata(pluginKindDNS)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == "alphadns" {
			found = true
			if row.Manifest == "" {
				t.Fatal("alphadns metadata is empty")
			}
		}
	}
	if !found {
		t.Fatal("alphadns metadata was not cached")
	}

	if err := s.updatePluginFileEnabled(pluginKindDNS, "helper", false); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()

	def, ok := service.DNSProviderInfo("alphadns")
	if !ok {
		t.Fatal("metadata should survive disabled helper")
	}
	if def.Available {
		t.Fatalf("disabled helper metadata should not be available: %+v", def)
	}
	if len(def.Fields) != 1 || def.Fields[0].Key != "token" || !def.Fields[0].Secret {
		t.Fatalf("cached fields are incomplete: %+v", def.Fields)
	}
}
