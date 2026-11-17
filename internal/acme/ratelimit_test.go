package acme

import (
	"testing"
	"time"
)

func TestIPRateLimiterLimitsPerIP(t *testing.T) {
	limiter := newIPRateLimiter(2, time.Hour)
	if !limiter.Allow("192.0.2.1:1000") || !limiter.Allow("192.0.2.1:1001") {
		t.Fatal("配额内请求应放行")
	}
	if limiter.Allow("192.0.2.1:1002") {
		t.Fatal("超过配额后应拒绝")
	}
	if !limiter.Allow("198.51.100.1:1000") {
		t.Fatal("不同 IP 应独立计数")
	}
}

func TestIPRateLimiterNormalizesIPv6(t *testing.T) {
	limiter := newIPRateLimiter(1, time.Hour)
	if !limiter.Allow("[2001:db8::1]:1000") {
		t.Fatal("首次请求应放行")
	}
	if limiter.Allow("[2001:db8::2]:1000") {
		t.Fatal("同一 /64 的 IPv6 应共享配额")
	}
}
