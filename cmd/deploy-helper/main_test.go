package main

import (
	"testing"

	"lactssl/internal/deployplugin"
	"lactssl/internal/service"
)

// TestManifestMetadataMatchesProviders 确保 helper 注册的实现与插件类型元数据一致，
// 避免 manifest 漏报目标或元数据残留已删除的实现。
func TestManifestMetadataMatchesProviders(t *testing.T) {
	for _, plugin := range deployplugin.ListBySource(deployplugin.SourceBuiltin) {
		if _, ok := service.PluginDeployTypeInfo(plugin.ID()); !ok {
			t.Errorf("provider %s has no metadata", plugin.ID())
		}
	}
	for _, def := range service.PluginDeployTypes {
		plugin, ok := deployplugin.Lookup(def.Value)
		if !ok || plugin.Source() != deployplugin.SourceBuiltin {
			t.Errorf("plugin deploy type %s has no implementation", def.Value)
		}
	}
}

func TestBuildManifestMergedDeployers(t *testing.T) {
	t.Setenv("LACTSSL_DEPLOY_PLUGIN_FEATURES", deployplugin.FeatureMergedDeployers)
	ids := map[string]bool{}
	for _, deployer := range buildManifest().Deployers {
		ids[deployer.ID] = true
	}
	for _, id := range []string{"qiniu", "baota", "1panel"} {
		if !ids[id] {
			t.Errorf("merged manifest missing %s", id)
		}
	}
	for _, id := range []string{"qiniu_cdn", "qiniu_kodo", "baota_site", "baota_panel", "1panel_site", "1panel_panel"} {
		if ids[id] {
			t.Errorf("merged manifest should not contain legacy id %s", id)
		}
	}
}

func TestBuildManifestLegacyDeployers(t *testing.T) {
	t.Setenv("LACTSSL_DEPLOY_PLUGIN_FEATURES", "")
	ids := map[string]bool{}
	for _, deployer := range buildManifest().Deployers {
		ids[deployer.ID] = true
	}
	for _, id := range []string{"qiniu_cdn", "qiniu_kodo", "baota_site", "baota_panel", "1panel_site", "1panel_panel"} {
		if !ids[id] {
			t.Errorf("legacy manifest missing %s", id)
		}
	}
	for _, id := range []string{"qiniu", "baota", "1panel"} {
		if ids[id] {
			t.Errorf("legacy manifest should not contain merged id %s", id)
		}
	}
}
