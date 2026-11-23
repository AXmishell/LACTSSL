package builtin

import (
	"runtime"
	"testing"
)

func TestMetadataMemory(t *testing.T) {
	SyncDNS(false, false, nil)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	SyncDNS(true, true, IDSet([]string{"cloudflare"}))
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("DNS metadata heap delta: alloc=%+d KB sys=%+d KB heap_objects=%+d",
		(int64(after.HeapAlloc)-int64(before.HeapAlloc))/1024,
		(int64(after.HeapSys)-int64(before.HeapSys))/1024,
		int64(after.HeapObjects)-int64(before.HeapObjects))
}
