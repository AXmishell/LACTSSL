package service

import (
	"strings"

	"lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/meta"
)

// deprecatedDeployTypeReplaced 判断旧目标的合并目标及对应部署对象是否已经可用。
// 只判断规范 ID 是否存在还不够：旧 helper 可能已有 safeline/acepanel/baota，
// 但不支持合并后新增的 panel、portal、docker 等 target。
func deprecatedDeployTypeReplaced(value string) bool {
	canonical, ok := DeprecatedDeployTypeCanonical[value]
	if !ok {
		return false
	}
	if rule, found := deployTargetMergeRuleFor(value); found {
		return deployTargetSupports(rule.Canonical, rule.ModeKey, rule.ModeValue)
	}
	return pluginDeployTypeAvailable(canonical)
}

// DeprecatedDeployTypeReplaced 供 API 层隐藏旧目标时复用同一套能力判断。
func DeprecatedDeployTypeReplaced(value string) bool {
	return deprecatedDeployTypeReplaced(value)
}

// deployTargetSupports 判断已加载的 helper 是否声明了规范目标的某个部署对象。
// 不能使用 manifest 缓存：缓存只用于展示与脱敏，插件未安装时不能据此迁移数据。
func deployTargetSupports(canonical, key, value string) bool {
	external, ok := deployplugin.ExternalDeployerInfo(canonical)
	if !ok {
		return false
	}
	return deployFieldsSupport(external.DeployFields, key, value)
}

func deployFieldsSupport(fields []deployplugin.ManifestField, key, value string) bool {
	for _, field := range fields {
		if field.Key != key {
			continue
		}
		if value == "" {
			return true
		}
		for _, option := range field.Options {
			if option.Value == value {
				return true
			}
		}
		return false
	}
	return false
}

// PluginDeployTypeInfo 只查找由部署插件提供（或缓存）的类型元数据。
func PluginDeployTypeInfo(value string) (ProviderDef, bool) {
	value = strings.TrimSpace(value)
	lookup := value
	if canonical, ok := DeprecatedDeployTypeCanonical[strings.ToLower(value)]; ok {
		lookup = canonical
	}
	if external, ok := deployplugin.ExternalDeployerInfo(lookup); ok {
		return externalDeployerDef(external), true
	}
	if deployer, ok := pluginmeta.Deployer(lookup); ok {
		return manifestDeployerDef(deployer), true
	}
	if lookup != value {
		if deployer, ok := pluginmeta.Deployer(value); ok {
			return manifestDeployerDef(deployer), true
		}
	}
	return ProviderDef{}, false
}

// pluginDeployTypeAvailable 判断插件目标是否已由外部 helper 提供。
func pluginDeployTypeAvailable(id string) bool {
	return deployplugin.Has(id)
}
