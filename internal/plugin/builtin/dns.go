//go:build !nobuiltin_dns

package builtin

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge"

	plugi18n "github.com/AXmishell/LACTSSL-Plugins/i18n"
	plugdnsutil "github.com/AXmishell/LACTSSL-Plugins/sdk/dnsutil"
	"lactssl/internal/i18n"
	dnsplugin "lactssl/internal/plugin/host/dns"
	pluginproto "lactssl/internal/plugin/proto"
	"lactssl/internal/util"
	plugcatalog "lactssl/providers/catalog"
	plugmeta "lactssl/providers/dns/lego5/meta"
)

// dnsEnvMu 串行化 lego v5 provider 的构造。
//
// lego v5 的服务商通过进程环境变量读取凭据；原 helper 每个请求一个进程，
// 因此天然隔离。进程内运行时只能在构造期间串行设置/恢复环境变量。
// Present/CleanUp 不再读取凭据环境变量，可以并发执行。
var dnsEnvMu sync.Mutex

type builtinDNSPlugin struct {
	inner dnsplugin.NativeProvider
	info  dnsplugin.ExternalProvider
}

func (p *builtinDNSPlugin) ID() string        { return p.info.ID }
func (p *builtinDNSPlugin) Aliases() []string { return p.info.Aliases }
func (p *builtinDNSPlugin) Source() dnsplugin.Source {
	return dnsplugin.SourceBuiltin
}
func (p *builtinDNSPlugin) Info() dnsplugin.ExternalProvider { return p.info }

func (p *builtinDNSPlugin) New(cfg map[string]string) (challenge.Provider, error) {
	provider, err := p.newProvider(cfg)
	if err != nil {
		return nil, err
	}
	return builtinChallenge{inner: provider}, nil
}

type builtinChallenge struct {
	inner dnsplugin.NativeChallengeProvider
}

func (p builtinChallenge) Present(domain, token, keyAuth string) error {
	return p.inner.Present(domain, token, keyAuth)
}

func (p builtinChallenge) CleanUp(domain, token, keyAuth string) error {
	return p.inner.CleanUp(domain, token, keyAuth)
}

// Timeout 暴露 provider 的传播等待参数；未实现时与 helper 默认值一致。
func (p builtinChallenge) Timeout() (time.Duration, time.Duration) {
	if timed, ok := p.inner.(interface {
		Timeout() (time.Duration, time.Duration)
	}); ok {
		return timed.Timeout()
	}
	return 180 * time.Second, 2 * time.Second
}

// Verify 实现宿主 dnsplugin.Verifier：mode=read 调用服务商只读探测，
// mode=write 或只读不支持时执行临时 TXT 写入/清理。
func (p *builtinDNSPlugin) Verify(ctx context.Context, cfg map[string]string, domain, mode, locale string) (pluginproto.VerifyResponse, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	loc := i18n.Locale(locale)
	if loc == "" {
		loc = i18n.Default
	}
	if mode == "read" {
		provider, err := p.newProvider(cfg)
		if err != nil {
			return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
				Key: "init", Status: pluginproto.VerifyStatusFailed,
				Message: i18n.T(loc, "service.verify.init_failed", map[string]any{"error": err.Error()}),
			}}}, nil
		}
		verifier, ok := provider.(dnsplugin.NativeVerifier)
		if !ok {
			return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
				Key: "verify", Status: pluginproto.VerifyStatusUnsupported,
				Message: i18n.T(loc, "service.verify.read_unsupported", nil),
			}}}, nil
		}
		zone, err := verifier.Verify(ctx, domain)
		if err != nil {
			return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
				Key: "zone", Status: pluginproto.VerifyStatusFailed,
				Message: i18n.T(loc, "service.verify.read_failed", map[string]any{"error": err.Error()}),
			}}}, nil
		}
		return pluginproto.VerifyResponse{OK: true, Checks: []pluginproto.VerifyCheck{{
			Key: "zone", Status: pluginproto.VerifyStatusOK,
			Message: i18n.T(loc, "service.verify.zone_ok", map[string]any{"zone": zone}),
		}}}, nil
	}
	return p.verifyWrite(ctx, cfg, domain, loc)
}

func (p *builtinDNSPlugin) newProvider(cfg map[string]string) (dnsplugin.NativeChallengeProvider, error) {
	def, _ := plugmeta.LookupDNS(p.info.ID)
	envs := plugcatalog.EnvForProvider(def, cfg)

	dnsEnvMu.Lock()
	previous := make(map[string]string, len(envs))
	present := make(map[string]bool, len(envs))
	for key := range envs {
		if value, ok := os.LookupEnv(key); ok {
			previous[key] = value
			present[key] = true
		}
	}
	defer func() {
		for key := range envs {
			if present[key] {
				_ = os.Setenv(key, previous[key])
			} else {
				_ = os.Unsetenv(key)
			}
		}
		dnsEnvMu.Unlock()
	}()
	for key, value := range envs {
		if strings.TrimSpace(value) == "" {
			_ = os.Unsetenv(key)
			continue
		}
		_ = os.Setenv(key, value)
	}
	return p.inner.New(cfg)
}

func (p *builtinDNSPlugin) verifyWrite(ctx context.Context, cfg map[string]string, domain string, loc i18n.Locale) (pluginproto.VerifyResponse, error) {
	response := pluginproto.VerifyResponse{OK: true, Checks: make([]pluginproto.VerifyCheck, 0, 2)}
	token, keyAuth, err := util.VerificationToken()
	if err != nil {
		return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
			Key: "token", Status: pluginproto.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.token_failed", map[string]any{"error": err.Error()}),
		}}}, nil
	}
	provider, err := p.newProvider(cfg)
	if err != nil {
		return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
			Key: "init", Status: pluginproto.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.init_failed", map[string]any{"error": err.Error()}),
		}}}, nil
	}
	if err := provider.Present(domain, token, keyAuth); err != nil {
		return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
			Key: "write", Status: pluginproto.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.write_failed", map[string]any{"error": err.Error()}),
		}}}, nil
	}
	response.Checks = append(response.Checks, pluginproto.VerifyCheck{Key: "write", Status: pluginproto.VerifyStatusOK})
	if err := provider.CleanUp(domain, token, keyAuth); err != nil {
		return pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{{
			Key: "cleanup", Status: pluginproto.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.cleanup_failed", map[string]any{"domain": domain, "error": err.Error()}),
		}}}, nil
	}
	response.Checks = append(response.Checks, pluginproto.VerifyCheck{Key: "cleanup", Status: pluginproto.VerifyStatusOK})
	return response, nil
}

// SyncDNS 重建内置 DNS 服务商注册。
// 返回注册错误；调用方应保留原有元数据并把错误暴露给管理员。
func SyncDNS(available, enabled bool, ids map[string]bool) error {
	if !available {
		if err := dnsplugin.ReplaceBuiltin(nil); err != nil {
			return err
		}
		dnsplugin.SetBuiltinProviders(nil)
		return nil
	}
	plugdnsutil.EnableCNAMEResolution()
	all := dnsplugin.ListNative()
	infos := make([]dnsplugin.ExternalProvider, 0, len(all))
	adapters := make([]*builtinDNSPlugin, 0, len(all))
	for _, provider := range all {
		id := strings.ToLower(strings.TrimSpace(provider.ID()))
		if id == "" {
			continue
		}
		active := enabled && ids[id]
		info := buildDNSInfo(provider, active)
		infos = append(infos, info)
		if active {
			adapters = append(adapters, &builtinDNSPlugin{inner: provider, info: info})
		}
	}
	plugins := make([]dnsplugin.Plugin, 0, len(adapters))
	for _, adapter := range adapters {
		plugins = append(plugins, adapter)
	}
	// 原子替换：全部注册成功后才生效，避免并发重扫时重复注册 panic。
	if err := dnsplugin.ReplaceBuiltin(plugins); err != nil {
		return err
	}
	dnsplugin.SetBuiltinProviders(infos)
	return nil
}

func buildDNSInfo(provider dnsplugin.NativeProvider, enabled bool) dnsplugin.ExternalProvider {
	id := strings.ToLower(strings.TrimSpace(provider.ID()))
	info := dnsplugin.ExternalProvider{
		ID:          id,
		Aliases:     append([]string(nil), provider.Aliases()...),
		Version:     "builtin",
		Enabled:     enabled,
		FileEnabled: true,
		Source:      string(dnsplugin.SourceBuiltin),
	}
	def, found := plugmeta.LookupDNS(id)
	if found {
		info.Name = def.LocalizedName(plugi18n.Default)
		info.Description = def.LocalizedDesc(plugi18n.Default)
		info.NameI18n = localizedProviderMap(def.LocalizedName)
		info.DescriptionI18n = localizedProviderMap(def.LocalizedDesc)
		info.Fields = convertDNSFields(def.Fields)
	}
	if info.Name == "" {
		info.Name = id
	}
	info.Capabilities = append(info.Capabilities, pluginproto.FeatureVerifyWrite)
	if dnsplugin.IsNativeVerifiable(id) {
		info.Capabilities = append(info.Capabilities, pluginproto.FeatureVerify)
	}
	return info
}

func convertDNSFields(fields []plugcatalog.Field) []pluginproto.DNSField {
	out := make([]pluginproto.DNSField, 0, len(fields))
	for _, field := range fields {
		item := pluginproto.DNSField{
			Key:             field.Key,
			Label:           field.LocalizedLabel(plugi18n.Default),
			LabelI18n:       localizedFieldMap(field.LocalizedLabel),
			Type:            field.Type,
			Required:        field.Required,
			Secret:          field.Secret,
			Placeholder:     field.LocalizedPlaceholder(plugi18n.Default),
			PlaceholderI18n: localizedFieldMap(field.LocalizedPlaceholder),
			Help:            field.LocalizedHelp(plugi18n.Default),
			HelpI18n:        localizedFieldMap(field.LocalizedHelp),
			Default:         field.Default,
		}
		if field.ShowIf != nil {
			item.ShowIf = &pluginproto.DNSCondition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, pluginproto.DNSOption{
				Label:     option.LocalizedLabel(plugi18n.Default),
				LabelI18n: localizedOptionMap(option.LocalizedLabel),
				Value:     option.Value,
			})
		}
		out = append(out, item)
	}
	return out
}
