package service

import (
	"reflect"
	"testing"
)

func baotaBatchDef() ProviderDef {
	return ProviderDef{
		Value: "baota",
		DeployFields: []FieldDef{
			{Key: "target", Type: "select", Default: "site"},
			{
				Key:      "sites",
				Type:     "text",
				Multiple: true,
				ShowIf:   &Condition{Key: "target", Values: []string{"site", "docker"}},
			},
		},
	}
}

func TestFilterBatchDeployObjectsMatchesCertificateDomains(t *testing.T) {
	cfg := map[string]string{"sites": "a.example.com,b.example.com"}
	key, objects, err := FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"a.example.com", "www.a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "sites" {
		t.Fatalf("unexpected field key: %q", key)
	}
	if want := []string{"a.example.com"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}
	if cfg["sites"] != "a.example.com" {
		t.Fatalf("shared sites config was not trimmed: %q", cfg["sites"])
	}
}

func TestFilterBatchDeployObjectsFallsBackToCertificateDomains(t *testing.T) {
	cfg := map[string]string{}
	_, objects, err := FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"b.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b.example.com"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}
}

func TestFilterBatchDeployObjectsRejectsMismatchedSite(t *testing.T) {
	cfg := map[string]string{"sites": "b.example.com"}
	if _, _, err := FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"a.example.com"}); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestFilterBatchDeployObjectsWildcardCertificate(t *testing.T) {
	cfg := map[string]string{"sites": "www.a.example.com"}
	_, objects, err := FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"*.a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"www.a.example.com"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}
}

func TestFilterBatchDeployObjectsPanelAndDockerModes(t *testing.T) {
	// 宝塔面板模式不涉及网站，但面板自身只有一个证书对象。
	cfg := map[string]string{"target": "panel", "sites": "a.example.com"}
	_, objects, err := FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"@panel"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("panel mode objects = %v, want %v", objects, want)
	}

	// Docker 网站必须显式填写站点，留空时不做域名回退。
	cfg = map[string]string{"target": "docker"}
	_, objects, err = FilterBatchDeployObjects(baotaBatchDef(), cfg, []string{"a.example.com"})
	if err != nil || len(objects) != 0 {
		t.Fatalf("docker mode without sites should not fall back: %v %v", objects, err)
	}
}

func TestFilterBatchDeployObjectsOnePanel(t *testing.T) {
	def := ProviderDef{
		Value: "1panel",
		DeployFields: []FieldDef{
			{Key: "target", Type: "select", Default: "site"},
			{Key: "website", Type: "text", ShowIf: &Condition{Key: "target", Value: "site"}},
		},
	}
	cfg := map[string]string{"website": "a.example.com"}
	_, objects, err := FilterBatchDeployObjects(def, cfg, []string{"a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.example.com"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}

	cfg = map[string]string{"website": "b.example.com"}
	if _, _, err := FilterBatchDeployObjects(def, cfg, []string{"a.example.com"}); err == nil {
		t.Fatal("expected mismatch error for another website")
	}
}

func TestFilterBatchDeployObjectsKeepsUnverifiableValues(t *testing.T) {
	// AcePanel 允许用数字 ID 选择网站，无法在本地按域名校验，必须保留原值，
	// 由插件解析后再通过站点冲突检查兜底。
	def := ProviderDef{
		Value:        "acepanel",
		DeployFields: []FieldDef{{Key: "websites", Type: "text"}},
	}
	cfg := map[string]string{"websites": "7"}
	_, objects, err := FilterBatchDeployObjects(def, cfg, []string{"a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"7"}; !reflect.DeepEqual(objects, want) {
		t.Fatalf("objects = %v, want %v", objects, want)
	}
	if cfg["websites"] != "7" {
		t.Fatalf("unverifiable value should be preserved: %q", cfg["websites"])
	}
}
