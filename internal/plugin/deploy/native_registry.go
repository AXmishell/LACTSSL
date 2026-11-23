// 本文件提供官方部署 helper 内部的部署目标注册表。
package deployplugin

import (
	"context"
	"sort"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// NativeCertificate 是交给插件的最小证书材料，与协议结构保持一致。
type NativeCertificate = protocol.Certificate

// NativeRequest 是一次部署请求。
type NativeRequest struct {
	Auth        map[string]string
	Config      map[string]string
	Certificate NativeCertificate
	// Locale 仅用于 verify，让插件返回对应语言的检查消息。
	Locale string
}

// NativeDeployer 是部署目标的统一接口。
type NativeDeployer interface {
	// ID 是部署类型的规范标识，必须与主程序数据库中的 deploy_accounts.type 一致。
	ID() string
	// Aliases 是兼容别名。
	Aliases() []string
	// Deploy 执行一次证书部署。
	Deploy(ctx context.Context, req NativeRequest) error
}

// NativeOptionsRequest 是一次动态选项读取请求。
// Field 是前端字段 key，Source 是 manifest 中声明的 options_source，
// Query 是可选的搜索词；插件按自身 API 决定如何使用这些参数。
type NativeOptionsRequest struct {
	Field  string
	Source string
	Query  string
	Auth   map[string]string
	Config map[string]string
}

// NativeOptionLister 是可选接口：部署目标可通过远端 API 提供下拉选项，
// 例如从宝塔面板读取网站或 Docker 网站列表。
type NativeOptionLister interface {
	ListOptions(ctx context.Context, req NativeOptionsRequest) ([]protocol.DeployOption, error)
}

// NativeVerifier 是可选接口：部署目标通过只读 API 验证认证与目标作用域。
// 实现方不得写入证书、创建持久对象或执行 reload 命令。
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
// 实现方不得写入证书或修改目标系统。
type NativePlanner interface {
	Plan(ctx context.Context, req NativePlanRequest) (NativePlanResponse, error)
}

// verifiable 记录实现了 NativeVerifier 的部署目标，供 manifest 输出能力位。
// 用显式清单而不是反射，避免某个目标暂时未实现时误报能力。
var verifiable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

// MarkNativeVerifiable 在 init 中声明部署目标支持只读验证。
func MarkNativeVerifiable(ids ...string) {
	verifiable.Lock()
	defer verifiable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			verifiable.ids[id] = true
		}
	}
}

// IsNativeVerifiable 判断部署目标是否声明了 verify-v1 能力。
func IsNativeVerifiable(id string) bool {
	verifiable.RLock()
	defer verifiable.RUnlock()
	return verifiable.ids[normalize(id)]
}

// plannable 记录实现了 NativePlanner 的部署目标，供 manifest 输出能力位。
var plannable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

// MarkNativePlannable 在 init 中声明部署目标支持 plan。
func MarkNativePlannable(ids ...string) {
	plannable.Lock()
	defer plannable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			plannable.ids[id] = true
		}
	}
}

// IsNativePlannable 判断部署目标是否声明了 plan-v1 能力。
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

// RegisterNative 注册内置部署器。重复的 ID 或别名会直接 panic。
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
		return errInvalidID
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

// LookupNative 按 ID 或别名查找部署器。
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

// HasNative 判断部署器是否已注册（含别名）。
func HasNative(name string) bool {
	_, ok := LookupNative(name)
	return ok
}

// ListNative 返回按 ID 排序的全部部署器。
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

// ResetNativeForTest 清空注册表，仅供测试使用。
func ResetNativeForTest() {
	nativeRegistry.Lock()
	defer nativeRegistry.Unlock()
	nativeRegistry.byID = make(map[string]NativeDeployer)
	nativeRegistry.alias = make(map[string]string)
}
