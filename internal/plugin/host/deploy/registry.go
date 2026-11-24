// Package deployplugin 提供证书部署目标的插件注册表。
//
// 官方实现随主程序编译，定义在 native_registry.go 与 providers/ 中；
// 外部目标通过一次性 exec helper 提供，由 Discover 在启动或手动重扫时注册。
package deployplugin

import (
	"context"
	"errors"
	"strings"

	pluginproto "lactssl/internal/plugin/proto"
	"lactssl/internal/plugin/runtime/registry"
)

// Source 标识部署插件来源。
type Source = registry.Source

const (
	// SourceBuiltin 表示编译进主程序的内置部署目标。
	SourceBuiltin = registry.Builtin
	// SourceExec 表示外部 exec helper 插件。
	SourceExec = registry.Exec
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

var plugins = registry.New(registry.Adapter[Deployer]{
	ID:         func(p Deployer) string { return p.ID() },
	Aliases:    func(p Deployer) []string { return p.Aliases() },
	Source:     func(p Deployer) Source { return p.Source() },
	ValidateID: func(id string) bool { return idPattern.MatchString(id) },
})

// Register 注册内置部署器。重复的 ID 或别名会直接 panic，
// 以便在开发阶段立即发现冲突。
func Register(p Deployer) {
	if err := plugins.Register(p); err != nil {
		panic(err)
	}
}

// RegisterExternal 注册外部部署器，返回注册冲突等错误。
func RegisterExternal(p Deployer) error {
	return plugins.Register(p)
}

// ReplaceBuiltin 原子替换全部内置部署器。
//
// 新集合全部注册成功后才交换注册表；失败时保留原有内置条目，
// 避免并发重扫期间出现半套注册或重复注册 panic。外部插件不受影响。
func ReplaceBuiltin(items []Deployer) error {
	return plugins.ReplaceBuiltin(items)
}

// Lookup 按 ID 或别名查找部署器。
func Lookup(name string) (Deployer, bool) {
	return plugins.Lookup(name)
}

// Has 判断部署器是否已注册（含别名）。
func Has(name string) bool {
	return plugins.Has(name)
}

// List 返回按 ID 排序的全部部署器。
func List() []Deployer {
	return plugins.List()
}

// ListBySource 返回指定来源的部署器。
func ListBySource(source Source) []Deployer {
	return plugins.ListBySource(source)
}

// UnregisterSource 移除指定来源的全部部署器，用于外部插件重扫。
func UnregisterSource(source Source) {
	plugins.UnregisterSource(source)
}

// replaceExternal 原子替换全部外部部署器，避免扫描中途失败留下半套插件。
func replaceExternal(items []Deployer) ([]Deployer, []error) {
	return plugins.ReplaceSourceBestEffort(SourceExec, items)
}

// resetForTest 清空注册表，仅供测试使用。
func resetForTest() {
	plugins.Reset()
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
