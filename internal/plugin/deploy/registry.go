// Package deployplugin 提供证书部署目标的插件注册表。
//
// 官方实现随主程序编译，定义在 native_registry.go 与 providers/ 中；
// 外部目标通过一次性 exec helper 提供，由 Discover 在启动或手动重扫时注册。
package deployplugin

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	pluginproto "lactssl/internal/plugin/proto"
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
	// AccountID 是 deploy_accounts 的主键；内置 SSH 部署用它回写 TOFU 主机指纹。
	AccountID   int64
	Auth        map[string]string
	Config      map[string]string
	Certificate Certificate
	// Locale 仅用于 verify，让插件返回对应语言的检查消息。
	Locale string
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

// Verifier 是部署目标可选实现的凭据探测接口。
// 实现方只能做认证、连通性和作用域检查，不得写入证书或执行 reload 命令；
// 需要写入测试时使用唯一临时对象并在返回前清理。
type Verifier interface {
	Verify(ctx context.Context, req Request) (pluginproto.VerifyResponse, error)
}

// ErrVerifyUnsupported 表示部署目标未声明 verify-v1 能力或未实现只读探测。
var ErrVerifyUnsupported = errors.New("deploy target does not support verify")

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
	return registerInto(registry.byID, registry.alias, p)
}

// registerInto 把部署器写入给定的 id/alias 映射，调用方负责加锁。
func registerInto(byID map[string]Deployer, alias map[string]string, p Deployer) error {
	id := normalize(p.ID())
	if id == "" || !idPattern.MatchString(id) {
		return errInvalidID
	}
	if _, exists := byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	if _, exists := alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, item := range p.Aliases() {
		item = normalize(item)
		if item == "" {
			continue
		}
		if !idPattern.MatchString(item) {
			return errInvalidID
		}
		if _, exists := byID[item]; exists {
			return &DuplicateError{ID: item}
		}
		if _, exists := alias[item]; exists {
			return &DuplicateError{ID: item}
		}
	}
	byID[id] = p
	for _, item := range p.Aliases() {
		item = normalize(item)
		if item != "" && item != id {
			alias[item] = id
		}
	}
	return nil
}

// ReplaceBuiltin 原子替换全部内置部署器。
//
// 新集合全部注册成功后才交换注册表；失败时保留原有内置条目，
// 避免并发重扫期间出现半套注册或重复注册 panic。外部插件不受影响。
func ReplaceBuiltin(plugins []Deployer) error {
	registry.Lock()
	defer registry.Unlock()
	return replaceSourceLocked(SourceBuiltin, plugins)
}

func replaceSourceLocked(source Source, plugins []Deployer) error {
	byID := make(map[string]Deployer, len(registry.byID)+len(plugins))
	for id, p := range registry.byID {
		if p.Source() != source {
			byID[id] = p
		}
	}
	alias := make(map[string]string, len(byID))
	for id, p := range byID {
		for _, item := range p.Aliases() {
			if item = normalize(item); item != "" && item != id {
				alias[item] = id
			}
		}
	}
	for _, p := range plugins {
		if p.Source() != source {
			return registryError("plugin source does not match replacement target")
		}
		if err := registerInto(byID, alias, p); err != nil {
			return err
		}
	}
	registry.byID = byID
	registry.alias = alias
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
