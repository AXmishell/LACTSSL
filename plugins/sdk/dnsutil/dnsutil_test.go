package dnsutil

import "testing"

func TestZoneAndRelativeName(t *testing.T) {
	fqdn := ChallengeFQDN("www.example.com")
	if fqdn != "_acme-challenge.www.example.com." {
		t.Fatalf("unexpected fqdn: %s", fqdn)
	}
	zone, err := ZoneName(fqdn, []string{"com.", "example.com.", "sub.example.com."})
	if err != nil {
		t.Fatalf("ZoneName: %v", err)
	}
	if zone != "example.com." {
		t.Fatalf("unexpected zone: %s", zone)
	}
	relative, err := RelativeName(fqdn, zone)
	if err != nil {
		t.Fatalf("RelativeName: %v", err)
	}
	if relative != "_acme-challenge.www" {
		t.Fatalf("unexpected relative: %s", relative)
	}
}

func TestChallengeFQDNFollowsCNAME(t *testing.T) {
	SetCNAMEResolver(func(fqdn string) (string, bool) {
		if fqdn == "_acme-challenge.example.com." {
			return "_acme-challenge.acme.example.net.", true
		}
		return "", false
	})
	t.Cleanup(func() { SetCNAMEResolver(nil) })

	if got := ChallengeFQDN("example.com"); got != "_acme-challenge.acme.example.net." {
		t.Fatalf("ChallengeFQDN = %q", got)
	}
	if got := ChallengeFQDN("other.example.com"); got != "_acme-challenge.other.example.com." {
		t.Fatalf("ChallengeFQDN without CNAME = %q", got)
	}
}

func TestTXTValue(t *testing.T) {
	// keyAuth 是 ACME 的 key authorization（token.thumbprint），
	// TXT 记录值必须是它的 SHA-256 base64url 摘要。
	if got, want := TXTValue("value"), "zUJATVKtVcz6mspK3IKKpYAK2dOFoGcfvL9yQRgyBhk"; got != want {
		t.Fatalf("TXTValue = %q, want %q", got, want)
	}
	if got := TXTValue("token.thumbprint"); len(got) != 43 {
		t.Fatalf("unexpected TXT value length: %d", len(got))
	}
}

func TestTXTValues(t *testing.T) {
	values := []string{`"a"`, `"b"`}
	if !ContainsTXT(values, "a") {
		t.Fatal("expected a to be present")
	}
	if ContainsTXT(values, "c") {
		t.Fatal("did not expect c")
	}
	remaining := RemoveTXT(values, "a")
	if len(remaining) != 1 || UnquoteTXT(remaining[0]) != "b" {
		t.Fatalf("unexpected remaining: %#v", remaining)
	}
	if ContainsTXT([]string{`"ABC"`}, "abc") {
		t.Fatal("TXT comparison must be case-sensitive")
	}
	if len(RemoveTXT([]string{`"ABC"`}, "abc")) != 1 {
		t.Fatal("RemoveTXT must not delete a case-different value")
	}
	if QuoteTXT("a") != `"a"` {
		t.Fatalf("unexpected quote: %s", QuoteTXT("a"))
	}
}
