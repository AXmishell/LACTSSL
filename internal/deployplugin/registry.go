// Package deployplugin 提供证书部署目标的插件注册表。
//
// 内置部署目标由 internal/service 实现；外部目标通过一次性 exec helper
// 提供，由 Discover 在启动或手动重扫时注册。
package deployplugin

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Source 标识部署插件来源。
type Source string

const (
	// SourceBuiltin 表示编译进主程序的内置部署目标。
	SourceBuiltin Source = "builtin"
	// SourceExec 表示外部 exec helper 插件。
	SourceExec Source = "exec"
)

// Certificate 是交给插件的最小证书材料。
// 私钥只通过 stdin 传递，不写入 argv 或环境变量。
type Certificate struct {
	Domain       string   `json:"domain"`
	Domains      []string `json:"domains,omitempty"`
	Issuer       string   `json:"issuer,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	NotBefore    string   `json:"not_before,omitempty"`
	NotAfter     string   `json:"not_after,omitempty"`
	CertPEM      string   `json:"cert_pem"`
	FullchainPEM string   `json:"fullchain_pem"`
	KeyPEM       string   `json:"key_pem"`
}

// Request 是一次部署请求。
// Auth 是目标凭据中的认证与连接字段，Config 是任务流程部署节点中的参数。
type Request struct {
	Auth        map[string]string
	Config      map[string]string
	Certificate Certificate
}

// Deployer 是部署目标的统一接口。
type Deployer interface {
	// ID 是部署类型的规范标识，必须与数据库中的 deploy_accounts.type 一致。
	ID() string
	// Aliases 是兼容别名。
	Aliases() []string
	// Source 返回实现来源。
	Source() Source
	// Deploy 执行一次证书部署。
	Deploy(ctx context.Context, req Request) error
}

var registry = struct {
	sync.RWMutex
	byID  map[string]Deployer
	alias map[string]string
}{
	byID:  make(map[string]Deployer),
	alias: make(map[string]string),
}

// Register 注册内置部署器。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立即发现冲突。
func Register(p Deployer) {
	if err := register(p); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部部署器，返回注册冲突等错误。
func RegisterExternal(p Deployer) error {
	return register(p)
}

func register(p Deployer) error {
	registry.Lock()
	defer registry.Unlock()
	return registerLocked(p)
}

func registerLocked(p Deployer) error {
	id := normalize(p.ID())
	if id == "" || !idPattern.MatchString(id) {
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
		if !idPattern.MatchString(alias) {
			return errInvalidID
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

// ListBySource 返回指定来源的部署器。
func ListBySource(source Source) []Deployer {
	all := List()
	out := all[:0]
	for _, p := range all {
		if p.Source() == source {
			out = append(out, p)
		}
	}
	return out
}

// UnregisterSource 移除指定来源的全部部署器，用于外部插件重扫。
func UnregisterSource(source Source) {
	registry.Lock()
	defer registry.Unlock()
	clearLocked(source)
}

func clearLocked(source Source) {
	for id, p := range registry.byID {
		if p.Source() != source {
			continue
		}
		delete(registry.byID, id)
		for _, alias := range p.Aliases() {
			alias = normalize(alias)
			if current, ok := registry.alias[alias]; ok && current == id {
				delete(registry.alias, alias)
			}
		}
	}
}

// replaceExternal 原子替换全部外部部署器，避免扫描中途失败留下半套插件。
func replaceExternal(plugins []Deployer) ([]Deployer, []error) {
	registry.Lock()
	defer registry.Unlock()
	clearLocked(SourceExec)
	loaded := make([]Deployer, 0, len(plugins))
	var errs []error
	for _, p := range plugins {
		if err := registerLocked(p); err != nil {
			errs = append(errs, err)
			continue
		}
		loaded = append(loaded, p)
	}
	return loaded, errs
}

// resetForTest 清空注册表，仅供测试使用。
func resetForTest() {
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
