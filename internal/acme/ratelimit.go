package acme

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ipRateLimiter 是 ACME 协议入口使用的轻量固定窗口限流器。
// 与内置管理端限流器独立，避免匿名 ACME 请求影响管理接口配额。
type ipRateLimiter struct {
	mu         sync.Mutex
	entries    map[string]*ipRateEntry
	max        int
	maxEntries int
	window     time.Duration
	lastSweep  time.Time
}

type ipRateEntry struct {
	count int
	reset time.Time
}

func newIPRateLimiter(max int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		entries:    make(map[string]*ipRateEntry),
		max:        max,
		maxEntries: 10000,
		window:     window,
		lastSweep:  time.Now(),
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
		if !ok && len(l.entries) >= l.maxEntries {
			l.evictLocked(now)
		}
		l.entries[key] = &ipRateEntry{count: 1, reset: now.Add(l.window)}
		return true
	}
	if entry.count >= l.max {
		return false
	}
	entry.count++
	return true
}

// evictLocked 在达到容量上限时删除已过期项；仍超限则删除最早重置的一项。
func (l *ipRateLimiter) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, entry := range l.entries {
		if now.After(entry.reset) {
			delete(l.entries, key)
			continue
		}
		if oldestKey == "" || entry.reset.Before(oldest) {
			oldestKey, oldest = key, entry.reset
		}
	}
	if len(l.entries) >= l.maxEntries && oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}

// clientIP 返回用于限流的客户端 IP；仅在请求来自可信代理时采信 X-Forwarded-For。
func (s *Server) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	host, _, err := net.SplitHostPort(strings.TrimSpace(remote))
	if err != nil {
		host = strings.TrimSpace(remote)
	}
	remoteIP := net.ParseIP(strings.Trim(host, "[]"))
	if remoteIP == nil || !trustedIP(remoteIP, s.TrustedProxies) {
		return remote
	}
	values := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for index := len(values) - 1; index >= 0; index-- {
		ip := net.ParseIP(strings.TrimSpace(values[index]))
		if ip == nil {
			continue
		}
		if trustedIP(ip, s.TrustedProxies) {
			continue
		}
		return ip.String()
	}
	return remote
}

// trustedIP 判断 IP 是否位于可信代理列表。
func trustedIP(ip net.IP, proxies []string) bool {
	for _, entry := range proxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if proxy := net.ParseIP(entry); proxy != nil && proxy.Equal(ip) {
			return true
		}
	}
	return false
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
