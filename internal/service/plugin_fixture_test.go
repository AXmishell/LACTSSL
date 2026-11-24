package service

import (
	"encoding/json"
	"os"
	"testing"

	"lactssl/internal/plugin/proto"
	"lactssl/internal/plugin/runtime/metadata"
)

// TestMain 为 service 包提供一份离线 manifest 缓存基线。
// 宿主不再编译插件 catalog，历史账号/迁移相关的元数据来自运行期缓存；
// 测试用与真实插件 manifest 相同的结构注入，避免依赖插件仓库源码。
func TestMain(m *testing.M) {
	seedPluginMetadata()
	code := m.Run()
	pluginmeta.Reset()
	os.Exit(code)
}

func seedPluginMetadata() {
	pluginmeta.Reset()
	for _, id := range BuiltinDNSProviderIDs() {
		pluginmeta.Upsert(pluginmeta.Entry{
			Kind: "dns", ID: id, Version: "test", Name: id,
			Raw: mustJSON(pluginproto.DNSProvider{
				ID:   id,
				Name: id,
				Fields: []pluginproto.DNSField{
					{Key: "api_token", Label: "API Token", Type: "password", Secret: true},
				},
			}),
		})
	}
	pluginmeta.Upsert(pluginmeta.Entry{
		Kind: "dns", ID: "ovh", Version: "test", Name: "OVH",
		Raw: mustJSON(pluginproto.DNSProvider{
			ID:              "ovh",
			Name:            "OVH",
			NameI18n:        map[string]string{"zh-CN": "OVH", "en-US": "OVH Cloud"},
			Description:     "OVH DNS",
			DescriptionI18n: map[string]string{"zh-CN": "OVH DNS", "en-US": "OVH DNS API"},
			Fields: []pluginproto.DNSField{
				{
					Key:             "endpoint",
					Label:           "接入点",
					LabelI18n:       map[string]string{"zh-CN": "接入点", "en-US": "Endpoint"},
					Type:            "select",
					Placeholder:     "ovh-eu",
					PlaceholderI18n: map[string]string{"zh-CN": "ovh-eu", "en-US": "ovh-eu"},
					Help:            "选择 OVH 区域",
					HelpI18n:        map[string]string{"zh-CN": "选择 OVH 区域", "en-US": "Select the OVH region"},
					Default:         "ovh-eu",
					Options: []pluginproto.DNSOption{
						{Label: "欧洲", LabelI18n: map[string]string{"zh-CN": "欧洲", "en-US": "Europe"}, Value: "ovh-eu"},
					},
				},
			},
		}),
	})

	for _, id := range DefaultPluginDeployIDs() {
		pluginmeta.Upsert(pluginmeta.Entry{
			Kind: "deploy", ID: id, Version: "test", Name: id,
			Raw: mustJSON(pluginproto.Deployer{
				ID:   id,
				Name: id,
				Fields: []pluginproto.DeployField{
					{Key: "api_token", Label: "API Token", Type: "password", Secret: true},
				},
				DeployFields: []pluginproto.DeployField{
					{Key: "sites", Label: "Sites", Type: "text"},
				},
			}),
		})
	}
	pluginmeta.Upsert(pluginmeta.Entry{
		Kind: "deploy", ID: "ftp", Version: "test", Name: "FTP",
		Raw: mustJSON(pluginproto.Deployer{
			ID:   "ftp",
			Name: "FTP",
			Fields: []pluginproto.DeployField{
				{Key: "host", Label: "主机", Type: "text", Required: true},
				{Key: "user", Label: "用户名", Type: "text", Required: true},
				{Key: "password", Label: "密码", Type: "password", Secret: true, Required: true},
			},
			DeployFields: []pluginproto.DeployField{
				{Key: "cert_path", Label: "证书路径", Type: "text", Required: true},
				{Key: "key_path", Label: "私钥路径", Type: "text", Required: true},
			},
		}),
	})
	pluginmeta.Upsert(pluginmeta.Entry{
		Kind: "deploy", ID: "aliyun", Version: "test", Name: "阿里云",
		Raw: mustJSON(pluginproto.Deployer{
			ID:   "aliyun",
			Name: "阿里云",
			Fields: []pluginproto.DeployField{
				{Key: "access_key", Label: "AccessKey", Type: "text", Required: true},
				{Key: "secret_key", Label: "SecretKey", Type: "password", Secret: true, Required: true},
			},
			DeployFields: []pluginproto.DeployField{
				{Key: "region", Label: "区域", Type: "text"},
				{Key: "target", Label: "目标", Type: "select", Options: []pluginproto.DeployOption{
					{Label: "CDN", Value: "cdn"}, {Label: "WAF", Value: "waf"},
				}},
				{Key: "domains", Label: "域名", Type: "text"},
			},
		}),
	})
	pluginmeta.Upsert(pluginmeta.Entry{
		Kind: "deploy", ID: "qiniu", Version: "test", Name: "七牛云",
		Raw: mustJSON(pluginproto.Deployer{
			ID:   "qiniu",
			Name: "七牛云",
			Fields: []pluginproto.DeployField{
				{Key: "access_key", Label: "AccessKey", Type: "text", Required: true},
				{Key: "secret_key", Label: "SecretKey", Type: "password", Secret: true, Required: true},
			},
			DeployFields: []pluginproto.DeployField{
				{Key: "domain_type", Label: "域名类型", Type: "select", Options: []pluginproto.DeployOption{
					{Label: "CDN", Value: "cdn"}, {Label: "Kodo", Value: "kodo"},
				}},
				{Key: "domains", Label: "域名", Type: "text"},
			},
		}),
	})
	pluginmeta.Upsert(pluginmeta.Entry{
		Kind: "deploy", ID: "baota", Version: "test", Name: "宝塔面板",
		Raw: mustJSON(pluginproto.Deployer{
			ID:   "baota",
			Name: "宝塔面板",
			Fields: []pluginproto.DeployField{
				{Key: "panel_url", Label: "面板地址", Type: "text", Required: true},
				{Key: "api_secret", Label: "API 密钥", Type: "password", Secret: true, Required: true},
			},
			DeployFields: []pluginproto.DeployField{
				{Key: "target", Label: "目标", Type: "select", Options: []pluginproto.DeployOption{
					{Label: "网站", Value: "site"}, {Label: "面板", Value: "panel"}, {Label: "Docker", Value: "docker"},
				}},
				{Key: "sites", Label: "网站", Type: "text"},
			},
		}),
	})
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
