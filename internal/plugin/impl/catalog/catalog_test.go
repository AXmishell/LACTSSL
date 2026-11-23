package catalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
)

func TestCounts(t *testing.T) {
	if got := len(DNSProviders()); got != 194 {
		t.Fatalf("dns providers = %d, want 194", got)
	}
	if got := len(Deployers()); got != 46 {
		t.Fatalf("deployers = %d, want 46", got)
	}
	if got := len(DeprecatedDeployTypes()); got != 21 {
		t.Fatalf("deprecated deploy types = %d, want 21", got)
	}
}

func TestDeployMergeGroups(t *testing.T) {
	groups := map[string]struct {
		field  string
		values []string
	}{
		"safeline":   {"target", []string{"waf", "panel", "portal"}},
		"baota":      {"target", []string{"site", "panel", "docker"}},
		"1panel":     {"target", []string{"site", "panel"}},
		"acepanel":   {"target", []string{"site", "panel"}},
		"qiniu":      {"domain_type", []string{"cdn", "kodo"}},
		"tencent":    {"target", []string{"cdn", "cos", "teo", "waf"}},
		"aliyun":     {"target", []string{"cdn", "dcdn", "esa", "oss", "waf"}},
		"volcengine": {"target", []string{"cdn", "dcdn"}},
	}
	legacy := map[string]string{
		"1panel_site": "1panel", "1panel_panel": "1panel",
		"baota_site": "baota", "baota_panel": "baota", "baota_docker": "baota",
		"qiniu_cdn": "qiniu", "qiniu_kodo": "qiniu",
		"safeline_panel": "safeline", "safeline_portal": "safeline",
		"acepanel_panel": "acepanel",
		"tencent_cdn":    "tencent", "tencent_cos": "tencent", "tencent_teo": "tencent", "tencent_waf": "tencent",
		"aliyun_cdn": "aliyun", "aliyun_dcdn": "aliyun", "aliyun_esa": "aliyun", "aliyun_oss": "aliyun", "aliyun_waf": "aliyun",
		"volcengine_cdn": "volcengine", "volcengine_dcdn": "volcengine",
	}
	deprecated := DeprecatedDeployTypes()
	for old, canonical := range legacy {
		if deprecated[old] != canonical {
			t.Errorf("deprecated %s = %q, want %q", old, deprecated[old], canonical)
		}
	}
	for id, group := range groups {
		def, ok := LookupDeployer(id)
		if !ok {
			t.Errorf("canonical deployer %s missing", id)
			continue
		}
		if def.Deprecated {
			t.Errorf("canonical deployer %s should not be deprecated", id)
		}
		field, ok := deployField(def, group.field)
		if !ok {
			t.Errorf("canonical deployer %s has no %s field", id, group.field)
			continue
		}
		got := make([]string, 0, len(field.Options))
		for _, option := range field.Options {
			got = append(got, option.Value)
		}
		if strings.Join(got, ",") != strings.Join(group.values, ",") {
			t.Errorf("%s %s options = %v, want %v", id, group.field, got, group.values)
		}
	}
	if _, ok := deprecated["btwaf"]; ok {
		t.Fatal("btwaf must stay independent from baota")
	}
}

func TestCloudRegionIsDeployField(t *testing.T) {
	for _, id := range []string{"aliyun", "volcengine"} {
		def, ok := LookupDeployer(id)
		if !ok {
			t.Errorf("deployer %s missing", id)
			continue
		}
		for _, field := range def.Fields {
			if field.Key == "region" {
				t.Errorf("%s: region must not be an auth field", id)
			}
		}
		if _, ok := deployField(def, "region"); !ok {
			t.Errorf("%s: region must be a deploy field", id)
		}
	}
	def, _ := LookupDeployer("aliyun")
	region, _ := deployField(def, "region")
	if region.ShowIf == nil || region.ShowIf.Key != "target" ||
		len(region.ShowIf.Values) != 2 || region.ShowIf.Values[0] != "oss" || region.ShowIf.Values[1] != "waf" {
		t.Errorf("aliyun region must only show for oss/waf: %+v", region.ShowIf)
	}
	domains, _ := deployField(def, "domains")
	if domains.ShowIf == nil || len(domains.ShowIf.Values) != 4 {
		t.Errorf("aliyun domains must only show for cdn/dcdn/oss/waf: %+v", domains.ShowIf)
	}
	certName, _ := deployField(def, "cert_name")
	if certName.ShowIf == nil || len(certName.ShowIf.Values) != 2 ||
		certName.ShowIf.Values[0] != "cdn" || certName.ShowIf.Values[1] != "waf" {
		t.Errorf("aliyun cert_name must only show for cdn/waf: %+v", certName.ShowIf)
	}

	baota, _ := LookupDeployer("baota")
	sites, _ := deployField(baota, "sites")
	if sites.ShowIf == nil || len(sites.ShowIf.Values) != 2 ||
		sites.ShowIf.Values[0] != "site" || sites.ShowIf.Values[1] != "docker" {
		t.Errorf("baota sites must only show for site/docker: %+v", sites.ShowIf)
	}
}

func TestDeployMigrations(t *testing.T) {
	want := map[string]DeployMigration{
		"1panel_panel":   {Provider: "1panel", Config: map[string]string{"target": "panel"}},
		"1panel_site":    {Provider: "1panel", Config: map[string]string{"target": "site"}},
		"baota_panel":    {Provider: "baota", Config: map[string]string{"target": "panel"}},
		"baota_site":     {Provider: "baota", Config: map[string]string{"target": "site"}},
		"baota_docker":   {Provider: "baota", Config: map[string]string{"target": "docker"}},
		"qiniu_cdn":      {Provider: "qiniu", Config: map[string]string{"domain_type": "cdn"}},
		"qiniu_kodo":     {Provider: "qiniu", Config: map[string]string{"domain_type": "kodo"}},
		"safeline_panel": {Provider: "safeline", Config: map[string]string{"target": "panel"}},
		"safeline_portal": {
			Provider: "safeline", Config: map[string]string{"target": "portal"},
		},
		"acepanel_panel":  {Provider: "acepanel", Config: map[string]string{"target": "panel"}},
		"tencent_cdn":     {Provider: "tencent", Config: map[string]string{"target": "cdn"}},
		"tencent_cos":     {Provider: "tencent", Config: map[string]string{"target": "cos"}},
		"tencent_teo":     {Provider: "tencent", Config: map[string]string{"target": "teo"}},
		"tencent_waf":     {Provider: "tencent", Config: map[string]string{"target": "waf"}},
		"aliyun_cdn":      {Provider: "aliyun", Config: map[string]string{"target": "cdn"}},
		"aliyun_dcdn":     {Provider: "aliyun", Config: map[string]string{"target": "dcdn"}},
		"aliyun_esa":      {Provider: "aliyun", Config: map[string]string{"target": "esa"}},
		"aliyun_oss":      {Provider: "aliyun", Config: map[string]string{"target": "oss"}},
		"aliyun_waf":      {Provider: "aliyun", Config: map[string]string{"target": "waf"}},
		"volcengine_cdn":  {Provider: "volcengine", Config: map[string]string{"target": "cdn"}},
		"volcengine_dcdn": {Provider: "volcengine", Config: map[string]string{"target": "dcdn"}},
	}
	got := DeployMigrations()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deploy migrations mismatch:\n got %#v\nwant %#v", got, want)
	}

	deprecated := DeprecatedDeployTypes()
	if len(got) != len(deprecated) {
		t.Fatalf("migrations = %d, deprecated = %d", len(got), len(deprecated))
	}
	for old, migration := range got {
		canonical, ok := deprecated[old]
		if !ok {
			t.Errorf("migration %s has no deprecated entry", old)
			continue
		}
		if migration.Provider != canonical {
			t.Errorf("migration %s provider = %q, want %q", old, migration.Provider, canonical)
		}
		def, ok := LookupDeployer(migration.Provider)
		if !ok {
			t.Errorf("migration %s target deployer %s missing", old, migration.Provider)
			continue
		}
		if def.Deprecated {
			t.Errorf("migration %s target deployer %s is deprecated", old, migration.Provider)
		}
		for key, value := range migration.Config {
			field, ok := deployField(def, key)
			if !ok {
				t.Errorf("migration %s config field %s missing on %s", old, key, migration.Provider)
				continue
			}
			if !fieldHasOption(field, value) {
				t.Errorf("migration %s config %s=%q is not a valid option", old, key, value)
			}
		}
	}
}

func deployField(def Provider, key string) (Field, bool) {
	for _, field := range def.DeployFields {
		if field.Key == key {
			return field, true
		}
	}
	return Field{}, false
}

func fieldHasOption(field Field, value string) bool {
	for _, option := range field.Options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func TestReturnsDeepCopies(t *testing.T) {
	providers := DNSProviders()
	if len(providers) == 0 || len(providers[0].Fields) == 0 {
		t.Fatal("no DNS provider fields")
	}
	providers[0].Fields[0].Label = "mutated"
	if got := DNSProviders()[0].Fields[0].Label; got == "mutated" {
		t.Fatal("DNSProviders returned shared field slice")
	}
	def, ok := LookupDNS(providers[0].Value)
	if !ok || len(def.Fields) == 0 {
		t.Fatal("DNS provider not found")
	}
	def.Fields[0].Label = "mutated"
	if got, _ := LookupDNS(providers[0].Value); got.Fields[0].Label == "mutated" {
		t.Fatal("LookupDNS returned shared field slice")
	}
}

func TestLookupNormalizesID(t *testing.T) {
	if _, ok := LookupDNS(" Cloudflare "); !ok {
		t.Fatal("lookup should normalize ID")
	}
	if _, ok := LookupDeployer("QINIU"); !ok {
		t.Fatal("lookup should normalize deployer ID")
	}
}

func TestEnvForCloudflareTokenWins(t *testing.T) {
	def, ok := LookupDNS("cloudflare")
	if !ok {
		t.Fatal("cloudflare metadata missing")
	}
	envs := EnvForProvider(def, map[string]string{"api_token": "token", "email": "a@b.c", "api_key": "key"})
	if envs["CLOUDFLARE_DNS_API_TOKEN"] != "token" {
		t.Fatalf("token env = %q", envs["CLOUDFLARE_DNS_API_TOKEN"])
	}
	if _, ok := envs["CLOUDFLARE_EMAIL"]; ok {
		t.Fatal("email should be removed when token is set")
	}
	if _, ok := envs["CLOUDFLARE_API_KEY"]; ok {
		t.Fatal("api key should be removed when token is set")
	}
}

func TestDeployCatalogI18n(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhCN, i18n.EnUS} {
		for _, provider := range Deployers() {
			keys := []string{provider.LabelKey, provider.DescKey}
			fields := append(append([]Field{}, provider.Fields...), provider.DeployFields...)
			for _, field := range fields {
				keys = append(keys, field.LabelKey, field.HelpKey, field.PlaceholderKey)
				for _, option := range field.Options {
					keys = append(keys, option.LabelKey)
				}
			}
			for _, key := range keys {
				if key == "" {
					continue
				}
				if text := i18n.T(locale, key, nil); text == key {
					t.Errorf("%s: missing translation %q", locale, key)
				}
			}
		}
	}
}

func TestDNSProviderMetadataCount(t *testing.T) {
	// catalog 保留 194 个历史 DNS ID，供 v5 legacy/兼容映射使用。
	if got := len(DNSProviders()); got != 194 {
		t.Fatalf("dns providers = %d, want 194", got)
	}
}
