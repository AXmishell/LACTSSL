package service

import (
	"testing"

	"lactssl/internal/model"
)

func TestNormalizeDNSZone(t *testing.T) {
	cases := map[string]string{
		" Example.COM. ":  "example.com",
		"*.example.com":   "example.com",
		"sub.example.com": "sub.example.com",
	}
	for input, want := range cases {
		got, err := NormalizeDNSZone(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeDNSZone(%q)=%q,%v want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "localhost", "bad_domain", "https://example.com"} {
		if _, err := NormalizeDNSZone(input); err == nil {
			t.Fatalf("NormalizeDNSZone(%q) should fail", input)
		}
	}
}

func TestResolveDNSAccountForDomainsUsesLongestZone(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 2, Zone: "sub.example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	res, err := ResolveDNSAccountForDomains([]string{"a.sub.example.com", "sub.example.com"}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if res.AccountID != 2 || res.Zone != "sub.example.com" {
		t.Fatalf("unexpected resolution: %+v", res)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("unexpected matches: %+v", res.Matches)
	}
}

func TestResolveDNSAccountForDomainsExactMode(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchExact, Enabled: true},
	}
	if _, err := ResolveDNSAccountForDomains([]string{"www.example.com"}, bindings); err == nil {
		t.Fatal("exact mode should not match subdomains")
	}
	if res, err := ResolveDNSAccountForDomains([]string{"example.com"}, bindings); err != nil || res.AccountID != 1 {
		t.Fatalf("exact zone should match: %+v %v", res, err)
	}
}

func TestResolveDNSAccountForDomainsWildcardSAN(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 7, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	res, err := ResolveDNSAccountForDomains([]string{"*.example.com"}, bindings)
	if err != nil || res.AccountID != 7 {
		t.Fatalf("wildcard SAN should be resolved by base zone: %+v %v", res, err)
	}
}

func TestResolveDNSAccountForDomainsUnresolved(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if _, err := ResolveDNSAccountForDomains([]string{"other.net"}, bindings); err == nil {
		t.Fatal("unbound domain should fail")
	}
	// 被禁用的绑定不参与解析。
	bindings[0].Enabled = false
	if _, err := ResolveDNSAccountForDomains([]string{"example.com"}, bindings); err == nil {
		t.Fatal("disabled binding should not be used")
	}
}

func TestResolveDNSAccountForDomainsAmbiguousAcrossAccounts(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 2, Zone: "sub.example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if _, err := ResolveDNSAccountForDomains([]string{"www.example.com", "a.sub.example.com"}, bindings); err == nil {
		t.Fatal("domains spanning multiple accounts should fail")
	}
}

func TestResolveDNSAccountForDomainsTieIsAmbiguous(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 2, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if _, err := ResolveDNSAccountForDomains([]string{"www.example.com"}, bindings); err == nil {
		t.Fatal("same zone on two accounts should be ambiguous")
	}
}

func TestResolveDNSAccountForDomainsTieIsAmbiguousRegardlessOfOrder(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 2, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if _, err := ResolveDNSAccountForDomains([]string{"www.example.com"}, bindings); err == nil {
		t.Fatal("same zone on two accounts should be ambiguous in any order")
	}
}

func TestResolveDNSRoutesForDomainsAllowsMultipleAccounts(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 2, Zone: "other.net", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	routes, err := ResolveDNSRoutesForDomains([]string{"a.example.com", "b.other.net"}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("unexpected routes: %+v", routes)
	}
	if routes[0].DNSAccountID != 1 || routes[1].DNSAccountID != 2 {
		t.Fatalf("domains should route to different accounts: %+v", routes)
	}
	if !DNSRoutesCoverDomains(routes, []string{"a.example.com", "b.other.net"}) {
		t.Fatal("routes should cover all domains")
	}
	if DNSRoutesCoverDomains(routes, []string{"a.example.com", "c.other.net"}) {
		t.Fatal("missing domain should not be covered")
	}
}

func TestResolveDNSRoutesForDomainsKeepsPerDomainAmbiguity(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
		{DNSAccountID: 2, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if _, err := ResolveDNSRoutesForDomains([]string{"www.example.com"}, bindings); err == nil {
		t.Fatal("same domain on two equal-rank accounts should fail")
	}
}

func TestDNSRoutesHashIsStable(t *testing.T) {
	first := []model.DNSRoute{
		{Domain: "a.example.com", Zone: "example.com", DNSAccountID: 1},
		{Domain: "b.other.net", Zone: "other.net", DNSAccountID: 2},
	}
	second := []model.DNSRoute{
		{Domain: "b.other.net", Zone: "other.net", DNSAccountID: 2},
		{Domain: "a.example.com", Zone: "example.com", DNSAccountID: 1},
	}
	if DNSRoutesHash(first) == "" || DNSRoutesHash(first) != DNSRoutesHash(second) {
		t.Fatalf("route hash should be stable: %s %s", DNSRoutesHash(first), DNSRoutesHash(second))
	}
	second[0].DNSAccountID = 3
	if DNSRoutesHash(first) == DNSRoutesHash(second) {
		t.Fatal("route hash should change when account changes")
	}
}

func TestMatchDNSAccountForDomains(t *testing.T) {
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: 1, Zone: "example.com", MatchMode: DNSZoneMatchSuffix, Enabled: true},
	}
	if zone := MatchDNSAccountForDomains([]string{"a.example.com"}, bindings, 1); zone != "example.com" {
		t.Fatalf("unexpected zone: %q", zone)
	}
	if zone := MatchDNSAccountForDomains([]string{"a.example.com"}, bindings, 2); zone != "" {
		t.Fatalf("wrong account should not match: %q", zone)
	}
}

func TestZoneFromVerifyResult(t *testing.T) {
	result := VerifyResult{Checks: []VerifyCheck{
		{Key: "auth", Status: VerifyStatusOK},
		{Key: "zone", Status: VerifyStatusOK, Message: "Example.COM."},
	}}
	if zone := ZoneFromVerifyResult(result); zone != "example.com" {
		t.Fatalf("unexpected zone: %q", zone)
	}
	if zone := ZoneFromVerifyResult(VerifyResult{}); zone != "" {
		t.Fatalf("empty result should not yield zone: %q", zone)
	}
}
