// Command deploy-helper 是 LACTSSL 证书部署插件协议的官方实现。
//
// 主程序通过一次性子进程调用它：
//
//	deploy-helper manifest   # 输出插件清单 JSON
//	deploy-helper deploy     # 从 stdin 读取 JSON 请求并执行部署
//	deploy-helper options    # 读取部署字段的动态选项
//	deploy-helper plan       # 读取可部署对象，供批量部署按证书域名匹配
//	deploy-helper verify     # 执行只读凭据验证
package main

import (
	"context"
	"os"
	"runtime/debug"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/helper"
	"lactssl/internal/plugin/catalog"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/providers/panels"
)

func main() {
	defer helper.Recover()
	panels.RegisterBaota()
	panels.RegisterOnePanel()
	panels.RegisterSafeline()
	helper.ApplyMemoryLimit()
	if len(os.Args) < 2 {
		helper.Fail("用法: deploy-helper manifest|deploy|options|plan|verify")
	}
	switch os.Args[1] {
	case "manifest", "list":
		writeManifest()
	case "deploy":
		handle()
	case "options":
		handleOptions()
	case "plan":
		handlePlan()
	case "verify":
		handleVerify()
	default:
		helper.Fail("未知子命令: " + os.Args[1])
	}
}

func writeManifest() {
	if err := helper.WriteJSON(buildManifest()); err != nil {
		helper.Fail("输出 manifest 失败: " + err.Error())
	}
}

// buildManifest 按主程序声明的特性输出部署目标。
// 新版主程序只接收合并后的规范 ID；旧版主程序仍接收旧 ID，避免版本错配后
// 七牛云、宝塔或 1Panel 目标整体不可用。
func buildManifest() protocol.DeployManifest {
	hasFeature := func(name string) bool {
		return helper.HasFeatureIn(protocol.DeployFeaturesEnv, name)
	}
	merged := hasFeature(protocol.FeatureMergedDeployers)
	dynamicOptions := hasFeature(protocol.FeatureDynamicOptions)
	verifyEnabled := hasFeature(protocol.FeatureVerify)
	planEnabled := hasFeature(protocol.FeaturePlan)
	deprecated := catalog.DeprecatedDeployTypes()
	mergedCanonical := make(map[string]bool)
	if !merged {
		for _, canonical := range deprecated {
			mergedCanonical[canonical] = true
		}
	}

	plugins := deploy.ListNative()
	manifest := protocol.DeployManifest{
		Protocol:  protocol.ProtocolVersion,
		Name:      "lactssl deploy helper",
		Version:   serviceVersion(),
		Deployers: make([]protocol.Deployer, 0, len(plugins)),
	}
	if verifyEnabled {
		manifest.Capabilities = []string{protocol.FeatureVerify}
	}
	if planEnabled {
		manifest.Capabilities = append(manifest.Capabilities, protocol.FeaturePlan)
	}
	for _, plugin := range plugins {
		def, ok := catalog.LookupDeployer(plugin.ID())
		if !ok {
			continue
		}
		if merged && def.Deprecated {
			continue
		}
		// 旧宿主不认识合并后的 target 字段。对 safeline、acepanel 这类
		// 规范 ID 在合并前就存在的目标，继续输出规范 ID，避免旧宿主失去原功能。
		if !merged && mergedCanonical[plugin.ID()] && !def.LegacyKeep {
			continue
		}
		manifest.Deployers = append(manifest.Deployers, protocol.Deployer{
			ID:              plugin.ID(),
			Name:            def.LocalizedName(i18n.Default),
			NameI18n:        localizedTextMap(func(locale i18n.Locale) string { return def.LocalizedName(locale) }),
			Description:     def.LocalizedDesc(i18n.Default),
			DescriptionI18n: localizedTextMap(func(locale i18n.Locale) string { return def.LocalizedDesc(locale) }),
			Aliases:         plugin.Aliases(),
			Fields:          manifestFields(def.Fields, dynamicOptions),
			DeployFields:    manifestFields(def.DeployFields, dynamicOptions),
			Capabilities:    deployerCapabilities(plugin.ID(), verifyEnabled, planEnabled),
		})
	}
	return manifest
}

// deployerCapabilities 只给实现了只读探测或预匹配的目标声明对应能力。
func deployerCapabilities(id string, verifyEnabled, planEnabled bool) []string {
	out := make([]string, 0, 2)
	if verifyEnabled && deploy.IsNativeVerifiable(id) {
		out = append(out, protocol.FeatureVerify)
	}
	if planEnabled && deploy.IsNativePlannable(id) {
		out = append(out, protocol.FeaturePlan)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func manifestFields(fields []catalog.Field, dynamicOptions bool) []protocol.DeployField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]protocol.DeployField, 0, len(fields))
	for _, field := range fields {
		item := protocol.DeployField{
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
			item.Options = append(item.Options, protocol.DeployOption{
				Label:     option.LocalizedLabel(i18n.Default),
				LabelI18n: localizedTextMap(func(locale i18n.Locale) string { return option.LocalizedLabel(locale) }),
				Value:     option.Value,
			})
		}
		if field.ShowIf != nil {
			item.ShowIf = &protocol.DeployCondition{Key: field.ShowIf.Key, Value: field.ShowIf.Value, Values: field.ShowIf.Values}
		}
		// 旧版宿主不认识动态选项字段，声明特性后才输出，保持 manifest 兼容。
		if dynamicOptions {
			item.OptionsSource = field.OptionsSource
			item.Multiple = field.Multiple
		}
		out = append(out, item)
	}
	return out
}

// localizedTextMap 把一组文案按插件支持的语言展开，供宿主直接按 locale 选择。
// 默认语言条目始终存在；没有翻译时 Localized* 会回退到默认语言。
func localizedTextMap(text func(i18n.Locale) string) map[string]string {
	locales := i18n.Supported()
	out := make(map[string]string, len(locales))
	for _, locale := range locales {
		out[string(locale)] = text(locale)
	}
	return out
}

func handle() {
	var req protocol.DeployRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != protocol.ProtocolVersion {
		helper.Fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Provider == "" {
		helper.Fail("请求缺少 provider")
	}
	plugin, ok := deploy.LookupNative(req.Provider)
	if !ok {
		helper.Fail("不支持的部署目标: " + req.Provider)
	}
	if err := plugin.Deploy(context.Background(), deploy.NativeRequest{
		Auth:        req.Auth,
		Config:      req.Config,
		Certificate: req.Certificate,
	}); err != nil {
		helper.Fail(err.Error())
	}
}

// handleVerify 执行只读探测。未实现 Verifier 的目标返回结构化 unsupported，
// 宿主据此显示“该插件版本不支持验证”，而不是误报权限失败。
func handleVerify() {
	var req protocol.DeployRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != protocol.ProtocolVersion {
		helper.Fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Provider == "" {
		helper.Fail("请求缺少 provider")
	}
	loc := i18n.Normalize(firstNonEmpty(req.Locale, string(i18n.Default)))
	plugin, ok := deploy.LookupNative(req.Provider)
	if !ok {
		helper.Fail("不支持的部署目标: " + req.Provider)
	}
	verifier, ok := plugin.(deploy.NativeVerifier)
	if !ok {
		response := protocol.VerifyResponse{OK: true, Checks: []protocol.VerifyCheck{{
			Key: "verify", Status: protocol.VerifyStatusUnsupported,
			Message: i18n.T(loc, "service.verify.unsupported", nil),
		}}}
		if err := helper.WriteJSON(response); err != nil {
			helper.Fail("输出验证结果失败: " + err.Error())
		}
		return
	}
	checks, err := verifier.Verify(context.Background(), deploy.NativeRequest{
		Auth:   req.Auth,
		Config: req.Config,
		Locale: string(loc),
	})
	if err != nil {
		checks = append(checks, protocol.VerifyCheck{
			Key: "verify", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}),
		})
	}
	if checks == nil {
		checks = []protocol.VerifyCheck{}
	}
	response := protocol.VerifyResponse{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == protocol.VerifyStatusFailed || check.Status == protocol.VerifyStatusError {
			response.OK = false
			break
		}
	}
	if err := helper.WriteJSON(response); err != nil {
		helper.Fail("输出验证结果失败: " + err.Error())
	}
}

// handleOptions 读取部署字段的动态选项，例如宝塔面板中的网站列表。
func handleOptions() {
	var req protocol.DeployOptionsRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != protocol.ProtocolVersion {
		helper.Fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Provider == "" {
		helper.Fail("请求缺少 provider")
	}
	if req.Field == "" {
		helper.Fail("请求缺少 field")
	}
	plugin, ok := deploy.LookupNative(req.Provider)
	if !ok {
		helper.Fail("不支持的部署目标: " + req.Provider)
	}
	lister, ok := plugin.(deploy.NativeOptionLister)
	if !ok {
		helper.Fail(i18n.E("service.deploy.options_unsupported").Error())
	}
	options, err := lister.ListOptions(context.Background(), deploy.NativeOptionsRequest{
		Field:  req.Field,
		Source: req.Source,
		Query:  req.Query,
		Auth:   req.Auth,
		Config: req.Config,
	})
	if err != nil {
		helper.Fail(err.Error())
	}
	if options == nil {
		options = []protocol.DeployOption{}
	}
	if err := helper.WriteJSON(protocol.DeployOptionsResponse{Options: options}); err != nil {
		helper.Fail("输出选项失败: " + err.Error())
	}
}

// handlePlan 读取目标系统中可部署的对象清单。该操作只读，不写入证书。
func handlePlan() {
	var req protocol.DeployPlanRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != protocol.ProtocolVersion {
		helper.Fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Provider == "" {
		helper.Fail("请求缺少 provider")
	}
	plugin, ok := deploy.LookupNative(req.Provider)
	if !ok {
		helper.Fail("不支持的部署目标: " + req.Provider)
	}
	planner, ok := plugin.(deploy.NativePlanner)
	if !ok {
		helper.Fail(i18n.E("service.deploy.plan_unsupported").Error())
	}
	loc := i18n.Normalize(firstNonEmpty(req.Locale, string(i18n.Default)))
	result, err := planner.Plan(context.Background(), deploy.NativePlanRequest{
		Auth:   req.Auth,
		Config: req.Config,
		Locale: string(loc),
	})
	if err != nil {
		helper.Fail(err.Error())
	}
	response := protocol.DeployPlanResponse{
		OK:       true,
		Field:    result.Field,
		Multiple: result.Multiple,
		Objects:  make([]protocol.DeployPlanObject, 0, len(result.Objects)),
	}
	for _, object := range result.Objects {
		response.Objects = append(response.Objects, protocol.DeployPlanObject{
			Value:       object.Value,
			Name:        object.Name,
			Domains:     object.Domains,
			Identifiers: object.Identifiers,
		})
	}
	if err := helper.WriteJSON(response); err != nil {
		helper.Fail("输出部署对象失败: " + err.Error())
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

// buildVersion 由 pluginctl 通过 -ldflags 注入，必须与仓库索引中的版本一致。
var buildVersion = ""

func serviceVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimSpace(info.Main.Version)
	}
	return "dev"
}
