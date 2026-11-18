package acme

import (
	"testing"
	"time"
)

func TestNonceStoreEvictsOldestLiveAtCapacity(t *testing.T) {
	store := NewNonceStore()
	store.max = 3
	v1, _ := store.New()
	v2, _ := store.New()
	v3, _ := store.New()
	if !store.Consume(v1) {
		t.Fatal("first nonce should be consumable")
	}
	// 已消费的 v1 不应占用容量；v4 插入后 live=3。
	v4, _ := store.New()
	if !store.Consume(v4) {
		t.Fatal("new nonce should be consumable")
	}
	v4, _ = store.New()
	// live = v2 v3 v4，再次 New 需要淘汰最旧的 live 条目 v2。
	_, _ = store.New()
	if store.Consume(v2) {
		t.Fatal("oldest live nonce should be evicted at capacity")
	}
	if !store.Consume(v3) || !store.Consume(v4) {
		t.Fatal("newer nonces should survive eviction")
	}
}

func TestNonceStoreQueueCompactsOnSweep(t *testing.T) {
	store := NewNonceStore()
	store.max = 1000
	for i := 0; i < 5000; i++ {
		if _, err := store.New(); err != nil {
			t.Fatal(err)
		}
	}
	// 强制触发一次时间清理，队列应收缩到当前存活 nonce 的数量附近。
	store.lastSweep = time.Time{}
	if _, err := store.New(); err != nil {
		t.Fatal(err)
	}
	if len(store.order) > store.max+1 {
		t.Fatalf("nonce 队列未收缩: len(order)=%d, max=%d", len(store.order), store.max)
	}
}
