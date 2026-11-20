package acme

import (
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"
)

func TestOCSPCacheKeyStableForGoodAndUnknown(t *testing.T) {
	now := time.Now()
	first, _ := ocspCacheKey(1, "ABCDEF", ocsp.Good, now, 0)
	second, _ := ocspCacheKey(1, "ABCDEF", ocsp.Good, now.Add(5*time.Second), 0)
	if first != second {
		t.Fatalf("Good 缓存 key 不应包含当前时间: %q != %q", first, second)
	}
	unknown1, ttl := ocspCacheKey(1, "ABCDEF", ocsp.Unknown, now, 0)
	unknown2, _ := ocspCacheKey(1, "ABCDEF", ocsp.Unknown, now.Add(time.Minute), 0)
	if unknown1 != unknown2 {
		t.Fatal("Unknown 缓存 key 应保持稳定")
	}
	if ttl <= 0 {
		t.Fatal("Unknown 应有负缓存 TTL")
	}
}

func TestOCSPCacheKeyRevokedBindsTime(t *testing.T) {
	now := time.Now()
	first, _ := ocspCacheKey(1, "ABCDEF", ocsp.Revoked, now, 4)
	second, _ := ocspCacheKey(1, "ABCDEF", ocsp.Revoked, now.Add(time.Second), 4)
	if first == second {
		t.Fatal("Revoked 缓存 key 应绑定吊销时间")
	}
}
