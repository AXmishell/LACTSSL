// Package dnsapi 定义官方 DNS provider 的 Native 接口与注册表。
//
// 该包位于独立的 providers module，宿主通过 type/function alias 复用；
// provider 实现只依赖本包与 plugins SDK，不依赖宿主 internal 包。
package dnsapi

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrInvalidID 表示 provider ID 为空或非法。
var ErrInvalidID = errors.New("dns provider id is empty or invalid")

// DuplicateError 表示 provider ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "dns provider already registered: " + e.ID }

// NativeProvider 是 DNS 服务商插件的统一接口。
type NativeProvider interface {
	ID() string
	Aliases() []string
	New(cfg map[string]string) (NativeChallengeProvider, error)
}

// NativeChallengeProvider 是 DNS-01 所需的最小能力集合。
type NativeChallengeProvider interface {
	Present(domain, token, keyAuth string) error
	CleanUp(domain, token, keyAuth string) error
}

// NativeVerifier 是可选的只读探测接口。
type NativeVerifier interface {
	Verify(ctx context.Context, domain string) (zone string, err error)
}

var verifiable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

// MarkNativeVerifiable 在 init 中声明服务商支持只读验证。
func MarkNativeVerifiable(ids ...string) {
	verifiable.Lock()
	defer verifiable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			verifiable.ids[id] = true
		}
	}
}

// IsNativeVerifiable 判断服务商是否声明了 verify-v1 能力。
func IsNativeVerifiable(id string) bool {
	verifiable.RLock()
	defer verifiable.RUnlock()
	return verifiable.ids[normalize(id)]
}

var nativeRegistry = struct {
	sync.RWMutex
	byID  map[string]NativeProvider
	alias map[string]string
}{
	byID:  make(map[string]NativeProvider),
	alias: make(map[string]string),
}

// RegisterNative 注册服务商。重复的 ID 或别名会直接 panic。
func RegisterNative(p NativeProvider) {
	if err := registerNative(p); err != nil {
		panic(err)
	}
}

func registerNative(p NativeProvider) error {
	id := normalize(p.ID())
	if id == "" {
		return ErrInvalidID
	}
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	if _, exists := nativeRegistry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias == "" {
			continue
		}
		if _, exists := nativeRegistry.byID[alias]; exists {
			return &DuplicateError{ID: alias}
		}
		if _, exists := nativeRegistry.alias[alias]; exists {
			return &DuplicateError{ID: alias}
		}
	}
	nativeRegistry.byID[id] = p
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias != "" && alias != id {
			nativeRegistry.alias[alias] = id
		}
	}
	return nil
}

// LookupNative 按 ID 或别名查找服务商。
func LookupNative(name string) (NativeProvider, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	if p, ok := nativeRegistry.byID[key]; ok {
		return p, true
	}
	if id, ok := nativeRegistry.alias[key]; ok {
		p, found := nativeRegistry.byID[id]
		return p, found
	}
	return nil, false
}

// HasNative 判断服务商是否已注册（含别名）。
func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

// ListNative 返回按 ID 排序的全部服务商。
func ListNative() []NativeProvider {
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	out := make([]NativeProvider, 0, len(nativeRegistry.byID))
	for _, p := range nativeRegistry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ResetNativeForTest 清空注册表，仅供测试使用。
func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeProvider)
	nativeRegistry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
