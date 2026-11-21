package service

import (
	"testing"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/model"
	dnsplugin "lactssl/internal/plugin/dns"
)

type routedFakeDNSPlugin struct {
	providers map[string]*fakeChallengeProvider
}

func (p routedFakeDNSPlugin) ID() string               { return "route-fake" }
func (p routedFakeDNSPlugin) Aliases() []string        { return nil }
func (p routedFakeDNSPlugin) Source() dnsplugin.Source { return dnsplugin.SourceExec }
func (p routedFakeDNSPlugin) New(cfg map[string]string) (challenge.Provider, error) {
	return p.providers[cfg["key"]], nil
}

func TestNewRoutedDNSProviderUsesAccountProviders(t *testing.T) {
	first := &fakeChallengeProvider{}
	second := &fakeChallengeProvider{}
	if err := dnsplugin.RegisterExternal(routedFakeDNSPlugin{providers: map[string]*fakeChallengeProvider{
		"first":  first,
		"second": second,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	accounts := map[int64]*model.DNSAccount{
		1: {ID: 1, Provider: "route-fake", Config: map[string]string{"key": "first"}},
		2: {ID: 2, Provider: "route-fake", Config: map[string]string{"key": "second"}},
	}
	routes := []model.DNSRoute{
		{Domain: "a.example.com", DNSAccountID: 1},
		{Domain: "b.other.net", DNSAccountID: 2},
	}
	provider, undo, err := newRoutedDNSProvider(routes, accounts)
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	if err := provider.Present("a.example.com", "token-a", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("b.other.net", "token-b", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if first.presentCalls != 1 || second.presentCalls != 1 {
		t.Fatalf("providers were not routed by account: %d %d", first.presentCalls, second.presentCalls)
	}
}

func TestResolveTaskDNSRoutesUsesSnapshotAndBindings(t *testing.T) {
	manager := newTestManager(t)
	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{first, second} {
		if err := manager.Store.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Store.ReplaceDNSZoneBindings(second.ID, []model.DNSZoneBinding{
		{DNSAccountID: second.ID, Zone: "other.net", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Domains: []string{"a.example.com", "b.other.net"},
		DNSRoutes: []model.DNSRoute{
			{Domain: "a.example.com", DNSAccountID: first.ID},
		},
	}
	routes, err := manager.resolveTaskDNSRoutes(task, model.PipelineConfig{}, task.Domains)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].DNSAccountID != first.ID || routes[1].DNSAccountID != second.ID {
		t.Fatalf("snapshot and binding fallback failed: %+v", routes)
	}
}

func TestRoutedDNSProviderPresentsAndCleansUpByDomain(t *testing.T) {
	first := &fakeChallengeProvider{}
	second := &fakeChallengeProvider{}
	provider := &routedDNSProvider{
		routes: map[string]model.DNSRoute{
			"a.example.com": {Domain: "a.example.com", Zone: "example.com", DNSAccountID: 1},
			"b.other.net":   {Domain: "b.other.net", Zone: "other.net", DNSAccountID: 2},
		},
		providers: map[int64]challenge.Provider{
			1: first,
			2: second,
		},
		used: make(map[string]int64),
	}

	if err := provider.Present("a.example.com", "token-a", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("b.other.net", "token-b", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if first.presentCalls != 1 || second.presentCalls != 1 {
		t.Fatalf("unexpected present calls: %d %d", first.presentCalls, second.presentCalls)
	}

	if err := provider.CleanUp("a.example.com", "token-a", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if err := provider.CleanUp("b.other.net", "token-b", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if first.cleanupCalls != 1 || second.cleanupCalls != 1 {
		t.Fatalf("unexpected cleanup calls: %d %d", first.cleanupCalls, second.cleanupCalls)
	}
}

func TestRoutedDNSProviderRejectsUnroutedDomain(t *testing.T) {
	first := &fakeChallengeProvider{}
	second := &fakeChallengeProvider{}
	provider := &routedDNSProvider{
		routes: map[string]model.DNSRoute{
			"a.example.com": {Domain: "a.example.com", DNSAccountID: 1},
		},
		providers: map[int64]challenge.Provider{
			1: first,
			2: second,
		},
		used: make(map[string]int64),
	}
	if err := provider.Present("other.net", "token", "key-auth"); err == nil {
		t.Fatal("unrouted domain should fail when multiple accounts exist")
	}
}

func TestRoutedDNSProviderFallsBackForSingleAccount(t *testing.T) {
	first := &fakeChallengeProvider{}
	provider := &routedDNSProvider{
		routes:    map[string]model.DNSRoute{},
		providers: map[int64]challenge.Provider{1: first},
		used:      make(map[string]int64),
	}
	if err := provider.Present("example.com", "token", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if first.presentCalls != 1 {
		t.Fatalf("single account should handle all domains: %d", first.presentCalls)
	}
}
