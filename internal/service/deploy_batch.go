package service

import (
	"sort"
	"strings"

	"lactssl/internal/i18n"
)

// deployObjectField 返回部署类型中用于指定“目标站点/对象”的字段。
// 批量部署时这些字段与证书一一对应：同一取值不能套用到全部证书，
// 否则后一次部署会覆盖前一次。
//
// fallback 表示字段留空时部署插件会回退到证书自身域名（例如宝塔网站、
// AcePanel 网站）。此时批量部署可以按证书域名自动匹配站点。
func deployObjectField(def ProviderDef) (key string, fallback bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(def.Value)) {
	case "baota", "baota_site":
		return "sites", true, true
	case "baota_docker":
		return "sites", false, true
	case "1panel", "1panel_site":
		return "website", false, true
	case "acepanel":
		return "websites", true, true
	}
	return "", false, false
}

func deployObjectFieldDef(def ProviderDef, key string) (FieldDef, bool) {
	for _, field := range def.DeployFields {
		if field.Key == key {
			return field, true
		}
	}
	return FieldDef{}, false
}

// deployObjectIsSingleton 判断部署目标是面板自身的唯一证书。
func deployObjectIsSingleton(def ProviderDef, cfg map[string]string) bool {
	value := strings.ToLower(strings.TrimSpace(def.Value))
	mode := strings.ToLower(strings.TrimSpace(cfg["target"]))
	switch value {
	case "baota_panel", "1panel_panel", "acepanel_panel", "safeline_panel", "safeline_portal":
		return true
	case "baota", "1panel", "acepanel":
		return mode == "panel"
	case "safeline":
		return mode == "panel" || mode == "portal"
	}
	return false
}

// FilterBatchDeployObjects 把批量部署共享的站点配置裁剪为当前证书可用的
// 站点，并返回本次实际会写入的目标对象。
//
// 规则：
//   - 显式填写站点时，只保留与证书域名匹配的站点；全部不匹配则返回错误，
//     避免把证书写到不属于它的站点上。
//   - 站点留空且插件支持回退时，按证书域名自动匹配。
//   - 没有站点类字段的部署目标返回空对象，由调用方按普通目标处理。
func FilterBatchDeployObjects(def ProviderDef, cfg map[string]string, domains []string) (string, []string, error) {
	// 面板自身证书（宝塔/1Panel/AcePanel/雷池面板）只有一个部署对象，
	// 多张证书写入同一凭据必然互相覆盖。
	if deployObjectIsSingleton(def, cfg) {
		return "@self", []string{"@panel"}, nil
	}
	key, fallback, ok := deployObjectField(def)
	if !ok || cfg == nil {
		return "", nil, nil
	}
	field, found := deployObjectFieldDef(def, key)
	if !found || !field.VisibleIn(def.DeployFields, cfg) {
		return "", nil, nil
	}
	// 合并后的宝塔目标只有在网站模式下才会回退到证书域名，
	// Docker 网站必须显式填写站点名称。
	if strings.EqualFold(strings.TrimSpace(def.Value), "baota") &&
		strings.TrimSpace(cfg["target"]) == "docker" {
		fallback = false
	}

	values := splitConfigList(cfg[key])
	if len(values) == 0 {
		if !fallback {
			return key, nil, nil
		}
		values = domains
		// 显式写入证书域名，确保插件实际写入的对象与冲突检测使用的一致。
		cfg[key] = strings.Join(values, ",")
	} else {
		matched := make([]string, 0, len(values))
		for _, value := range values {
			if siteMatchesDomains(value, domains) {
				matched = append(matched, value)
				continue
			}
			// 数字 ID、自定义站点名等无法仅凭域名判断归属，保留原值交由
			// 插件解析；后续的站点冲突检查会阻止多张证书写入同一对象。
			if !looksLikeDeployDomain(value) {
				matched = append(matched, value)
			}
		}
		if len(matched) == 0 {
			return key, nil, i18n.E("service.deploy.batch_site_mismatch",
				"sites", strings.Join(values, ", "),
				"domains", strings.Join(domains, ", "))
		}
		cfg[key] = strings.Join(matched, ",")
		values = matched
	}

	objects := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = normalizeDeployDomain(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		objects = append(objects, value)
	}
	sort.Strings(objects)
	return key, objects, nil
}

// siteMatchesDomains 判断站点名称是否属于证书覆盖的域名。
// 证书包含通配符域名时，通配符下的任意子域名也视为匹配。
func siteMatchesDomains(site string, domains []string) bool {
	site = normalizeDeployDomain(site)
	if site == "" {
		return false
	}
	for _, domain := range domains {
		domain = normalizeDeployDomain(domain)
		if domain == "" {
			continue
		}
		if site == domain {
			return true
		}
		if strings.HasPrefix(domain, "*.") {
			base := strings.TrimPrefix(domain, "*.")
			if site == base || strings.HasSuffix(site, "."+base) {
				return true
			}
		}
	}
	return false
}

// looksLikeDeployDomain 判断站点字段值是否像域名。
// 像域名的值必须与证书域名匹配；数字 ID 或自定义站点名无法在本地校验，
// 由插件解析并交给批量冲突检查兜底。
func looksLikeDeployDomain(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, ".") && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://")
}

func normalizeDeployDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}
