package pluginmeta

import (
	"testing"

	"lactssl/internal/plugin/proto"
)

func TestCacheLifecycleAndAliases(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	if err := Put("dns", "cloudflare", "1.0.0", "Cloudflare", pluginproto.DNSProvider{
		ID:      "cloudflare",
		Name:    "Cloudflare",
		Aliases: []string{"cf"},
		Fields:  []pluginproto.DNSField{{Key: "token", Secret: true}},
	}); err != nil {
		t.Fatal(err)
	}

	provider, ok := Provider("cf")
	if !ok || provider.ID != "cloudflare" || len(provider.Fields) != 1 {
		t.Fatalf("alias lookup = %+v, ok=%v", provider, ok)
	}

	ReplaceKind("dns", []Entry{{
		Kind: "dns", ID: "ovh", Version: "1.0.0", Name: "OVH",
		Raw: []byte(`{"id":"ovh","name":"OVH"}`),
	}})
	if _, ok := Provider("cloudflare"); ok {
		t.Fatal("ReplaceKind should remove stale entries")
	}
	if provider, ok := Provider("ovh"); !ok || provider.Name != "OVH" {
		t.Fatalf("replacement entry = %+v, ok=%v", provider, ok)
	}
}
