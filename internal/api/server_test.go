package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterAllowsBurst(t *testing.T) {
	l := newRateLimiter(10)
	key := "test:burst"
	for i := 0; i < 3; i++ {
		if allowed, _ := l.Allow(key, 3, time.Minute); !allowed {
			t.Fatalf("第 %d 次请求不应被限制", i+1)
		}
	}
	allowed, wait := l.Allow(key, 3, time.Minute)
	if allowed {
		t.Fatal("超过阈值后应被限制")
	}
	if wait <= 0 {
		t.Fatalf("应返回重试等待时间: %v", wait)
	}
}

func TestRateLimiterRefillAndReset(t *testing.T) {
	l := newRateLimiter(10)
	key := "test:refill"

	if allowed, _ := l.Allow(key, 1, 50*time.Millisecond); !allowed {
		t.Fatal("首次请求应放行")
	}
	if allowed, _ := l.Allow(key, 1, 50*time.Millisecond); allowed {
		t.Fatal("窗口内第二次请求应被限制")
	}
	time.Sleep(60 * time.Millisecond)
	if allowed, _ := l.Allow(key, 1, 50*time.Millisecond); !allowed {
		t.Fatal("窗口过后应恢复配额")
	}

	l.Reset(key)
	if allowed, _ := l.Allow(key, 1, time.Minute); !allowed {
		t.Fatal("Reset 后应恢复配额")
	}
}

func TestRateLimiterMaxKeys(t *testing.T) {
	l := newRateLimiter(2)
	l.Allow("a", 10, time.Minute)
	l.Allow("b", 10, time.Minute)
	l.Allow("c", 10, time.Minute)
	if len(l.buckets) > 2 {
		t.Fatalf("key 数量超过上限: %d", len(l.buckets))
	}
}

func TestRateLimiterSweepIdle(t *testing.T) {
	l := newRateLimiter(10)
	l.Allow("old", 1, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	l.mu.Lock()
	l.lastSweep = time.Now().Add(-time.Hour)
	l.mu.Unlock()
	l.Allow("new", 1, time.Minute)

	l.mu.Lock()
	_, exists := l.buckets["old"]
	l.mu.Unlock()
	if exists {
		t.Fatal("空闲 key 应被定期清理")
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	l := newRateLimiter(100)
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("concurrent", 50, time.Hour); ok {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("并发限流结果不正确: %d", allowed)
	}
}

func TestNormalizeRateIP(t *testing.T) {
	if got := normalizeRateIP("192.0.2.1"); got != "192.0.2.1" {
		t.Fatalf("IPv4 归一化不正确: %s", got)
	}
	first := normalizeRateIP(net.ParseIP("2001:db8::1").String())
	second := normalizeRateIP(net.ParseIP("2001:db8::ffff").String())
	if first != second {
		t.Fatalf("同一 /64 的 IPv6 应归一化到同一 key: %s != %s", first, second)
	}
	if first != "2001:db8::/64" {
		t.Fatalf("IPv6 归一化结果不正确: %s", first)
	}
	if normalizeRateIP("not-an-ip") != "not-an-ip" {
		t.Fatal("无效 IP 应原样返回")
	}
}

func TestEntryMiddlewareRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{limiter: newRateLimiter(100)}
	s.entry.Store("secret-entry")

	r := gin.New()
	r.GET("/:entry/api/ping", s.entryMiddleware(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "192.0.2.10:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := request("/secret-entry/api/ping"); w.Code != http.StatusOK {
		t.Fatalf("正确入口应放行: %d", w.Code)
	}
	if w := request("/wrong-entry/api/ping"); w.Code != http.StatusNotFound {
		t.Fatalf("错误入口应返回 404: %d", w.Code)
	}

	var last *httptest.ResponseRecorder
	for i := 0; i < 60; i++ {
		last = request("/wrong-entry/api/ping")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("超过入口失败阈值应返回 429: %d", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应应包含 Retry-After")
	}
}
