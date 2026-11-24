// Package deployapi 定义官方部署目标的 Native 接口与注册表。
package deployapi

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// ErrInvalidID 表示部署目标 ID 为空或非法。
var ErrInvalidID = errors.New("deploy target id is empty or invalid")

// DuplicateError 表示部署目标 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "deploy target already registered: " + e.ID }

// NativeCertificate 是交给插件的最小证书材料，与协议结构保持一致。
type NativeCertificate = protocol.Certificate

// NativeRequest 是一次部署请求。
type NativeRequest struct {
	Auth        map[string]string
	Config      map[string]string
	Certificate NativeCertificate
	Locale      string
}

// NativeDeployer 是部署目标的统一接口。
type NativeDeployer interface {
	ID() string
	Aliases() []string
	Deploy(ctx context.Context, req NativeRequest) error
}

// NativeOptionsRequest 是一次动态选项读取请求。
type NativeOptionsRequest struct {
	Field  string
	Source string
	Query  string
	Auth   map[string]string
	Config map[string]string
}

// NativeOptionLister 是可选接口：部署目标可通过远端 API 提供下拉选项。
type NativeOptionLister interface {
	ListOptions(ctx context.Context, req NativeOptionsRequest) ([]protocol.DeployOption, error)
}

// NativeVerifier 是可选接口：部署目标通过只读 API 验证认证与目标作用域。
type NativeVerifier interface {
	Verify(ctx context.Context, req NativeRequest) ([]protocol.VerifyCheck, error)
}

// NativePlanRequest 是读取可部署对象的请求。
type NativePlanRequest struct {
	Auth   map[string]string
	Config map[string]string
	Locale string
}

// NativePlanObject 是一个可部署对象。
type NativePlanObject struct {
	Value       string
	Name        string
	Domains     []string
	Identifiers []string
}

// NativePlanResponse 描述目标字段和当前可部署对象。
type NativePlanResponse struct {
	Field    string
	Multiple bool
	Objects  []NativePlanObject
}

// NativePlanner 是可选接口：部署目标通过只读 API 返回网站清单。
type NativePlanner interface {
	Plan(ctx context.Context, req NativePlanRequest) (NativePlanResponse, error)
}

var verifiable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

func MarkNativeVerifiable(ids ...string) {
	verifiable.Lock()
	defer verifiable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			verifiable.ids[id] = true
		}
	}
}

func IsNativeVerifiable(id string) bool {
	verifiable.RLock()
	defer verifiable.RUnlock()
	return verifiable.ids[normalize(id)]
}

var plannable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

func MarkNativePlannable(ids ...string) {
	plannable.Lock()
	defer plannable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			plannable.ids[id] = true
		}
	}
}

func IsNativePlannable(id string) bool {
	plannable.RLock()
	defer plannable.RUnlock()
	return plannable.ids[normalize(id)]
}

var nativeRegistry = struct {
	sync.RWMutex
	byID  map[string]NativeDeployer
	alias map[string]string
}{
	byID:  make(map[string]NativeDeployer),
	alias: make(map[string]string),
}

func RegisterNative(p NativeDeployer) {
	if err := registerNative(p); err != nil {
		panic(err)
	}
}

func registerNative(p NativeDeployer) error {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	id := normalize(p.ID())
	if id == "" {
		return ErrInvalidID
	}
	if _, exists := nativeRegistry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := nativeRegistry.alias[id]; exists {
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

func LookupNative(name string) (NativeDeployer, bool) {
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

func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

func ListNative() []NativeDeployer {
	nativeRegistry.RLock()
	defer nativeRegistry.RUnlock()
	out := make([]NativeDeployer, 0, len(nativeRegistry.byID))
	for _, p := range nativeRegistry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeDeployer)
	nativeRegistry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
