package api

import (
	"testing"
	"time"
)

// TestRateLimiterProtectsSensitiveBuckets 验证普通 key 洪泛不会淘汰登录等
// 安全关键限流桶。
func TestRateLimiterProtectsSensitiveBuckets(t *testing.T) {
	limiter := newRateLimiter(3)
	window := time.Minute
	if allowed, _ := limiter.Allow("login:acct:victim", 1, window); !allowed {
		t.Fatal("首次登录尝试应被允许")
	}
	for _, key := range []string{"noise:1", "noise:2", "noise:3"} {
		if allowed, _ := limiter.Allow(key, 1, window); !allowed {
			t.Fatalf("普通 bucket %s 首次应被允许", key)
		}
	}
	if allowed, _ := limiter.Allow("login:acct:victim", 1, window); allowed {
		t.Fatal("登录 bucket 不应被普通 key 洪泛淘汰")
	}
}
