package builtin

import (
	"sync"
	"testing"

	dnsplugin "lactssl/internal/plugin/dns"
	plugdns "lactssl/internal/plugin/impl/dns"
)

// TestSyncDNSConcurrentReplace 确保并发重扫不会因为重复注册而 panic。
// 修复前 SyncDNS 先注销再逐个 Register，两个 goroutine 交错时会触发
// "dns plugin already registered"；ReplaceBuiltin 在单次锁内原子替换。
func TestSyncDNSConcurrentReplace(t *testing.T) {
	ids := make([]string, 0, 256)
	for _, provider := range plugdns.List() {
		ids = append(ids, provider.ID())
	}
	set := IDSet(ids)
	if err := SyncDNS(true, true, set); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	t.Cleanup(func() {
		_ = SyncDNS(false, false, nil)
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				if err := SyncDNS(true, true, set); err != nil {
					t.Errorf("sync: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if !dnsplugin.Has("cloudflare") {
		t.Fatal("cloudflare should stay registered")
	}
	if got := len(dnsplugin.ListBySource(dnsplugin.SourceBuiltin)); got != len(ids) {
		t.Fatalf("builtin registry size = %d, want %d", got, len(ids))
	}
}
