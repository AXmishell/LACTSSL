package acme

import (
	"net"
	"sync"
	"time"
)

// ipRateLimiter 是 ACME 协议入口使用的轻量固定窗口限流器。
// 与内置管理端限流器独立，避免匿名 ACME 请求影响管理接口配额。
type ipRateLimiter struct {
	mu        sync.Mutex
	entries   map[string]*ipRateEntry
	max       int
	window    time.Duration
	lastSweep time.Time
}

type ipRateEntry struct {
	count int
	reset time.Time
}

func newIPRateLimiter(max int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		entries:   make(map[string]*ipRateEntry),
		max:       max,
		window:    window,
		lastSweep: time.Now(),
	}
}

// Allow 按客户端 IP 计数；IPv6 归并到 /64，防止轮换地址绕过。
func (l *ipRateLimiter) Allow(remoteAddr string) bool {
	if l == nil || l.max <= 0 || l.window <= 0 {
		return true
	}
	key := remoteRateKey(remoteAddr)
	if key == "" {
		return true
	}
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > l.window {
		for k, entry := range l.entries {
			if now.After(entry.reset) {
				delete(l.entries, k)
			}
		}
		l.lastSweep = now
	}

	entry, ok := l.entries[key]
	if !ok || now.After(entry.reset) {
		l.entries[key] = &ipRateEntry{count: 1, reset: now.Add(l.window)}
		return true
	}
	if entry.count >= l.max {
		return false
	}
	entry.count++
	return true
}

func remoteRateKey(remoteAddr string) string {
	host := remoteAddr
	if value, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = value
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
