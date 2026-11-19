package service

import (
	"context"
	"strings"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/plugin/deploy"
)

// DeployObject 是一个可部署对象，例如面板上的网站。
type DeployObject struct {
	Value       string
	Name        string
	Domains     []string
	Identifiers []string
}

// DeployObjectPlan 是目标系统返回的部署对象清单。
type DeployObjectPlan struct {
	Field    string
	Multiple bool
	Objects  []DeployObject
}

// DeployObjectFieldInfo 返回站点类部署目标的字段信息。
// key 是写入部署参数的字段，multiple 表示字段允许逗号分隔的多个值，
// fallback 表示字段留空时插件会回退到证书域名。
func DeployObjectFieldInfo(def ProviderDef, cfg map[string]string) (key string, multiple, fallback, ok bool) {
	key, fallback, ok = deployObjectField(def)
	if !ok || cfg == nil {
		return "", false, false, false
	}
	field, found := deployObjectFieldDef(def, key)
	if !found || !field.VisibleIn(def.DeployFields, cfg) {
		return "", false, false, false
	}
	// 合并后的宝塔目标只有在网站模式下才会回退到证书域名，
	// Docker 网站必须显式填写站点名称。
	if strings.EqualFold(strings.TrimSpace(def.Value), "baota") &&
		strings.TrimSpace(cfg["target"]) == "docker" {
		fallback = false
	}
	return key, field.Multiple, fallback, true
}

// DeployObjectIsSingleton 判断部署目标是面板自身的唯一证书。
func DeployObjectIsSingleton(def ProviderDef, cfg map[string]string) bool {
	return deployObjectIsSingleton(def, cfg)
}

// PlanDeployObjects 调用部署插件读取目标系统中真实存在的部署对象。
// 只传认证与部署参数，不传证书私钥。
func (m *Manager) PlanDeployObjects(ctx context.Context, account *model.DeployAccount, deployConfig map[string]string, locale string) (DeployObjectPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if account == nil {
		return DeployObjectPlan{}, i18n.E("service.deploy.certificate_or_account_missing")
	}
	deployConfig = deployModeFallback(account, deployConfig)
	deployerID := account.Type
	// 与 DeployContext 保持一致：规范目标不支持某个部署对象时回退到旧 ID，
	// 旧 helper 也可能只注册旧 ID。
	if key, value, ok := deployTargetModeForAccount(*account); ok && !deployTargetSupports(account.Type, key, value) {
		if legacy := legacyDeployerID(account.Type, deployConfig); legacy != "" {
			deployerID = legacy
		}
	}
	if !deployplugin.Has(deployerID) {
		if legacy := legacyDeployerID(account.Type, deployConfig); legacy != "" {
			deployerID = legacy
		}
	}
	result, err := deployplugin.Plan(ctx, deployerID, deployplugin.PlanRequest{
		Auth:   account.Config,
		Config: deployConfig,
		Locale: locale,
	})
	if err != nil {
		return DeployObjectPlan{}, err
	}
	out := DeployObjectPlan{Field: result.Field, Multiple: result.Multiple, Objects: make([]DeployObject, 0, len(result.Objects))}
	for _, object := range result.Objects {
		out.Objects = append(out.Objects, DeployObject{
			Value:       object.Value,
			Name:        object.Name,
			Domains:     object.Domains,
			Identifiers: object.Identifiers,
		})
	}
	return out, nil
}

// MatchDeployObjects 按证书域名和用户显式选择筛选部署对象。
// selected 为空时返回所有命中证书域名的对象；非空时还必须命中用户选择值。
func MatchDeployObjects(objects []DeployObject, selected, domains []string) []DeployObject {
	out := make([]DeployObject, 0, len(objects))
	for _, object := range objects {
		if !objectMatchesDomains(object, domains) {
			continue
		}
		if len(selected) > 0 && !objectSelected(object, selected) {
			continue
		}
		out = append(out, object)
	}
	return out
}

// DeployObjectValues 返回写入部署参数字段的值，去重并保持稳定顺序。
func DeployObjectValues(objects []DeployObject) []string {
	out := make([]string, 0, len(objects))
	seen := make(map[string]bool, len(objects))
	for _, object := range objects {
		if object.Value == "" || seen[object.Value] {
			continue
		}
		seen[object.Value] = true
		out = append(out, object.Value)
	}
	return out
}

// ForceDeployValues 返回强制部署时写入的值。
// 用户显式填写时保留原值；留空时使用证书域名尝试下发。
func ForceDeployValues(cfg map[string]string, key string, domains []string) []string {
	values := splitConfigList(cfg[key])
	if len(values) > 0 {
		return values
	}
	return append([]string(nil), domains...)
}

// NormalizeDeployDomains 去重并规范化证书域名。
func NormalizeDeployDomains(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = normalizeDeployDomain(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func objectMatchesDomains(object DeployObject, domains []string) bool {
	for _, host := range objectHosts(object) {
		if siteMatchesDomains(host, domains) {
			return true
		}
	}
	return false
}

func objectSelected(object DeployObject, selected []string) bool {
	hosts := append(objectHosts(object), object.Identifiers...)
	for _, value := range selected {
		value = normalizeDeployDomain(value)
		if value == "" {
			continue
		}
		for _, host := range hosts {
			if normalizeDeployDomain(host) == value {
				return true
			}
		}
	}
	return false
}

func objectHosts(object DeployObject) []string {
	out := make([]string, 0, len(object.Domains)+2)
	out = append(out, object.Domains...)
	out = append(out, object.Value, object.Name)
	result := make([]string, 0, len(out))
	seen := make(map[string]bool, len(out))
	for _, host := range out {
		host = normalizeDeployDomain(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		result = append(result, host)
	}
	return result
}

// sites 以下为旧的字符串过滤逻辑，保留给单次部署和兼容场景。
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

func normalizeDeployDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}
