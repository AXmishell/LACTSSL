package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"lactssl/internal/plugin/deploy/providers/panels"
)

func TestMain(m *testing.M) {
	panels.RegisterBaota()
	panels.RegisterOnePanel()
	panels.RegisterSafeline()
	os.Exit(m.Run())
}

func loadLegacy(t *testing.T, name string) protocol.DeployManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var manifest protocol.DeployManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func stripLocalizedFields(item protocol.Deployer) protocol.Deployer {
	item.NameI18n = nil
	item.DescriptionI18n = nil
	item.Fields = stripLocalizedManifestFields(item.Fields)
	item.DeployFields = stripLocalizedManifestFields(item.DeployFields)
	return item
}

func stripLocalizedManifestFields(fields []protocol.DeployField) []protocol.DeployField {
	if fields == nil {
		return nil
	}
	out := make([]protocol.DeployField, len(fields))
	for index, field := range fields {
		field.LabelI18n = nil
		field.PlaceholderI18n = nil
		field.HelpI18n = nil
		if field.Options != nil {
			options := make([]protocol.DeployOption, len(field.Options))
			for optionIndex, option := range field.Options {
				option.LabelI18n = nil
				options[optionIndex] = option
			}
			field.Options = options
		}
		out[index] = field
	}
	return out
}

func assertLegacyEntries(t *testing.T, got, want protocol.DeployManifest) {
	t.Helper()
	byID := make(map[string]protocol.Deployer, len(got.Deployers))
	for _, item := range got.Deployers {
		byID[item.ID] = stripLocalizedFields(item)
	}
	for _, item := range want.Deployers {
		item = stripLocalizedFields(item)
		actual, ok := byID[item.ID]
		if !ok {
			t.Fatalf("legacy deployer %s is missing", item.ID)
		}
		if !reflect.DeepEqual(actual, item) {
			t.Fatalf("legacy deployer %s changed:\n got %+v\nwant %+v", item.ID, actual, item)
		}
	}
}

func TestManifestMatchesLegacyHost(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, "")
	got := buildManifest()
	got.Version = ""
	got.Name = ""

	want := loadLegacy(t, "legacy-deploy-manifest.json")
	want.Version = ""
	want.Name = ""

	assertLegacyEntries(t, got, want)
	if len(got.Deployers) != 40 {
		t.Fatalf("deployers = %d, want 40", len(got.Deployers))
	}
}

func TestManifestMergedDeployers(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers)
	got := buildManifest()
	got.Version = ""
	got.Name = ""

	want := loadLegacy(t, "legacy-deploy-manifest-merged.json")
	want.Version = ""
	want.Name = ""

	assertLegacyEntries(t, got, want)
	if len(got.Deployers) != 25 {
		t.Fatalf("deployers = %d, want 25", len(got.Deployers))
	}
}

func TestManifestDynamicOptionsFeature(t *testing.T) {
	findSites := func(manifest protocol.DeployManifest, id string) *protocol.DeployField {
		for index := range manifest.Deployers {
			if manifest.Deployers[index].ID != id {
				continue
			}
			for fieldIndex := range manifest.Deployers[index].DeployFields {
				field := &manifest.Deployers[index].DeployFields[fieldIndex]
				if field.Key == "sites" {
					return field
				}
			}
		}
		return nil
	}

	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers)
	legacy := findSites(buildManifest(), "baota")
	if legacy == nil {
		t.Fatal("baota sites field is missing")
	}
	if legacy.OptionsSource != "" || legacy.Multiple {
		t.Fatalf("dynamic options leaked into legacy manifest: %+v", legacy)
	}

	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers+","+protocol.FeatureDynamicOptions)
	dynamic := findSites(buildManifest(), "baota")
	if dynamic == nil {
		t.Fatal("baota sites field is missing")
	}
	if dynamic.OptionsSource != "baota_sites" || !dynamic.Multiple {
		t.Fatalf("unexpected dynamic options metadata: %+v", dynamic)
	}
}

func TestManifestPlanFeature(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers+","+protocol.FeaturePlan)
	manifest := buildManifest()
	wantPlan := map[string]bool{"baota": true, "baota_site": true, "baota_docker": true, "1panel": true, "1panel_site": true, "acepanel": true}
	found := 0
	for _, item := range manifest.Deployers {
		if !wantPlan[item.ID] {
			continue
		}
		found++
		hasPlan := false
		for _, capability := range item.Capabilities {
			if capability == protocol.FeaturePlan {
				hasPlan = true
			}
		}
		if !hasPlan {
			t.Fatalf("deployer %s should advertise plan-v1: %+v", item.ID, item.Capabilities)
		}
	}
	if found == 0 {
		t.Fatal("no plannable deployer found")
	}
}

func TestManifestContainsLocalizedMetadata(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers)
	got := buildManifest()
	var smtp *protocol.Deployer
	for index := range got.Deployers {
		if got.Deployers[index].ID == "smtp" {
			smtp = &got.Deployers[index]
			break
		}
	}
	if smtp == nil {
		t.Fatal("smtp deployer is missing")
	}
	if smtp.NameI18n["zh-CN"] != "邮件发送证书" || smtp.NameI18n["en-US"] != "Email Certificate" {
		t.Fatalf("unexpected smtp name translations: %#v", smtp.NameI18n)
	}
	if smtp.DescriptionI18n["en-US"] == "" {
		t.Fatalf("missing smtp description translations: %#v", smtp.DescriptionI18n)
	}
	for _, field := range append(append([]protocol.DeployField{}, smtp.Fields...), smtp.DeployFields...) {
		if field.LabelI18n["zh-CN"] == "" || field.LabelI18n["en-US"] == "" {
			t.Fatalf("field %s missing label translations: %#v", field.Key, field.LabelI18n)
		}
		for _, option := range field.Options {
			if option.LabelI18n["zh-CN"] == "" || option.LabelI18n["en-US"] == "" {
				t.Fatalf("option %s missing label translations: %#v", option.Value, option.LabelI18n)
			}
		}
	}
}

// TestManifestVerifyCapability 验证 verify-v1 能力位只按部署目标输出。
func TestManifestVerifyCapability(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, protocol.FeatureMergedDeployers+","+protocol.FeatureVerify)
	capabilities := map[string][]string{}
	for _, deployer := range buildManifest().Deployers {
		capabilities[deployer.ID] = deployer.Capabilities
	}
	for _, id := range []string{"baota", "1panel", "safeline", "tencent", "aliyun", "huawei_cdn", "qiniu", "ftp", "smtp", "acepanel"} {
		if len(capabilities[id]) == 0 {
			t.Fatalf("%s 应声明 verify-v1", id)
		}
	}
	if len(capabilities["webhook"]) != 0 {
		t.Fatalf("未实现的 webhook 不应声明能力: %v", capabilities["webhook"])
	}
}
