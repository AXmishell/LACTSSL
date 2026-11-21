package acme

import (
	"net/http"
	"net/http/httptest"
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

func TestClientIPUsesTrustedForwardedHeaders(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:    "可信代理 XFF",
			trusted: []string{"192.0.2.0/24"},
			remote:  "192.0.2.30:1234",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "非可信来源忽略 XFF",
			trusted: []string{"192.0.2.0/24"},
			remote:  "198.51.100.1:1234",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:    "198.51.100.1",
		},
		{
			name:    "可信代理 X-Real-IP",
			trusted: []string{"192.0.2.0/24"},
			remote:  "192.0.2.30:1234",
			headers: map[string]string{"X-Real-IP": "203.0.113.10"},
			want:    "203.0.113.10",
		},
		{
			name:    "全链可信回退 peer",
			trusted: []string{"192.0.2.0/24"},
			remote:  "192.0.2.30:1234",
			headers: map[string]string{"X-Forwarded-For": "192.0.2.40, 192.0.2.30"},
			want:    "192.0.2.30",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{TrustedProxies: test.trusted}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = test.remote
			for key, value := range test.headers {
				req.Header.Set(key, value)
			}
			if got := s.clientIP(req); got != test.want {
				t.Fatalf("clientIP=%s want %s", got, test.want)
			}
		})
	}
}
