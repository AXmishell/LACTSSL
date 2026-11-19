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

func TestDeployObjectFieldInfo(t *testing.T) {
	key, multiple, fallback, ok := DeployObjectFieldInfo(baotaBatchDef(), map[string]string{})
	if !ok || key != "sites" || !multiple || !fallback {
		t.Fatalf("unexpected field info: %q %v %v %v", key, multiple, fallback, ok)
	}

	// 宝塔 Docker 网站不回退到证书域名。
	_, _, fallback, ok = DeployObjectFieldInfo(baotaBatchDef(), map[string]string{"target": "docker"})
	if !ok || fallback {
		t.Fatalf("docker mode should not fallback: %v %v", fallback, ok)
	}

	// 面板自身证书不涉及网站字段。
	if _, _, _, ok := DeployObjectFieldInfo(baotaBatchDef(), map[string]string{"target": "panel"}); ok {
		t.Fatal("panel mode should not expose the sites field")
	}
}

func TestDeployObjectIsSingleton(t *testing.T) {
	if !DeployObjectIsSingleton(baotaBatchDef(), map[string]string{"target": "panel"}) {
		t.Fatal("panel mode should be treated as a singleton target")
	}
	if DeployObjectIsSingleton(baotaBatchDef(), map[string]string{"target": "site"}) {
		t.Fatal("site mode should not be treated as a singleton target")
	}
}

func TestMatchDeployObjectsUsesWebsiteDomains(t *testing.T) {
	objects := []DeployObject{
		{Value: "site-a", Domains: []string{"a.example.com", "www.a.example.com"}},
		{Value: "site-b", Name: "b.example.com"},
		{Value: "site-c", Domains: []string{"c.example.com"}},
	}
	matched := MatchDeployObjects(objects, nil, []string{"www.a.example.com"})
	if got := DeployObjectValues(matched); !reflect.DeepEqual(got, []string{"site-a"}) {
		t.Fatalf("matched = %v", got)
	}
	// 显式选择只能缩小范围，不能突破证书域名限制。
	matched = MatchDeployObjects(objects, []string{"site-a", "site-c"}, []string{"c.example.com"})
	if got := DeployObjectValues(matched); !reflect.DeepEqual(got, []string{"site-c"}) {
		t.Fatalf("selected matched = %v", got)
	}
}

func TestMatchDeployObjectsWildcardCertificate(t *testing.T) {
	objects := []DeployObject{{Value: "www.a.example.com", Domains: []string{"www.a.example.com"}}}
	matched := MatchDeployObjects(objects, nil, []string{"*.a.example.com"})
	if len(matched) != 1 {
		t.Fatalf("wildcard certificate should match subdomain: %+v", matched)
	}
}

func TestMatchDeployObjectsSelectedIdentifier(t *testing.T) {
	objects := []DeployObject{{Value: "example.com", Domains: []string{"example.com"}, Identifiers: []string{"7"}}}
	matched := MatchDeployObjects(objects, []string{"7"}, []string{"example.com"})
	if len(matched) != 1 {
		t.Fatalf("numeric website id should match explicit selection: %+v", matched)
	}
}

func TestForceDeployValuesUsesConfiguredOrCertificateDomains(t *testing.T) {
	cfg := map[string]string{"sites": "custom-site"}
	if got := ForceDeployValues(cfg, "sites", []string{"a.example.com"}); !reflect.DeepEqual(got, []string{"custom-site"}) {
		t.Fatalf("configured values = %v", got)
	}
	cfg = map[string]string{}
	if got := ForceDeployValues(cfg, "sites", []string{"a.example.com", "b.example.com"}); !reflect.DeepEqual(got, []string{"a.example.com", "b.example.com"}) {
		t.Fatalf("fallback values = %v", got)
	}
}
