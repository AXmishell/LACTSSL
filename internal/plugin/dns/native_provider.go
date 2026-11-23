// 本文件定义官方 DNS helper 侧的最小 DNS-01 接口。
//
// 该接口刻意不依赖具体 lego 版本，这样上游包装可以通过结构类型满足该接口。
package dnsplugin

import (
	"context"
	"strings"
	"sync"
)

// NativeProvider 是 DNS 服务商插件的统一接口。
type NativeProvider interface {
	// ID 是插件的规范标识，必须与主程序数据库中的 provider 值一致。
	ID() string
	// Aliases 是兼容别名。
	Aliases() []string
	// New 构造 DNS-01 challenge provider。配置通过 cfg 传入，
	// 同时 helper 仍会按 catalog 设置兼容的环境变量。
	New(cfg map[string]string) (NativeChallengeProvider, error)
}

// NativeChallengeProvider 是 DNS-01 所需的最小能力集合。
//
// 它与 lego 的 challenge.NativeProvider 具有相同的方法签名，
// 因此原有 lego provider 无需修改即可继续使用。
type NativeChallengeProvider interface {
	Present(domain, token, keyAuth string) error
	CleanUp(domain, token, keyAuth string) error
}

// NativeVerifier 是可选的只读探测接口：列出账号可管理的 zone，并确认域名归属。
// 返回的 zone 用于展示；未实现该接口的服务商由 helper 返回 unsupported，
// 宿主可回退到 DNS-01 写入测试。
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
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			verifiable.ids[id] = true
		}
	}
}

// IsNativeVerifiable 判断服务商是否声明了 verify-v1 能力。
func IsNativeVerifiable(id string) bool {
	verifiable.RLock()
	defer verifiable.RUnlock()
	return verifiable.ids[strings.ToLower(strings.TrimSpace(id))]
}
