package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/deploy"
	"lactssl/internal/util"
)

const (
	defaultTargetFlowMaxDomains = 100
	maxTargetFlowSources        = 10
	maxTargetFlowSites          = 500
	maxTargetFlowGroups         = 100
)

// TargetSourceInfo 是预览结果中每个来源的部署对象字段信息。
type TargetSourceInfo struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	SiteField   string `json:"site_field"`
	Multiple    bool   `json:"multiple"`
}

// TargetFlowDiscovery 是一次站点读取的结果。
type TargetFlowDiscovery struct {
	Sites    []model.TargetSite
	Sources  []TargetSourceInfo
	Warnings []string
}

// DiscoverTargetSites 通过部署插件的 plan 能力读取目标站点。
// 只读取站点清单，不写入任何证书或配置。
func (m *Manager) DiscoverTargetSites(ctx context.Context, sources []model.TargetSource, locale string) (TargetFlowDiscovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(sources) == 0 {
		return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_source_required")
	}
	if len(sources) > maxTargetFlowSources {
		return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_source_too_many", "max", maxTargetFlowSources)
	}
	discovery := TargetFlowDiscovery{Sites: make([]model.TargetSite, 0), Sources: make([]TargetSourceInfo, 0), Warnings: make([]string, 0)}
	usedKeys := make(map[string]bool)
	for _, source := range sources {
		account, err := m.Store.GetDeployAccount(source.DeployAccountID)
		if err != nil || account == nil {
			return TargetFlowDiscovery{}, i18n.E("api.error.deploy_account_not_found")
		}
		def, found := DeployTypeInfo(account.Type)
		if !found {
			return TargetFlowDiscovery{}, i18n.E("api.error.deploy_type_unsupported")
		}
		merged := MergeDeployConfig(def, account.Config, source.DeployConfig)
		deployCfg := FilterDeployConfig(def, merged)
		if DeployObjectIsSingleton(def, deployCfg) {
			return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_singleton_target", "name", account.Name)
		}
		fieldKey, multiple, _, ok := DeployObjectFieldInfo(def, deployCfg)
		if !ok {
			return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_plan_unsupported", "name", account.Name)
		}
		// 站点字段只用于写回部署值，读取清单时不应限制在某一个站点。
		delete(deployCfg, fieldKey)
		plan, err := m.PlanDeployObjects(ctx, account, deployCfg, locale)
		if err != nil {
			if errors.Is(err, deployplugin.ErrPlanUnsupported) {
				return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_plan_unsupported", "name", account.Name)
			}
			return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_plan_failed", "name", account.Name, "error", sanitizeErrorText(err.Error()))
		}
		if plan.Field != "" {
			fieldKey = plan.Field
		}
		if plan.Multiple {
			multiple = true
		}
		discovery.Sources = append(discovery.Sources, TargetSourceInfo{
			AccountID:   account.ID,
			AccountName: account.Name,
			AccountType: account.Type,
			SiteField:   fieldKey,
			Multiple:    multiple,
		})
		for _, object := range plan.Objects {
			value := strings.TrimSpace(object.Value)
			if value == "" {
				continue
			}
			key := targetSiteKey(account.ID, object.Identifiers, value)
			for usedKeys[key] {
				key += "#2"
			}
			usedKeys[key] = true
			domains, warnings := normalizeTargetDomains(object.Domains, true, i18n.Normalize(locale))
			if len(domains) == 0 {
				// 插件没有返回域名时，仅在站点值本身是合法域名时兜底。
				if util.ValidDomain(normalizeTargetHost(value)) {
					domains = []string{normalizeTargetHost(value)}
					warnings = append(warnings, i18n.T(i18n.Normalize(locale), "task.target_flow.warn_domain_fallback", map[string]any{"site": value}))
				}
			}
			site := model.TargetSite{
				Key:            key,
				AccountID:      account.ID,
				AccountName:    account.Name,
				AccountType:    account.Type,
				Value:          value,
				Name:           strings.TrimSpace(object.Name),
				Domains:        domains,
				Status:         model.SiteStatusReady,
				Warnings:       warnings,
				SiteField:      fieldKey,
				MultipleValues: multiple,
			}
			if site.Name == "" {
				site.Name = value
			}
			if len(domains) == 0 {
				site.Status = model.SiteStatusInvalid
				site.Warnings = append(site.Warnings, i18n.T(i18n.Normalize(locale), "task.target_flow.warn_no_domains", nil))
			}
			discovery.Sites = append(discovery.Sites, site)
		}
	}
	if len(discovery.Sites) == 0 {
		return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_no_sites")
	}
	if len(discovery.Sites) > maxTargetFlowSites {
		return TargetFlowDiscovery{}, i18n.E("task.error.target_flow_too_many_sites", "max", maxTargetFlowSites)
	}
	markDuplicateDomains(discovery.Sites, i18n.Normalize(locale), &discovery.Warnings)
	return discovery, nil
}

// PreviewTargetFlow 读取站点、应用选取规则并生成证书分组预览。
func (m *Manager) PreviewTargetFlow(ctx context.Context, cfg model.TargetFlowConfig, locale i18n.Locale, publicCA bool) (model.TargetFlowPreview, error) {
	discovery, err := m.DiscoverTargetSites(ctx, cfg.Sources, string(locale))
	if err != nil {
		return model.TargetFlowPreview{}, err
	}
	applyTargetSelection(discovery.Sites, cfg.Selection, publicCA, locale)
	applyDomainFilters(discovery.Sites, cfg.Selection, locale, &discovery.Warnings)
	recommendation := RecommendCertStrategy(discovery.Sites, cfg.Policy.MaxDomainsPerCert, publicCA, locale)
	policy := cfg.Policy
	if policy.Strategy == "" || policy.Strategy == model.StrategySmart {
		policy.Strategy = recommendation.Strategy
	}
	if policy.MaxDomainsPerCert <= 0 {
		policy.MaxDomainsPerCert = defaultTargetFlowMaxDomains
	}
	groups, warnings, err := BuildCertGroups(discovery.Sites, policy, cfg.Overrides, publicCA, locale)
	if err != nil {
		return model.TargetFlowPreview{}, err
	}
	discovery.Warnings = append(discovery.Warnings, warnings...)
	m.assignReusableCertificates(groups, 0, nil)
	covered := make(map[string]bool)
	for index := range groups {
		if groups[index].ReuseCertID > 0 {
			for siteIndex := range groups[index].Sites {
				if groups[index].Sites[siteIndex].Status != model.SiteStatusInvalid {
					groups[index].Sites[siteIndex].Status = model.SiteStatusCovered
					covered[groups[index].Sites[siteIndex].Key] = true
				}
			}
		}
	}
	for index := range discovery.Sites {
		if covered[discovery.Sites[index].Key] {
			discovery.Sites[index].Status = model.SiteStatusCovered
		}
	}
	return model.TargetFlowPreview{
		DiscoveredAt:   time.Now().UTC(),
		Sites:          discovery.Sites,
		Groups:         groups,
		Recommendation: recommendation,
		Warnings:       discovery.Warnings,
		Summary: model.TargetFlowSummary{
			SiteCount:  countSelectedSites(discovery.Sites),
			GroupCount: len(groups),
			CertCount:  len(groups),
		},
	}, nil
}

// RecommendCertStrategy 根据站点数量、域名数量和主域名分布给出策略建议。
func RecommendCertStrategy(sites []model.TargetSite, maxDomains int, publicCA bool, locale i18n.Locale) model.StrategyRecommendation {
	if maxDomains <= 0 {
		maxDomains = defaultTargetFlowMaxDomains
	}
	selected := selectedSitesWithDomains(sites)
	result := model.StrategyRecommendation{Strategy: model.StrategyPerSite}
	if len(selected) == 0 {
		result.Warnings = append(result.Warnings, i18n.T(locale, "task.target_flow.warn_no_selected_sites", nil))
		return result
	}
	if len(selected) == 1 {
		result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_single_site", nil))
		return result
	}
	domainSet := make(map[string]bool)
	baseSet := make(map[string]bool)
	multiBase := false
	for _, site := range selected {
		for _, domain := range site.Domains {
			domainSet[domain] = true
		}
		bases := siteBaseDomains(site.Domains)
		if len(bases) > 1 {
			multiBase = true
		}
		for _, base := range bases {
			baseSet[base] = true
		}
	}
	totalDomains := len(domainSet)
	if totalDomains > maxDomains {
		result.Strategy = model.StrategyPerBaseDomain
		result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_too_many_domains", map[string]any{"count": totalDomains, "max": maxDomains}))
		return result
	}
	if multiBase {
		result.Strategy = model.StrategyPerSite
		result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_multi_base_site", nil))
		result.Warnings = append(result.Warnings, i18n.T(locale, "task.target_flow.warn_multi_base_site", nil))
		return result
	}
	if len(baseSet) == 1 {
		result.Strategy = model.StrategySingle
		result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_same_base", nil))
		return result
	}
	if len(selected) <= 5 && totalDomains <= 20 {
		result.Strategy = model.StrategySingle
		result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_small_inventory", map[string]any{"sites": len(selected), "domains": totalDomains}))
		return result
	}
	result.Strategy = model.StrategyPerBaseDomain
	result.Reasons = append(result.Reasons, i18n.T(locale, "task.target_flow.reason_multiple_bases", map[string]any{"count": len(baseSet)}))
	if publicCA && len(selected) > 20 {
		result.Warnings = append(result.Warnings, i18n.T(locale, "task.target_flow.warn_many_sites_rate_limit", map[string]any{"count": len(selected)}))
	}
	return result
}

// BuildCertGroups 根据选取后的站点生成证书组。返回的 warnings 是非致命提示，
// error 表示无法安全生成分组（例如单站点域名数超过上限）。
func BuildCertGroups(sites []model.TargetSite, policy model.CertPolicy, overrides []model.CertGroupOverride, publicCA bool, locale i18n.Locale) ([]model.CertGroup, []string, error) {
	selected := selectedSitesWithDomains(sites)
	if len(selected) == 0 {
		return nil, nil, i18n.E("task.error.target_flow_no_sites")
	}
	if policy.MaxDomainsPerCert <= 0 {
		policy.MaxDomainsPerCert = defaultTargetFlowMaxDomains
	}
	if policy.MaxDomainsPerCert > 500 {
		policy.MaxDomainsPerCert = 500
	}
	strategy := strings.TrimSpace(policy.Strategy)
	if strategy == "" || strategy == model.StrategySmart {
		strategy = RecommendCertStrategy(selected, policy.MaxDomainsPerCert, publicCA, locale).Strategy
	}
	var groups []model.CertGroup
	var warnings []string
	switch strategy {
	case model.StrategyPerSite:
		groups = buildPerSiteGroups(selected)
	case model.StrategySingle:
		groups = buildSingleGroup(selected, locale)
	case model.StrategyPerSource:
		groups = buildPerSourceGroups(selected, locale)
	case model.StrategyPerBaseDomain:
		groups = buildPerBaseDomainGroups(selected, locale)
	case model.StrategyCustom:
		groups, warnings = buildCustomGroups(selected, overrides, locale)
	default:
		return nil, nil, i18n.E("task.error.target_flow_strategy_invalid", "strategy", strategy)
	}
	groups, applyWarnings, err := applyGroupExtrasAndLimits(groups, policy, locale)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, applyWarnings...)
	if err := validateGroupDomains(groups, publicCA); err != nil {
		return nil, nil, err
	}
	if len(groups) > maxTargetFlowGroups {
		return nil, nil, i18n.E("task.error.target_flow_too_many_groups", "max", maxTargetFlowGroups)
	}
	warnings = append(warnings, duplicateGroupWarnings(groups, locale)...)
	return groups, warnings, nil
}

func buildPerSiteGroups(sites []model.TargetSite) []model.CertGroup {
	groups := make([]model.CertGroup, 0, len(sites))
	for _, site := range sites {
		groups = append(groups, model.CertGroup{
			Key:     "site:" + site.Key,
			Label:   siteLabel(site),
			Domains: append([]string(nil), site.Domains...),
			Sites:   []model.TargetSite{site},
		})
	}
	return groups
}

func buildSingleGroup(sites []model.TargetSite, locale i18n.Locale) []model.CertGroup {
	group := model.CertGroup{
		Key:     "all",
		Label:   i18n.T(locale, "task.target_flow.group_all", map[string]any{"count": len(sites)}),
		Sites:   append([]model.TargetSite(nil), sites...),
		Domains: make([]string, 0),
	}
	for _, site := range sites {
		group.Domains = appendUniqueDomains(group.Domains, site.Domains...)
	}
	if len(group.Domains) > 0 {
		group.Label = group.Domains[0]
	}
	return []model.CertGroup{group}
}

func buildPerSourceGroups(sites []model.TargetSite, locale i18n.Locale) []model.CertGroup {
	order := make([]int64, 0)
	bySource := make(map[int64][]model.TargetSite)
	for _, site := range sites {
		if _, exists := bySource[site.AccountID]; !exists {
			order = append(order, site.AccountID)
		}
		bySource[site.AccountID] = append(bySource[site.AccountID], site)
	}
	groups := make([]model.CertGroup, 0, len(order))
	for _, accountID := range order {
		list := bySource[accountID]
		label := "target-" + strconv.FormatInt(accountID, 10)
		if len(list) > 0 && list[0].AccountName != "" {
			label = list[0].AccountName
		}
		group := model.CertGroup{
			Key:     "source:" + strconv.FormatInt(accountID, 10),
			Label:   label,
			Sites:   list,
			Domains: make([]string, 0),
		}
		for _, site := range list {
			group.Domains = appendUniqueDomains(group.Domains, site.Domains...)
		}
		groups = append(groups, group)
	}
	_ = locale
	return groups
}

func buildPerBaseDomainGroups(sites []model.TargetSite, locale i18n.Locale) []model.CertGroup {
	order := make([]string, 0)
	byBase := make(map[string][]model.TargetSite)
	for _, site := range sites {
		bases := siteBaseDomains(site.Domains)
		if len(bases) != 1 {
			key := "multi:" + site.Key
			order = append(order, key)
			byBase[key] = []model.TargetSite{site}
			continue
		}
		key := "base:" + bases[0]
		if _, exists := byBase[key]; !exists {
			order = append(order, key)
		}
		byBase[key] = append(byBase[key], site)
	}
	groups := make([]model.CertGroup, 0, len(order))
	for _, key := range order {
		list := byBase[key]
		label := key
		if strings.HasPrefix(key, "base:") {
			label = strings.TrimPrefix(key, "base:")
		} else if len(list) > 0 {
			label = siteLabel(list[0])
		}
		group := model.CertGroup{Key: key, Label: label, Sites: list, Domains: make([]string, 0)}
		for _, site := range list {
			group.Domains = appendUniqueDomains(group.Domains, site.Domains...)
		}
		if len(groups) == 0 && len(group.Domains) > 0 {
			group.Label = group.Domains[0]
		}
		groups = append(groups, group)
	}
	_ = locale
	return groups
}

func buildCustomGroups(sites []model.TargetSite, overrides []model.CertGroupOverride, locale i18n.Locale) ([]model.CertGroup, []string) {
	byKey := make(map[string]model.TargetSite, len(sites))
	for _, site := range sites {
		byKey[site.Key] = site
	}
	used := make(map[string]bool)
	groups := make([]model.CertGroup, 0, len(overrides))
	warnings := make([]string, 0)
	for index, override := range overrides {
		key := strings.TrimSpace(override.Key)
		if key == "" {
			key = "custom-" + strconv.Itoa(index+1)
		}
		label := strings.TrimSpace(override.Label)
		if label == "" {
			label = key
		}
		group := model.CertGroup{Key: key, Label: label, Domains: make([]string, 0)}
		for _, siteKey := range override.Sites {
			site, ok := byKey[siteKey]
			if !ok {
				warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_unknown_site", map[string]any{"site": siteKey}))
				continue
			}
			if used[siteKey] {
				warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_site_in_multiple_groups", map[string]any{"site": site.Name}))
				continue
			}
			used[siteKey] = true
			group.Sites = append(group.Sites, site)
			group.Domains = appendUniqueDomains(group.Domains, site.Domains...)
		}
		if len(group.Sites) == 0 {
			continue
		}
		group.Domains = appendUniqueDomains(group.Domains, override.Domains...)
		if len(group.Domains) > 0 && strings.HasPrefix(label, "custom-") {
			group.Label = group.Domains[0]
		}
		groups = append(groups, group)
	}
	// 未被任何自定义分组引用的站点单独成组，避免静默漏签。
	for _, site := range sites {
		if used[site.Key] {
			continue
		}
		if len(overrides) > 0 {
			warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_unassigned_site", map[string]any{"site": site.Name}))
		}
		groups = append(groups, model.CertGroup{
			Key:     "site:" + site.Key,
			Label:   siteLabel(site),
			Domains: append([]string(nil), site.Domains...),
			Sites:   []model.TargetSite{site},
		})
	}
	return groups, warnings
}

func applyGroupExtrasAndLimits(groups []model.CertGroup, policy model.CertPolicy, locale i18n.Locale) ([]model.CertGroup, []string, error) {
	limit := policy.MaxDomainsPerCert
	if limit <= 0 {
		limit = defaultTargetFlowMaxDomains
	}
	out := make([]model.CertGroup, 0, len(groups))
	warnings := make([]string, 0)
	for _, group := range groups {
		extra := util.NormalizeDomains(policy.ExtraDomains[group.Key])
		for _, domain := range extra {
			if !util.ValidDomain(domain) {
				return nil, nil, i18n.E("task.error.target_flow_domain_invalid", "domain", domain)
			}
			group.Domains = appendUniqueDomains(group.Domains, domain)
		}
		if len(group.Domains) > limit && len(group.Sites) <= 1 {
			return nil, nil, i18n.E("task.error.target_flow_group_too_large", "site", group.Label, "count", len(group.Domains), "max", limit)
		}
		if len(group.Domains) <= limit {
			out = append(out, group)
			continue
		}
		parts, splitErr := splitGroupBySites(group, limit, locale)
		if splitErr != nil {
			return nil, nil, splitErr
		}
		warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_group_split", map[string]any{"group": group.Label, "count": len(parts)}))
		out = append(out, parts...)
	}
	return out, warnings, nil
}

func splitGroupBySites(group model.CertGroup, limit int, locale i18n.Locale) ([]model.CertGroup, error) {
	parts := make([]model.CertGroup, 0)
	current := model.CertGroup{Key: group.Key, Label: group.Label, Domains: make([]string, 0), Sites: make([]model.TargetSite, 0)}
	flush := func() {
		if len(current.Sites) == 0 {
			return
		}
		parts = append(parts, current)
	}
	for _, site := range group.Sites {
		if len(site.Domains) > limit {
			return nil, i18n.E("task.error.target_flow_group_too_large", "site", site.Name, "count", len(site.Domains), "max", limit)
		}
		if len(current.Domains)+len(site.Domains) > limit && len(current.Sites) > 0 {
			flush()
			current = model.CertGroup{Key: group.Key, Label: group.Label, Domains: make([]string, 0), Sites: make([]model.TargetSite, 0)}
		}
		current.Sites = append(current.Sites, site)
		current.Domains = appendUniqueDomains(current.Domains, site.Domains...)
	}
	flush()
	for index := range parts {
		if len(parts) == 1 {
			continue
		}
		parts[index].Key = fmt.Sprintf("%s-%d", group.Key, index+1)
		parts[index].Label = fmt.Sprintf("%s (%d/%d)", group.Label, index+1, len(parts))
	}
	_ = locale
	return parts, nil
}

func validateGroupDomains(groups []model.CertGroup, publicCA bool) error {
	for _, group := range groups {
		for _, domain := range group.Domains {
			if !util.ValidDomain(domain) {
				return i18n.E("task.error.target_flow_domain_invalid", "domain", domain)
			}
			if publicCA && isPublicCAUnsupportedDomain(domain) {
				return i18n.E("task.error.target_flow_domain_invalid", "domain", domain)
			}
		}
	}
	return nil
}

func duplicateGroupWarnings(groups []model.CertGroup, locale i18n.Locale) []string {
	owners := make(map[string]string)
	warnings := make([]string, 0)
	for _, group := range groups {
		for _, domain := range group.Domains {
			if owner, exists := owners[domain]; exists && owner != group.Key {
				warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_domain_duplicate_group", map[string]any{"domain": domain, "groups": owner + ", " + group.Key}))
				continue
			}
			owners[domain] = group.Key
		}
	}
	return warnings
}

// assignReusableCertificates 为目标组查找覆盖其全部域名且仍可复用的证书。
// 泛域名证书可以覆盖同一主域名下的子域名，从而避免因新增站点重签整张证书。
func (m *Manager) assignReusableCertificates(groups []model.CertGroup, renewDays int, allowedCAs map[string]bool) {
	certs, err := m.Store.ListCertificates()
	if err != nil || len(certs) == 0 {
		return
	}
	now := time.Now().UTC()
	for index := range groups {
		if len(groups[index].Domains) == 0 {
			continue
		}
		for _, cert := range certs {
			if cert.ID == 0 || !cert.KeyAvailable || cert.RevokedAt != nil {
				continue
			}
			if len(allowedCAs) > 0 && !allowedCAs[cert.CA] {
				continue
			}
			if cert.Status != "" && cert.Status != "valid" {
				continue
			}
			if !cert.NotAfter.After(now) || cert.NotBefore.After(now) {
				continue
			}
			if renewalDue(cert.NotBefore, cert.NotAfter, renewDays) {
				continue
			}
			if !certificateCoversDomains(cert.Domains, groups[index].Domains) {
				continue
			}
			groups[index].ReuseCertID = cert.ID
			break
		}
	}
}

func certificateCoversDomains(certDomains, wanted []string) bool {
	if len(wanted) == 0 {
		return false
	}
	for _, domain := range wanted {
		if !domainCoveredByCert(domain, certDomains) {
			return false
		}
	}
	return true
}

func domainCoveredByCert(domain string, certDomains []string) bool {
	domain = normalizeTargetHost(domain)
	if domain == "" {
		return false
	}
	for _, raw := range certDomains {
		certDomain := normalizeTargetHost(raw)
		if certDomain == "" {
			continue
		}
		if certDomain == domain {
			return true
		}
		if strings.HasPrefix(certDomain, "*.") {
			base := strings.TrimPrefix(certDomain, "*.")
			if strings.HasSuffix(domain, "."+base) && strings.Count(domain, ".") == strings.Count(base, ".")+1 {
				return true
			}
		}
	}
	return false
}

func applyTargetSelection(sites []model.TargetSite, selection model.SiteSelection, publicCA bool, locale i18n.Locale) {
	mode := strings.TrimSpace(selection.Mode)
	if mode == "" {
		mode = model.SelectionAll
	}
	selected := make(map[string]bool, len(selection.Keys))
	for _, key := range selection.Keys {
		selected[key] = true
	}
	for index := range sites {
		site := &sites[index]
		if site.Status == model.SiteStatusInvalid || len(site.Domains) == 0 {
			site.Selected = false
			site.Status = model.SiteStatusInvalid
			continue
		}
		if publicCA {
			filtered := make([]string, 0, len(site.Domains))
			for _, domain := range site.Domains {
				if isPublicCAUnsupportedDomain(domain) {
					site.Warnings = append(site.Warnings, i18n.T(locale, "task.target_flow.warn_public_domain", map[string]any{"domain": domain}))
					continue
				}
				filtered = append(filtered, domain)
			}
			site.Domains = filtered
			if len(site.Domains) == 0 {
				site.Selected = false
				site.Status = model.SiteStatusInvalid
				continue
			}
		}
		if site.Status == "" {
			site.Status = model.SiteStatusReady
		}
		switch mode {
		case model.SelectionSelected:
			site.Selected = selected[site.Key]
		case model.SelectionAll, model.SelectionSmart:
			site.Selected = true
		default:
			site.Selected = true
		}
		if !site.Selected && site.Status != model.SiteStatusInvalid {
			site.Status = model.SiteStatusSkipped
		}
	}
}

func applyDomainFilters(sites []model.TargetSite, selection model.SiteSelection, locale i18n.Locale, warnings *[]string) {
	include := util.NormalizeDomains(selection.IncludeDomains)
	exclude := util.NormalizeDomains(selection.ExcludeDomains)
	if len(include) == 0 && len(exclude) == 0 {
		return
	}
	for index := range sites {
		site := &sites[index]
		if !site.Selected {
			continue
		}
		filtered := make([]string, 0, len(site.Domains))
		for _, domain := range site.Domains {
			if len(include) > 0 && !domainMatchesAny(domain, include) {
				continue
			}
			if len(exclude) > 0 && domainMatchesAny(domain, exclude) {
				continue
			}
			filtered = append(filtered, domain)
		}
		site.Domains = filtered
		if len(filtered) == 0 {
			site.Selected = false
			site.Status = model.SiteStatusSkipped
			*warnings = append(*warnings, i18n.T(locale, "task.target_flow.warn_site_filtered", map[string]any{"site": site.Name}))
		}
	}
}

func markDuplicateDomains(sites []model.TargetSite, locale i18n.Locale, warnings *[]string) {
	owners := make(map[string]string)
	for index := range sites {
		site := &sites[index]
		if len(site.Domains) == 0 {
			continue
		}
		duplicate := false
		for _, domain := range site.Domains {
			if owner, exists := owners[domain]; exists && owner != site.Key {
				duplicate = true
				site.Warnings = append(site.Warnings, i18n.T(locale, "task.target_flow.warn_domain_duplicate", map[string]any{"domain": domain}))
				*warnings = append(*warnings, i18n.T(locale, "task.target_flow.warn_domain_duplicate", map[string]any{"domain": domain}))
				continue
			}
			owners[domain] = site.Key
		}
		if duplicate && site.Status == model.SiteStatusReady {
			site.Status = model.SiteStatusDuplicate
		}
	}
}

func normalizeTargetDomains(raw []string, dedupe bool, locale i18n.Locale) ([]string, []string) {
	domains := util.NormalizeDomains(raw)
	if !dedupe {
		return domains, nil
	}
	valid := make([]string, 0, len(domains))
	warnings := make([]string, 0)
	for _, domain := range domains {
		if !util.ValidDomain(domain) {
			warnings = append(warnings, i18n.T(locale, "task.target_flow.warn_domain_invalid", map[string]any{"domain": domain}))
			continue
		}
		valid = append(valid, domain)
	}
	return valid, warnings
}

func targetSiteKey(accountID int64, identifiers []string, value string) string {
	stable := strings.TrimSpace(value)
	for _, identifier := range identifiers {
		if strings.TrimSpace(identifier) != "" {
			stable = strings.TrimSpace(identifier)
			break
		}
	}
	return strconv.FormatInt(accountID, 10) + "|" + stable
}

func normalizeTargetHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	if index := strings.IndexAny(value, "/:"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSuffix(value, ".")
}

func siteBaseDomains(domains []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		host := strings.TrimPrefix(normalizeTargetHost(domain), "*.")
		if host == "" {
			continue
		}
		base, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil || base == "" {
			base = fallbackBaseDomain(host)
		}
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, base)
	}
	sort.Strings(out)
	return out
}

func fallbackBaseDomain(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func selectedSitesWithDomains(sites []model.TargetSite) []model.TargetSite {
	out := make([]model.TargetSite, 0, len(sites))
	for _, site := range sites {
		if !site.Selected || len(site.Domains) == 0 || site.Status == model.SiteStatusInvalid || site.Status == model.SiteStatusSkipped {
			continue
		}
		out = append(out, site)
	}
	return out
}

func countSelectedSites(sites []model.TargetSite) int {
	count := 0
	for _, site := range sites {
		if site.Selected && len(site.Domains) > 0 && site.Status != model.SiteStatusInvalid && site.Status != model.SiteStatusSkipped {
			count++
		}
	}
	return count
}

func appendUniqueDomains(dst []string, values ...string) []string {
	seen := make(map[string]bool, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = true
	}
	for _, value := range values {
		value = normalizeTargetHost(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		dst = append(dst, value)
	}
	return dst
}

func domainMatchesAny(domain string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = normalizeTargetHost(pattern)
		if pattern == "" {
			continue
		}
		if pattern == "*" || domain == pattern {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			base := strings.TrimPrefix(pattern, "*.")
			if domain == base || strings.HasSuffix(domain, "."+base) {
				return true
			}
			continue
		}
		if strings.HasSuffix(domain, "."+pattern) {
			return true
		}
	}
	return false
}

func isPublicCAUnsupportedDomain(domain string) bool {
	host := strings.TrimPrefix(normalizeTargetHost(domain), "*.")
	if host == "" {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return host == "localhost" || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".")
}

func siteLabel(site model.TargetSite) string {
	if strings.TrimSpace(site.Name) != "" {
		return site.Name
	}
	if strings.TrimSpace(site.Value) != "" {
		return site.Value
	}
	if len(site.Domains) > 0 {
		return site.Domains[0]
	}
	return site.Key
}

func sanitizeErrorText(text string) string {
	if len(text) > 400 {
		text = text[:400] + "..."
	}
	return text
}

// TargetFlowUsesCommand 判断目标流程的部署参数是否包含重载命令。
func TargetFlowUsesCommand(cfg model.TargetFlowConfig) bool {
	for _, source := range cfg.Sources {
		if strings.TrimSpace(source.DeployConfig["reload_command"]) != "" {
			return true
		}
	}
	return false
}
