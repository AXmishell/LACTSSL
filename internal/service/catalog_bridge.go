package service

import (
	"github.com/AXmishell/LACTSSL-Plugins/catalog"
	plugini18n "github.com/AXmishell/LACTSSL-Plugins/i18n"
)

// DNSProviders 是 DNS 服务商元数据目录，字段映射到 lego 所需的环境变量。
// 数据由 LACTSSL-Plugins 仓库统一维护，宿主只负责转换为前端使用的结构。
var DNSProviders = buildDNSProviders()

// PluginDeployTypes 是改由部署插件提供的部署目标元数据。
// 与 DNSProviders 一样，数据来自 LACTSSL-Plugins 仓库。
var PluginDeployTypes = buildPluginDeployTypes()

// DeprecatedDeployTypeCanonical 记录已合并的旧部署目标与新的规范目标。
// 旧 ID 仍保留元数据与插件实现，用于历史凭据、旧任务和旧 helper 的兼容。
var DeprecatedDeployTypeCanonical = catalog.DeprecatedDeployTypes()

// builtinDNSProviderIDs 记录迁移前直接编译进主程序的 DNS 服务商 ID。
// 这些服务商现在由 DNS 插件提供，但仍默认启用，保证升级后行为不变。
var builtinDNSProviderIDs = []string{
	"alidns",
	"aliesa",
	"cloudflare",
	"dnspod",
	"tencentcloud",
	"huaweicloud",
	"volcengine",
	"baiducloud",
	"jdcloud",
	"digitalocean",
	"godaddy",
	"namecheap",
	"namesilo",
	"porkbun",
	"spaceship",
	"cloudns",
	"bunny",
	"rainyun",
	"westcn",
	"pdns",
	"dnsimple",
	"dnsmadeeasy",
	"route53",
	"gcloud",
	"azuredns",
	"netlify",
	"vercel",
	"duckdns",
	"freemyip",
	"vultr",
	"linode",
	"hetzner",
	"gandi",
	"gandiv5",
	"rfc2136",
}

// BuiltinDNSProviderIDs 返回迁移前内置的 DNS 服务商 ID，
// 供插件启用白名单初始化默认值。
func BuiltinDNSProviderIDs() []string {
	return append([]string(nil), builtinDNSProviderIDs...)
}

func buildDNSProviders() []ProviderDef {
	list := catalog.DNSProviders()
	out := make([]ProviderDef, 0, len(list))
	for _, item := range list {
		out = append(out, fromCatalogProvider(item))
	}
	return out
}

func buildPluginDeployTypes() []ProviderDef {
	list := catalog.Deployers()
	out := make([]ProviderDef, 0, len(list))
	for _, item := range list {
		out = append(out, fromCatalogProvider(item))
	}
	return out
}

func fromCatalogProvider(item catalog.Provider) ProviderDef {
	return ProviderDef{
		Value:        item.Value,
		Label:        item.Label,
		LabelKey:     item.LabelKey,
		Desc:         item.Desc,
		DescKey:      item.DescKey,
		Deprecated:   item.Deprecated,
		Fields:       fromCatalogFields(item.Fields),
		DeployFields: fromCatalogFields(item.DeployFields),
		labelI18n: catalogTextMap(func(locale plugini18n.Locale) string {
			return item.LocalizedName(locale)
		}),
		descI18n: catalogTextMap(func(locale plugini18n.Locale) string {
			return item.LocalizedDesc(locale)
		}),
	}
}

func fromCatalogFields(fields []catalog.Field) []FieldDef {
	if len(fields) == 0 {
		return nil
	}
	out := make([]FieldDef, 0, len(fields))
	for _, field := range fields {
		item := FieldDef{
			Key:            field.Key,
			Label:          field.Label,
			LabelKey:       field.LabelKey,
			Type:           field.Type,
			Required:       field.Required,
			Secret:         field.Secret,
			Placeholder:    field.Placeholder,
			PlaceholderKey: field.PlaceholderKey,
			Help:           field.Help,
			HelpKey:        field.HelpKey,
			Default:        field.Default,
			Env:            field.Env,
			labelI18n: catalogTextMap(func(locale plugini18n.Locale) string {
				return field.LocalizedLabel(locale)
			}),
			placeholderI18n: catalogTextMap(func(locale plugini18n.Locale) string {
				return field.LocalizedPlaceholder(locale)
			}),
			helpI18n: catalogTextMap(func(locale plugini18n.Locale) string {
				return field.LocalizedHelp(locale)
			}),
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, Option{
				Label:    option.Label,
				LabelKey: option.LabelKey,
				Value:    option.Value,
				labelI18n: catalogTextMap(func(locale plugini18n.Locale) string {
					return option.LocalizedLabel(locale)
				}),
			})
		}
		if field.ShowIf != nil {
			item.ShowIf = &Condition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out = append(out, item)
	}
	return out
}

// catalogTextMap 把插件仓库 catalog 的文案按语言展开。
// 主程序不再为插件服务商/部署目标维护 meta.* 翻译，直接使用插件侧资源。
func catalogTextMap(text func(plugini18n.Locale) string) map[string]string {
	locales := plugini18n.Supported()
	out := make(map[string]string, len(locales))
	for _, locale := range locales {
		out[string(locale)] = text(locale)
	}
	return out
}
