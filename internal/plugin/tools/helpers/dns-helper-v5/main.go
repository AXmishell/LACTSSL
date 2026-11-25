// Command dns-helper-v5 是 LACTSSL DNS 插件协议的 lego v5 实现。
//
// 它使用协议 v1，provider 全部来自 github.com/go-acme/lego/v5，
// 作为独立插件包发布。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/dnsutil"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/helper"
	dns "lactssl/internal/plugin/host/dns"
	dnsstate "lactssl/internal/plugin/host/dns/state"
	"lactssl/internal/util"
	"lactssl/providers/catalog"
	_ "lactssl/providers/dns/lego5/groups/all"
	"lactssl/providers/dns/lego5/meta"
)

func main() {
	defer helper.Recover()
	dnsutil.EnableCNAMEResolution()
	helper.ApplyMemoryLimit()
	if len(os.Args) < 2 {
		helper.Fail("用法: dns-helper-v5 manifest|present|cleanup|verify")
	}
	switch os.Args[1] {
	case "manifest", "list":
		writeManifest()
	case "present", "cleanup", "verify":
		handle(os.Args[1])
	default:
		helper.Fail("未知子命令: " + os.Args[1])
	}
}

func writeManifest() {
	if err := helper.WriteJSON(buildManifest()); err != nil {
		helper.Fail("输出 manifest 失败: " + err.Error())
	}
}

func buildManifest() protocol.DNSManifest {
	readEnabled := helper.HasFeatureIn(protocol.DNSFeaturesEnv, protocol.FeatureVerify)
	writeEnabled := helper.HasFeatureIn(protocol.DNSFeaturesEnv, protocol.FeatureVerifyWrite)
	manifest := protocol.DNSManifest{
		Protocol:  protocol.ProtocolVersion,
		Name:      "lactssl dns helper v5",
		Version:   serviceVersion(),
		Providers: make([]protocol.DNSProvider, 0, len(dns.ListNative())),
	}
	for _, plugin := range dns.ListNative() {
		def, ok := meta.LookupDNS(plugin.ID())
		if !ok {
			continue
		}
		manifest.Providers = append(manifest.Providers, protocol.DNSProvider{
			ID:              plugin.ID(),
			Name:            def.LocalizedName(i18n.Default),
			NameI18n:        localizedTextMap(func(locale i18n.Locale) string { return def.LocalizedName(locale) }),
			Description:     def.LocalizedDesc(i18n.Default),
			DescriptionI18n: localizedTextMap(func(locale i18n.Locale) string { return def.LocalizedDesc(locale) }),
			Aliases:         plugin.Aliases(),
			Fields:          manifestFields(def.Fields),
			Capabilities:    providerCapabilities(plugin.ID(), readEnabled, writeEnabled),
		})
	}
	return manifest
}

// providerCapabilities 声明 provider 支持的验证能力。
// 所有 lego provider 都能在同一个进程内完成 present/cleanup，因此统一声明
// verify-write-v1；只有实现 dns.NativeVerifier 的服务商才声明只读的 verify-v1。
func providerCapabilities(id string, readEnabled, writeEnabled bool) []string {
	caps := make([]string, 0, 2)
	if writeEnabled {
		caps = append(caps, protocol.FeatureVerifyWrite)
	}
	if readEnabled && dns.IsNativeVerifiable(id) {
		caps = append(caps, protocol.FeatureVerify)
	}
	if len(caps) == 0 {
		return nil
	}
	return caps
}

func manifestFields(fields []catalog.Field) []protocol.DNSField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]protocol.DNSField, 0, len(fields))
	for _, field := range fields {
		item := protocol.DNSField{
			Key:             field.Key,
			Label:           field.LocalizedLabel(i18n.Default),
			LabelI18n:       localizedTextMap(func(locale i18n.Locale) string { return field.LocalizedLabel(locale) }),
			Type:            field.Type,
			Required:        field.Required,
			Secret:          field.Secret,
			Placeholder:     field.LocalizedPlaceholder(i18n.Default),
			PlaceholderI18n: localizedTextMap(func(locale i18n.Locale) string { return field.LocalizedPlaceholder(locale) }),
			Help:            field.LocalizedHelp(i18n.Default),
			HelpI18n:        localizedTextMap(func(locale i18n.Locale) string { return field.LocalizedHelp(locale) }),
			Default:         field.Default,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, protocol.DNSOption{
				Label:     option.LocalizedLabel(i18n.Default),
				LabelI18n: localizedTextMap(func(locale i18n.Locale) string { return option.LocalizedLabel(locale) }),
				Value:     option.Value,
			})
		}
		if field.ShowIf != nil {
			item.ShowIf = &protocol.DNSCondition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out = append(out, item)
	}
	return out
}

func localizedTextMap(text func(i18n.Locale) string) map[string]string {
	locales := i18n.Supported()
	out := make(map[string]string, len(locales))
	for _, locale := range locales {
		out[string(locale)] = text(locale)
	}
	return out
}

func handle(action string) {
	var req protocol.DNSRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Provider == "" {
		helper.Fail("请求缺少 provider")
	}
	plugin, ok := dns.LookupNative(req.Provider)
	if !ok {
		helper.Fail("不支持的服务商: " + req.Provider)
	}
	def, ok := meta.LookupDNS(plugin.ID())
	if !ok {
		helper.Fail("服务商元数据缺失: " + plugin.ID())
	}
	envs := catalog.EnvForProvider(def, req.Config)
	for key := range envs {
		_ = os.Unsetenv(key)
	}
	for key, value := range envs {
		if value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			helper.Fail("设置环境变量失败: " + err.Error())
		}
	}
	provider, err := plugin.New(req.Config)
	if err != nil {
		helper.Fail("初始化服务商失败: " + err.Error())
	}
	if action == "verify" {
		handleVerify(provider, req)
		return
	}
	provider = dnsstate.Wrap(plugin.ID(), provider)
	if err := run(provider, action, req); err != nil {
		helper.Fail(err.Error())
	}
}

func handleVerify(provider dns.NativeChallengeProvider, req protocol.DNSRequest) {
	loc := i18n.Normalize(firstNonEmpty(req.Locale, string(i18n.Default)))
	if strings.EqualFold(strings.TrimSpace(req.Mode), "write") {
		writeVerifyResponse(verifyWrite(provider, req.Domain, loc))
		return
	}
	response := protocol.VerifyResponse{OK: true, Checks: make([]protocol.VerifyCheck, 0, 2)}
	verifier, ok := provider.(dns.NativeVerifier)
	if !ok {
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "verify", Status: protocol.VerifyStatusUnsupported,
			Message: i18n.T(loc, "service.verify.unsupported", nil),
		})
		writeVerifyResponse(response)
		return
	}
	zone, err := verifier.Verify(context.Background(), req.Domain)
	if err != nil {
		response.OK = false
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "zone", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.zone_failed", map[string]any{"domain": req.Domain, "error": err.Error()}),
		})
	} else {
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "zone", Status: protocol.VerifyStatusOK,
			Message: i18n.T(loc, "service.verify.zone_ok", map[string]any{"domain": req.Domain, "zone": zone}),
		})
	}
	writeVerifyResponse(response)
}

// verifyWrite 在同一个 helper 进程内完成临时 TXT 的写入与清理。
// 相比宿主分别调用 present/cleanup，provider 只构造一次，鉴权、zone 查询与
// HTTP 连接都可以复用；清理失败仍作为独立检查返回，不能伪装成验证通过。
func verifyWrite(provider dns.NativeChallengeProvider, domain string, loc i18n.Locale) protocol.VerifyResponse {
	response := protocol.VerifyResponse{OK: true, Checks: make([]protocol.VerifyCheck, 0, 2)}
	token, keyAuth, err := util.VerificationToken()
	if err != nil {
		response.OK = false
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "write", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.token_failed", map[string]any{"error": err.Error()}),
		})
		return response
	}
	if err := provider.Present(domain, token, keyAuth); err != nil {
		response.OK = false
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "write", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.write_failed", map[string]any{"error": err.Error()}),
		})
		return response
	}
	response.Checks = append(response.Checks, protocol.VerifyCheck{Key: "write", Status: protocol.VerifyStatusOK})
	if err := provider.CleanUp(domain, token, keyAuth); err != nil {
		response.OK = false
		response.Checks = append(response.Checks, protocol.VerifyCheck{
			Key: "cleanup", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.cleanup_failed", map[string]any{"domain": domain, "error": err.Error()}),
		})
		return response
	}
	response.Checks = append(response.Checks, protocol.VerifyCheck{Key: "cleanup", Status: protocol.VerifyStatusOK})
	return response
}

func writeVerifyResponse(response protocol.VerifyResponse) {
	if err := helper.WriteJSON(response); err != nil {
		helper.Fail("输出验证结果失败: " + err.Error())
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func run(provider dns.NativeChallengeProvider, action string, req protocol.DNSRequest) error {
	switch action {
	case "present":
		return provider.Present(req.Domain, req.Token, req.KeyAuth)
	case "cleanup":
		return provider.CleanUp(req.Domain, req.Token, req.KeyAuth)
	default:
		return fmt.Errorf("未知动作: %s", action)
	}
}

// buildVersion 由 pluginctl 通过 -ldflags 注入，必须与仓库索引中的版本一致。
var buildVersion = ""

func serviceVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
