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
	mu     sync.Mutex
	values map[string]time.Time
	// order 是 nonce 的近似 FIFO 插入顺序，用于容量上限时淘汰较旧条目。
	// 队列中的条目可能已被 Consume 删除，淘汰时会跳过这些失效项。
	order     []string
	head      int
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
	n.order = append(n.order, value)
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
	// 时间清理必须独立于容量判断：达到上限后如果仍用 len<max 作为条件，
	// 每个 New/Consume 都会执行一次全表扫描。
	if now.Sub(n.lastSweep) < time.Minute {
		return
	}
	for value, expires := range n.values {
		if !now.Before(expires) {
			delete(n.values, value)
		}
	}
	n.rebuildOrderLocked()
	n.lastSweep = now
}

// rebuildOrderLocked 把队列收缩到当前仍然有效的 nonce。
// 队列只用于容量淘汰，允许顺序近似，重点是避免已消费/过期条目长期占用内存。
func (n *NonceStore) rebuildOrderLocked() {
	order := make([]string, 0, len(n.values))
	for value := range n.values {
		order = append(order, value)
	}
	n.order = order
	n.head = 0
}

func (n *NonceStore) evictLocked() {
	// 从队列头部跳过已消费或过期的条目，删除第一个仍有效的 nonce。
	for n.head < len(n.order) {
		value := n.order[n.head]
		n.order[n.head] = ""
		n.head++
		if _, ok := n.values[value]; ok {
			delete(n.values, value)
			n.compactOrderLocked()
			return
		}
	}
	// 理论上队列不会早于 values 耗尽；若出现异常，直接收缩队列并兜底删除一项。
	n.rebuildOrderLocked()
	if len(n.values) >= n.max {
		for value := range n.values {
			delete(n.values, value)
			break
		}
	}
}

// compactOrderLocked 在队列头部积累较多已失效条目时收缩底层切片。
// 使用“头部超过一半”作为阈值，平摊到每次插入仍是 O(1)。
func (n *NonceStore) compactOrderLocked() {
	if n.head == 0 {
		return
	}
	if n.head >= len(n.order) {
		n.order = n.order[:0]
		n.head = 0
		return
	}
	if n.head > 4096 && n.head*2 >= len(n.order) {
		n.order = append([]string(nil), n.order[n.head:]...)
		n.head = 0
	}
}
