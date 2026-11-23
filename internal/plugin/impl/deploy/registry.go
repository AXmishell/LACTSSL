// Package deploy 提供官方部署 helper 内部的部署目标注册表。
package deploy

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

// Certificate 是交给插件的最小证书材料，与协议结构保持一致。
type Certificate = protocol.Certificate

// Request 是一次部署请求。
type Request struct {
	Auth        map[string]string
	Config      map[string]string
	Certificate Certificate
	// Locale 仅用于 verify，让插件返回对应语言的检查消息。
	Locale string
}

// Deployer 是部署目标的统一接口。
type Deployer interface {
	// ID 是部署类型的规范标识，必须与主程序数据库中的 deploy_accounts.type 一致。
	ID() string
	// Aliases 是兼容别名。
	Aliases() []string
	// Deploy 执行一次证书部署。
	Deploy(ctx context.Context, req Request) error
}

// OptionsRequest 是一次动态选项读取请求。
// Field 是前端字段 key，Source 是 manifest 中声明的 options_source，
// Query 是可选的搜索词；插件按自身 API 决定如何使用这些参数。
type OptionsRequest struct {
	Field  string
	Source string
	Query  string
	Auth   map[string]string
	Config map[string]string
}

// OptionLister 是可选接口：部署目标可通过远端 API 提供下拉选项，
// 例如从宝塔面板读取网站或 Docker 网站列表。
type OptionLister interface {
	ListOptions(ctx context.Context, req OptionsRequest) ([]protocol.DeployOption, error)
}

// Verifier 是可选接口：部署目标通过只读 API 验证认证与目标作用域。
// 实现方不得写入证书、创建持久对象或执行 reload 命令。
type Verifier interface {
	Verify(ctx context.Context, req Request) ([]protocol.VerifyCheck, error)
}

// PlanRequest 是读取可部署对象的请求。
type PlanRequest struct {
	Auth   map[string]string
	Config map[string]string
	Locale string
}

// PlanObject 是一个可部署对象。
type PlanObject struct {
	Value       string
	Name        string
	Domains     []string
	Identifiers []string
}

// PlanResponse 描述目标字段和当前可部署对象。
type PlanResponse struct {
	Field    string
	Multiple bool
	Objects  []PlanObject
}

// Planner 是可选接口：部署目标通过只读 API 返回网站清单。
// 实现方不得写入证书或修改目标系统。
type Planner interface {
	Plan(ctx context.Context, req PlanRequest) (PlanResponse, error)
}

// verifiable 记录实现了 Verifier 的部署目标，供 manifest 输出能力位。
// 用显式清单而不是反射，避免某个目标暂时未实现时误报能力。
var verifiable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

// MarkVerifiable 在 init 中声明部署目标支持只读验证。
func MarkVerifiable(ids ...string) {
	verifiable.Lock()
	defer verifiable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			verifiable.ids[id] = true
		}
	}
}

// IsVerifiable 判断部署目标是否声明了 verify-v1 能力。
func IsVerifiable(id string) bool {
	verifiable.RLock()
	defer verifiable.RUnlock()
	return verifiable.ids[normalize(id)]
}

// plannable 记录实现了 Planner 的部署目标，供 manifest 输出能力位。
var plannable = struct {
	sync.RWMutex
	ids map[string]bool
}{ids: make(map[string]bool)}

// MarkPlannable 在 init 中声明部署目标支持 plan。
func MarkPlannable(ids ...string) {
	plannable.Lock()
	defer plannable.Unlock()
	for _, id := range ids {
		if id = normalize(id); id != "" {
			plannable.ids[id] = true
		}
	}
}

// IsPlannable 判断部署目标是否声明了 plan-v1 能力。
func IsPlannable(id string) bool {
	plannable.RLock()
	defer plannable.RUnlock()
	return plannable.ids[normalize(id)]
}

var registry = struct {
	sync.RWMutex
	byID  map[string]Deployer
	alias map[string]string
}{
	byID:  make(map[string]Deployer),
	alias: make(map[string]string),
}

// Register 注册内置部署器。重复的 ID 或别名会直接 panic。
func Register(p Deployer) {
	if err := register(p); err != nil {
		panic(err)
	}
}

func register(p Deployer) error {
	registry.Lock()
	defer registry.Unlock()
	id := normalize(p.ID())
	if id == "" {
		return errInvalidID
	}
	if _, exists := registry.byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := registry.alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias == "" {
			continue
		}
		if _, exists := registry.byID[alias]; exists {
			return &DuplicateError{ID: alias}
		}
		if _, exists := registry.alias[alias]; exists {
			return &DuplicateError{ID: alias}
		}
	}
	registry.byID[id] = p
	for _, alias := range p.Aliases() {
		alias = normalize(alias)
		if alias != "" && alias != id {
			registry.alias[alias] = id
		}
	}
	return nil
}

// Lookup 按 ID 或别名查找部署器。
func Lookup(name string) (Deployer, bool) {
	key := normalize(name)
	if key == "" {
		return nil, false
	}
	registry.RLock()
	defer registry.RUnlock()
	if p, ok := registry.byID[key]; ok {
		return p, true
	}
	if id, ok := registry.alias[key]; ok {
		p, found := registry.byID[id]
		return p, found
	}
	return nil, false
}

// Has 判断部署器是否已注册（含别名）。
func Has(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// List 返回按 ID 排序的全部部署器。
func List() []Deployer {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]Deployer, 0, len(registry.byID))
	for _, p := range registry.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ResetForTest 清空注册表，仅供测试使用。
func ResetForTest() {
	registry.Lock()
	defer registry.Unlock()
	registry.byID = make(map[string]Deployer)
	registry.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// DuplicateError 表示部署器 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "deploy plugin already registered: " + e.ID }

type registryError string

func (e registryError) Error() string { return string(e) }

const errInvalidID registryError = "deploy plugin id is empty or invalid"
