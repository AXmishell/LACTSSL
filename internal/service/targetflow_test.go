package service

import (
	"fmt"
	"testing"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

func testTargetSite(key, name string, domains ...string) model.TargetSite {
	return model.TargetSite{
		Key:       key,
		AccountID: 1,
		Value:     name,
		Name:      name,
		Domains:   domains,
		Selected:  true,
		Status:    model.SiteStatusReady,
	}
}

func TestRecommendCertStrategy(t *testing.T) {
	locale := i18n.ZhCN
	one := []model.TargetSite{testTargetSite("1|a", "a", "a.example.com")}
	if got := RecommendCertStrategy(one, 100, true, locale); got.Strategy != model.StrategyPerSite {
		t.Fatalf("single site recommendation = %s", got.Strategy)
	}
	sameBase := []model.TargetSite{
		testTargetSite("1|a", "a", "a.example.com"),
		testTargetSite("1|b", "b", "b.example.com"),
	}
	if got := RecommendCertStrategy(sameBase, 100, true, locale); got.Strategy != model.StrategySingle {
		t.Fatalf("same base recommendation = %s", got.Strategy)
	}
	many := []model.TargetSite{
		testTargetSite("1|a", "a", "a.example.com"),
		testTargetSite("1|b", "b", "b.example.com"),
		testTargetSite("1|c", "c", "c.other.net"),
		testTargetSite("1|d", "d", "d.other.net"),
		testTargetSite("1|e", "e", "e.third.org"),
		testTargetSite("1|f", "f", "f.third.org"),
	}
	if got := RecommendCertStrategy(many, 100, true, locale); got.Strategy != model.StrategyPerBaseDomain {
		t.Fatalf("multiple base recommendation = %s", got.Strategy)
	}
	multiBase := []model.TargetSite{
		testTargetSite("1|a", "a", "a.example.com", "a.other.net"),
		testTargetSite("1|b", "b", "b.example.com"),
	}
	if got := RecommendCertStrategy(multiBase, 100, true, locale); got.Strategy != model.StrategyPerSite {
		t.Fatalf("multi-base recommendation = %s", got.Strategy)
	}
}

func TestBuildPerSiteGroups(t *testing.T) {
	sites := []model.TargetSite{
		testTargetSite("1|a", "site-a", "a.example.com", "www.a.example.com"),
		testTargetSite("1|b", "site-b", "b.example.com"),
	}
	groups, _, err := BuildCertGroups(sites, model.CertPolicy{Strategy: model.StrategyPerSite}, nil, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups[0].Domains) != 2 || groups[1].Sites[0].Value != "site-b" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestBuildSingleGroupDeduplicatesDomains(t *testing.T) {
	sites := []model.TargetSite{
		testTargetSite("1|a", "site-a", "a.example.com", "shared.example.com"),
		testTargetSite("1|b", "site-b", "b.example.com", "shared.example.com"),
	}
	groups, _, err := BuildCertGroups(sites, model.CertPolicy{Strategy: model.StrategySingle, MaxDomainsPerCert: 100}, nil, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Domains) != 3 || len(groups[0].Sites) != 2 {
		t.Fatalf("unexpected group: %+v", groups)
	}
}

func TestBuildPerBaseDomainGroups(t *testing.T) {
	sites := []model.TargetSite{
		testTargetSite("1|a", "site-a", "a.example.com"),
		testTargetSite("1|b", "site-b", "b.example.com"),
		testTargetSite("1|c", "site-c", "c.other.net"),
	}
	groups, _, err := BuildCertGroups(sites, model.CertPolicy{Strategy: model.StrategyPerBaseDomain, MaxDomainsPerCert: 100}, nil, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(groups), groups)
	}
	if groups[0].Key != "base:example.com" || len(groups[0].Sites) != 2 {
		t.Fatalf("unexpected first group: %+v", groups[0])
	}
}

func TestBuildCustomGroupsAndUnassigned(t *testing.T) {
	sites := []model.TargetSite{
		testTargetSite("1|a", "site-a", "a.example.com"),
		testTargetSite("1|b", "site-b", "b.example.com"),
		testTargetSite("1|c", "site-c", "c.other.net"),
	}
	overrides := []model.CertGroupOverride{
		{Key: "custom:shared", Label: "shared", Sites: []string{"1|a", "1|b"}},
	}
	groups, warnings, err := BuildCertGroups(sites, model.CertPolicy{Strategy: model.StrategyCustom, MaxDomainsPerCert: 100}, overrides, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups[0].Sites) != 2 || groups[1].Sites[0].Key != "1|c" {
		t.Fatalf("unexpected custom groups: %+v", groups)
	}
	if len(warnings) == 0 {
		t.Fatal("unassigned site should produce a warning")
	}
}

func TestBuildGroupsSplitsWithoutBreakingSiteAtomicity(t *testing.T) {
	a := testTargetSite("1|a", "site-a", makeDomains("a", 40)...)
	b := testTargetSite("1|b", "site-b", makeDomains("b", 40)...)
	c := testTargetSite("1|c", "site-c", makeDomains("c", 40)...)
	sites := []model.TargetSite{a, b, c}
	groups, warnings, err := BuildCertGroups(sites, model.CertPolicy{Strategy: model.StrategySingle, MaxDomainsPerCert: 100}, nil, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected split into 2 groups, got %d: %+v", len(groups), groups)
	}
	for _, group := range groups {
		if len(group.Domains) > 100 {
			t.Fatalf("group exceeds limit: %d", len(group.Domains))
		}
		for _, site := range group.Sites {
			if len(site.Domains) != 40 {
				t.Fatalf("site was split: %+v", site)
			}
		}
	}
	if len(warnings) == 0 {
		t.Fatal("split should produce a warning")
	}
}

func TestBuildGroupsRejectsOversizedSite(t *testing.T) {
	site := testTargetSite("1|a", "site-a", makeDomains("a", 101)...)
	_, _, err := BuildCertGroups([]model.TargetSite{site}, model.CertPolicy{Strategy: model.StrategyPerSite, MaxDomainsPerCert: 100}, nil, true, i18n.ZhCN)
	if err == nil {
		t.Fatal("oversized site should be rejected")
	}
}

func TestBuildGroupsAppliesExtraDomains(t *testing.T) {
	sites := []model.TargetSite{testTargetSite("1|a", "site-a", "a.example.com")}
	policy := model.CertPolicy{
		Strategy:          model.StrategyPerSite,
		MaxDomainsPerCert: 100,
		ExtraDomains:      map[string][]string{"site:1|a": {"www.a.example.com"}},
	}
	groups, _, err := BuildCertGroups(sites, policy, nil, true, i18n.ZhCN)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Domains) != 2 {
		t.Fatalf("extra domain not applied: %+v", groups)
	}
}

func TestCertificateCoversDomains(t *testing.T) {
	if !certificateCoversDomains([]string{"*.example.com", "example.com"}, []string{"example.com", "a.example.com"}) {
		t.Fatal("wildcard should cover subdomain")
	}
	if certificateCoversDomains([]string{"*.example.com"}, []string{"example.com"}) {
		t.Fatal("wildcard must not cover the apex without an explicit entry")
	}
	if certificateCoversDomains([]string{"a.example.com"}, []string{"b.example.com"}) {
		t.Fatal("different domains must not match")
	}
}

func TestTargetFlowUsesCommand(t *testing.T) {
	cfg := model.TargetFlowConfig{Sources: []model.TargetSource{{DeployConfig: map[string]string{"reload_command": " systemctl reload nginx "}}}}
	if !TargetFlowUsesCommand(cfg) {
		t.Fatal("reload command should require deploy:command")
	}
}

func makeDomains(prefix string, count int) []string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, fmt.Sprintf("%s-%d.example.com", prefix, i))
	}
	return out
}
