package builtin

import (
	dnsplugin "lactssl/internal/plugin/host/dns"
	"testing"
)

func TestSyncDNSAllBuiltins(t *testing.T) {
	ids := make([]string, 0, 256)
	for _, p := range dnsplugin.ListNative() {
		ids = append(ids, p.ID())
	}
	SyncDNS(true, true, IDSet(ids))
	if len(ids) < 200 {
		t.Fatalf("unexpected ids: %d", len(ids))
	}
}
