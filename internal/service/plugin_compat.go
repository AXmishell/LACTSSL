package service

import (
	"sort"
	"strings"
)

// builtinDNSProviderIDs 记录迁移前直接编译进主程序的 DNS 服务商 ID。
// 这些服务商是迁移前内置的历史 ID，用于旧版停用列表迁移与兼容；
// 新安装不再默认启用任何 DNS 服务商。
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

// DeprecatedDeployTypeCanonical 记录已合并的旧部署目标与新的规范目标。
// 由 deployTargetMergeRules 派生，避免旧 ID 映射出现两份来源。
var DeprecatedDeployTypeCanonical = buildDeprecatedDeployMap()

func buildDeprecatedDeployMap() map[string]string {
	out := make(map[string]string, len(deployTargetMergeRules))
	for _, rule := range deployTargetMergeRules {
		out[rule.Legacy] = rule.Canonical
	}
	return out
}

// defaultPluginDeployIDs 是迁移前内置、现在由部署插件提供的规范目标。
// 旧 ID 由 DeprecatedDeployTypeCanonical 自动补齐，保证升级后旧 helper
// 提供的兼容目标仍默认启用。
var defaultPluginDeployIDs = []string{
	"ftp",
	"webhook",
	"aliyun",
	"qiniu",
	"baota",
	"1panel",
	"safeline",
	"smtp",
	"nginx_proxy_manager",
	"acepanel",
	"cdnfly",
	"lucky",
	"doge",
	"tencent",
	"huawei_cdn",
	"volcengine",
	"ucloud_cdn",
	"upyun_cdn",
	"proxmox",
	"synology",
	"plesk",
	"baidu_cdn",
	"lecdn",
	"rainyun",
	"btwaf",
}

// DefaultPluginDeployIDs 返回默认启用的部署插件目标（规范 ID + 旧 ID）。
func DefaultPluginDeployIDs() []string {
	seen := make(map[string]bool, len(defaultPluginDeployIDs)+len(DeprecatedDeployTypeCanonical))
	out := make([]string, 0, len(defaultPluginDeployIDs)+len(DeprecatedDeployTypeCanonical))
	appendID := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range defaultPluginDeployIDs {
		appendID(id)
	}
	for legacy := range DeprecatedDeployTypeCanonical {
		appendID(legacy)
	}
	sort.Strings(out)
	return out
}

// IsDefaultPluginDeployID 判断目标是否属于默认启用集合。
func IsDefaultPluginDeployID(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	if _, ok := DeprecatedDeployTypeCanonical[id]; ok {
		return true
	}
	for _, item := range defaultPluginDeployIDs {
		if item == id {
			return true
		}
	}
	return false
}
