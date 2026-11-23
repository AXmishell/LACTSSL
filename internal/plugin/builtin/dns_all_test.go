package builtin

import (
	"testing"

	plugdns "lactssl/internal/plugin/impl/dns"
)

func TestSyncDNSAllBuiltins(t *testing.T) {
	ids := make([]string, 0, 256)
	for _, p := range plugdns.List() {
		ids = append(ids, p.ID())
	}
	SyncDNS(true, true, IDSet(ids))
	if len(ids) < 200 {
		t.Fatalf("unexpected ids: %d", len(ids))
	}
}
