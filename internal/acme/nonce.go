package acme

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

const (
	defaultNonceTTL = 10 * time.Minute
	defaultNonceMax = 100000
)

// NonceStore 保存一次性 ACME Replay-Nonce。单实例部署使用内存即可。
type NonceStore struct {
	mu        sync.Mutex
	values    map[string]time.Time
	ttl       time.Duration
	max       int
	lastSweep time.Time
	now       func() time.Time
}

// NewNonceStore 创建 nonce 存储。
func NewNonceStore() *NonceStore {
	return &NonceStore{
		values: make(map[string]time.Time),
		ttl:    defaultNonceTTL,
		max:    defaultNonceMax,
		now:    time.Now,
	}
}

// New 生成并保存一个 nonce。
func (n *NonceStore) New() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(buf)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sweepLocked()
	if len(n.values) >= n.max {
		n.evictLocked()
	}
	n.values[value] = n.now().Add(n.ttl)
	return value, nil
}

// Consume 校验并消费 nonce，同一个 nonce 只能使用一次。
func (n *NonceStore) Consume(value string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sweepLocked()
	expires, ok := n.values[value]
	if !ok {
		return false
	}
	delete(n.values, value)
	return n.now().Before(expires)
}

func (n *NonceStore) sweepLocked() {
	now := n.now()
	if now.Sub(n.lastSweep) < time.Minute && len(n.values) < n.max {
		return
	}
	for value, expires := range n.values {
		if !now.Before(expires) {
			delete(n.values, value)
		}
	}
	n.lastSweep = now
}

func (n *NonceStore) evictLocked() {
	// 达到上限时删除最早过期的一批 nonce，避免内存无限增长。
	var oldest string
	var oldestExpiry time.Time
	for value, expires := range n.values {
		if oldest == "" || expires.Before(oldestExpiry) {
			oldest, oldestExpiry = value, expires
		}
	}
	if oldest != "" {
		delete(n.values, oldest)
	}
}
