package service

import (
	"context"
	"strings"
	"time"

	"lactssl/internal/deployplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// builtinDeployFunc 是内置部署目标的统一签名。
// manager 仅 SSH 的 TOFU 指纹回写需要，纯函数型目标可忽略。
type builtinDeployFunc func(ctx context.Context, manager *Manager, cert *model.Certificate, cfg map[string]string, account *model.DeployAccount) error

// builtinDeployers 把主程序内置的部署类型映射到具体实现。
// 只有本地文件与 SSH 属于内置目标；FTP、Webhook、CDN、面板等目标
// 由部署插件（internal/deployplugin/providers）提供。
var builtinDeployers = map[string]builtinDeployFunc{
	"local": func(_ context.Context, _ *Manager, cert *model.Certificate, cfg map[string]string, _ *model.DeployAccount) error {
		return deployLocal(cert, cfg)
	},
	"ssh": func(_ context.Context, manager *Manager, cert *model.Certificate, cfg map[string]string, account *model.DeployAccount) error {
		return deploySSH(manager, cert, cfg, account)
	},
}

// BuiltinDeployTypeIDs 返回内置部署类型集合，供外部插件扫描时避让。
func BuiltinDeployTypeIDs() map[string]bool {
	out := make(map[string]bool, len(builtinDeployers))
	for id := range builtinDeployers {
		out[id] = true
	}
	return out
}

// builtinDeployTypeInfo 只查找内置定义，不包含外部插件。
func builtinDeployTypeInfo(value string) (ProviderDef, bool) {
	value = strings.TrimSpace(value)
	for _, provider := range DeployTypes {
		if provider.Value == value {
			return provider, true
		}
	}
	return ProviderDef{}, false
}

// builtinDeployer 是内置部署目标对 deployplugin.Deployer 的适配。
type builtinDeployer struct {
	id      string
	manager *Manager
	fn      builtinDeployFunc
}

func (d builtinDeployer) ID() string                  { return d.id }
func (d builtinDeployer) Aliases() []string           { return nil }
func (d builtinDeployer) Source() deployplugin.Source { return deployplugin.SourceBuiltin }

func (d builtinDeployer) Deploy(ctx context.Context, req deployplugin.Request) error {
	def, found := builtinDeployTypeInfo(d.id)
	if !found {
		return i18n.E("service.deploy.type_unsupported", "type", d.id)
	}
	cfg := MergeDeployConfig(def, req.Auth, req.Config)
	account := &model.DeployAccount{Type: d.id, Config: req.Auth}
	return d.fn(ctx, d.manager, certificateFromRequest(req.Certificate), cfg, account)
}

// deployerFor 返回内置或外部部署器；内置实现优先，避免插件覆盖。
func (m *Manager) deployerFor(id string) (deployplugin.Deployer, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if fn, ok := builtinDeployers[id]; ok {
		return builtinDeployer{id: id, manager: m, fn: fn}, true
	}
	return deployplugin.Lookup(id)
}

// DeployBuiltin 调用主程序内置的部署实现（本地文件与 SSH）。
// 插件目标由 internal/deployplugin 的注册表负责，不经过该函数。
func DeployBuiltin(ctx context.Context, id string, cert *model.Certificate, auth, deployCfg map[string]string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	fn, ok := builtinDeployers[id]
	if !ok {
		return i18n.E("service.deploy.type_unsupported", "type", id)
	}
	def, found := builtinDeployTypeInfo(id)
	if !found {
		return i18n.E("service.deploy.type_unsupported", "type", id)
	}
	cfg := MergeDeployConfig(def, auth, deployCfg)
	return fn(ctx, nil, cert, cfg, &model.DeployAccount{Type: id, Config: auth})
}

// pluginCertificate 构造交给插件的证书材料。
func pluginCertificate(cert *model.Certificate) deployplugin.Certificate {
	if cert == nil {
		return deployplugin.Certificate{}
	}
	out := deployplugin.Certificate{
		Domain:       cert.PrimaryDomain,
		Domains:      append([]string(nil), cert.Domains...),
		Issuer:       cert.Issuer,
		Serial:       cert.Serial,
		CertPEM:      cert.CertPEM,
		FullchainPEM: cert.FullchainPEM,
		KeyPEM:       cert.KeyPEM,
	}
	if !cert.NotBefore.IsZero() {
		out.NotBefore = cert.NotBefore.Format(time.RFC3339)
	}
	if !cert.NotAfter.IsZero() {
		out.NotAfter = cert.NotAfter.Format(time.RFC3339)
	}
	return out
}

// certificateFromRequest 把插件请求还原为内部证书结构，供内置 helper 复用。
func certificateFromRequest(value deployplugin.Certificate) *model.Certificate {
	out := &model.Certificate{
		Domains:       append([]string(nil), value.Domains...),
		PrimaryDomain: value.Domain,
		Issuer:        value.Issuer,
		Serial:        value.Serial,
		CertPEM:       value.CertPEM,
		FullchainPEM:  value.FullchainPEM,
		KeyPEM:        value.KeyPEM,
	}
	if out.PrimaryDomain == "" && len(out.Domains) > 0 {
		out.PrimaryDomain = out.Domains[0]
	}
	if value.NotBefore != "" {
		if parsed, err := time.Parse(time.RFC3339, value.NotBefore); err == nil {
			out.NotBefore = parsed
		}
	}
	if value.NotAfter != "" {
		if parsed, err := time.Parse(time.RFC3339, value.NotAfter); err == nil {
			out.NotAfter = parsed
		}
	}
	return out
}
