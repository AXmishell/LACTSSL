package service

import (
	"strings"

	"lactssl/internal/deployplugin"
)

// DeprecatedDeployTypeCanonical 记录已合并的旧部署目标与新的规范目标。
// 旧 ID 仍保留元数据与插件实现，用于历史凭据、旧任务和旧 helper 的兼容。
var DeprecatedDeployTypeCanonical = map[string]string{
	"qiniu_cdn":    "qiniu",
	"qiniu_kodo":   "qiniu",
	"baota_site":   "baota",
	"baota_panel":  "baota",
	"1panel_site":  "1panel",
	"1panel_panel": "1panel",
}

// deprecatedDeployTypeReplaced 判断旧目标的合并目标是否已经可用。
func deprecatedDeployTypeReplaced(value string) bool {
	canonical, ok := DeprecatedDeployTypeCanonical[value]
	if !ok {
		return false
	}
	return pluginDeployTypeAvailable(canonical)
}

// PluginDeployTypes 是改由部署插件提供的部署目标元数据。
//
// 这些目标不编译进主程序：主程序只内置本地文件与 SSH，其余目标由
// cmd/deploy-helper 通过 exec 插件协议提供。元数据保留在这里，用于：
//   - 生成部署 helper 的 manifest；
//   - 历史凭据的字段脱敏与部署参数迁移，即使插件暂时未安装也能识别。
//
// 七牛云 CDN/Kodo、宝塔网站/面板 SSL、1Panel 网站/面板已合并为单一目标，
// 旧 ID 作为 Deprecated 条目保留，合并目标可用时不会出现在部署类型列表中。
var PluginDeployTypes = []ProviderDef{
	{
		Value: "ftp", LabelKey: "meta.deploy.ftp.label", DescKey: "meta.deploy.ftp.desc",
		Fields: []FieldDef{
			{Key: "host", LabelKey: "meta.field.host.label", PlaceholderKey: "meta.field.host.placeholder", Type: "text", Required: true},
			{Key: "port", LabelKey: "meta.field.ftp_port.label", Type: "number", Default: "21"},
			{Key: "user", LabelKey: "meta.field.user.label", PlaceholderKey: "meta.field.user.placeholder", Type: "text", Required: true},
			{Key: "password", LabelKey: "meta.field.password.label", Type: "password", Required: true},
			{Key: "tls", LabelKey: "meta.field.ftp_tls.label", HelpKey: "meta.field.ftp_tls.help", Type: "select", Default: "explicit", Options: []Option{
				{LabelKey: "meta.field.ftp_tls.option.none", Value: "none"},
				{LabelKey: "meta.field.ftp_tls.option.explicit", Value: "explicit"},
				{LabelKey: "meta.field.ftp_tls.option.implicit", Value: "implicit"},
			}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", HelpKey: "meta.field.ftp_skip_verify.help", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "cert_path", LabelKey: "meta.field.remote_cert_path.label", PlaceholderKey: "meta.field.remote_cert_path.placeholder", Type: "text", Required: true},
			{Key: "key_path", LabelKey: "meta.field.remote_key_path.label", PlaceholderKey: "meta.field.remote_key_path.placeholder", Type: "text", Required: true},
		},
	},
	{
		Value: "webhook", LabelKey: "meta.deploy.webhook.label", DescKey: "meta.deploy.webhook.desc",
		Fields: []FieldDef{
			{Key: "url", LabelKey: "meta.field.url.label", PlaceholderKey: "meta.field.url.placeholder", Type: "text", Required: true},
			{Key: "auth_header", LabelKey: "meta.field.auth_header.label", HelpKey: "meta.field.auth_header.help", Type: "password"},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
	},
	{
		Value: "aliyun_cdn", LabelKey: "meta.deploy.aliyun_cdn.label", DescKey: "meta.deploy.aliyun_cdn.desc",
		Fields: []FieldDef{
			{Key: "access_key_id", LabelKey: "meta.field.access_key_id.label", Type: "text", Required: true},
			{Key: "access_key_secret", LabelKey: "meta.field.access_key_secret.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.domains.label", HelpKey: "meta.field.domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "qiniu", LabelKey: "meta.deploy.qiniu.label", DescKey: "meta.deploy.qiniu.desc",
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domain_type", LabelKey: "meta.field.qiniu_domain_type.label", HelpKey: "meta.field.qiniu_domain_type.help", Type: "select", Default: "cdn", Options: []Option{
				{LabelKey: "meta.field.qiniu_domain_type.option.cdn", Value: "cdn"},
				{LabelKey: "meta.field.qiniu_domain_type.option.kodo", Value: "kodo"},
			}},
			{Key: "domains", LabelKey: "meta.field.qiniu_domains.label", HelpKey: "meta.field.qiniu_domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "qiniu_cdn", LabelKey: "meta.deploy.qiniu_cdn.label", DescKey: "meta.deploy.qiniu_cdn.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.domains.label", HelpKey: "meta.field.domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "qiniu_kodo", LabelKey: "meta.deploy.qiniu_kodo.label", DescKey: "meta.deploy.qiniu_kodo.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "access_key", LabelKey: "meta.field.access_key.label", Type: "text", Required: true},
			{Key: "secret_key", LabelKey: "meta.field.secret_key.label", Type: "password", Required: true},
		},
		DeployFields: []FieldDef{
			{Key: "domains", LabelKey: "meta.field.kodo_domains.label", HelpKey: "meta.field.kodo_domains.help", Type: "text"},
			{Key: "cert_name", LabelKey: "meta.field.cert_name.label", HelpKey: "meta.field.cert_name.help", Type: "text"},
		},
	},
	{
		Value: "baota", LabelKey: "meta.deploy.baota.label", DescKey: "meta.deploy.baota.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.panel_api_secret.label", HelpKey: "meta.field.panel_api_secret.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "target", LabelKey: "meta.field.deploy_target.label", HelpKey: "meta.field.deploy_target.help", Type: "select", Default: "site", Options: []Option{
				{LabelKey: "meta.field.deploy_target.option.site", Value: "site"},
				{LabelKey: "meta.field.deploy_target.option.panel", Value: "panel"},
			}},
			{Key: "sites", LabelKey: "meta.field.sites.label", HelpKey: "meta.field.sites.help", Type: "text", ShowIf: &Condition{Key: "target", Value: "site"}},
		},
	},
	{
		Value: "baota_site", LabelKey: "meta.deploy.baota_site.label", DescKey: "meta.deploy.baota_site.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.panel_api_secret.label", HelpKey: "meta.field.panel_api_secret.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "sites", LabelKey: "meta.field.sites.label", HelpKey: "meta.field.sites.help", Type: "text"},
		},
	},
	{
		Value: "baota_panel", LabelKey: "meta.deploy.baota_panel.label", DescKey: "meta.deploy.baota_panel.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_secret", LabelKey: "meta.field.panel_api_secret.label", HelpKey: "meta.field.panel_api_secret.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
	},
	{
		Value: "1panel", LabelKey: "meta.deploy.1panel.label", DescKey: "meta.deploy.1panel.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.panel_api_key.label", HelpKey: "meta.field.panel_api_key.help", Type: "password", Required: true},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", Type: "select", Default: "v2", Options: []Option{{Label: "v2", Value: "v2"}, {Label: "v1", Value: "v1"}}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "target", LabelKey: "meta.field.deploy_target.label", HelpKey: "meta.field.deploy_target.help", Type: "select", Default: "site", Options: []Option{
				{LabelKey: "meta.field.deploy_target.option.site", Value: "site"},
				{LabelKey: "meta.field.deploy_target.option.panel", Value: "panel"},
			}},
			{Key: "website", LabelKey: "meta.field.website.label", HelpKey: "meta.field.website.help", Type: "text", ShowIf: &Condition{Key: "target", Value: "site"}},
			{Key: "ssl_mode", LabelKey: "meta.field.ssl_mode.label", HelpKey: "meta.field.ssl_mode.help", Type: "select", Default: "Enable", ShowIf: &Condition{Key: "target", Value: "panel"}, Options: []Option{{LabelKey: "meta.field.ssl_mode.option.enable", Value: "Enable"}, {LabelKey: "meta.field.ssl_mode.option.mux", Value: "Mux"}}},
		},
	},
	{
		Value: "1panel_site", LabelKey: "meta.deploy.1panel_site.label", DescKey: "meta.deploy.1panel_site.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.panel_api_key.label", HelpKey: "meta.field.panel_api_key.help", Type: "password", Required: true},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", Type: "select", Default: "v2", Options: []Option{{Label: "v2", Value: "v2"}, {Label: "v1", Value: "v1"}}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "website", LabelKey: "meta.field.website.label", HelpKey: "meta.field.website.help", Type: "text"},
		},
	},
	{
		Value: "1panel_panel", LabelKey: "meta.deploy.1panel_panel.label", DescKey: "meta.deploy.1panel_panel.desc", Deprecated: true,
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_key", LabelKey: "meta.field.panel_api_key.label", HelpKey: "meta.field.panel_api_key.help", Type: "password", Required: true},
			{Key: "api_version", LabelKey: "meta.field.api_version.label", Type: "select", Default: "v2", Options: []Option{{Label: "v2", Value: "v2"}, {Label: "v1", Value: "v1"}}},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "ssl_mode", LabelKey: "meta.field.ssl_mode.label", HelpKey: "meta.field.ssl_mode.help", Type: "select", Default: "Enable", Options: []Option{{LabelKey: "meta.field.ssl_mode.option.enable", Value: "Enable"}, {LabelKey: "meta.field.ssl_mode.option.mux", Value: "Mux"}}},
		},
	},
	{
		Value: "safeline", LabelKey: "meta.deploy.safeline.label", DescKey: "meta.deploy.safeline.desc",
		Fields: []FieldDef{
			{Key: "panel_url", LabelKey: "meta.field.panel_url.label", PlaceholderKey: "meta.field.panel_url.placeholder", HelpKey: "meta.field.panel_url.help", Type: "text", Required: true},
			{Key: "api_token", LabelKey: "meta.field.panel_api_token.label", HelpKey: "meta.field.panel_api_token.help", Type: "password", Required: true},
			{Key: "skip_verify", LabelKey: "meta.field.skip_verify.label", Type: "select", Default: "false", Options: []Option{{LabelKey: "meta.field.skip_verify.option.false", Value: "false"}, {LabelKey: "meta.field.skip_verify.option.true", Value: "true"}}},
		},
		DeployFields: []FieldDef{
			{Key: "cert_ids", LabelKey: "meta.field.cert_ids.label", HelpKey: "meta.field.cert_ids.help", Type: "text"},
		},
	},
}

// PluginDeployTypeInfo 只查找由部署插件提供的类型元数据。
func PluginDeployTypeInfo(value string) (ProviderDef, bool) {
	value = strings.TrimSpace(value)
	for _, p := range PluginDeployTypes {
		if p.Value == value {
			return p, true
		}
	}
	return ProviderDef{}, false
}

// pluginDeployTypeAvailable 判断插件目标是否已由外部 helper 提供。
func pluginDeployTypeAvailable(id string) bool {
	return deployplugin.Has(id)
}
