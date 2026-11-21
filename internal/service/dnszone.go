package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// DNS zone 绑定的匹配模式。
const (
	DNSZoneMatchSuffix = "suffix"
	DNSZoneMatchExact  = "exact"

	DNSZoneSourceManual = "manual"
	DNSZoneSourceVerify = "verify"
	DNSZoneSourceImport = "import"
)

// NormalizeDNSZone 把用户输入的 zone 归一化为小写、无尾点、无通配符的形式。
// 通配符输入（*.example.com）按 example.com 处理，因为 suffix 模式本就覆盖子域。
func NormalizeDNSZone(value string) (string, error) {
	zone := strings.ToLower(strings.TrimSpace(value))
	zone = strings.TrimPrefix(zone, "*.")
	zone = strings.TrimSuffix(zone, ".")
	if zone == "" || !util.ValidDomain(zone) {
		return "", i18n.E("service.csr.dns_zone_invalid", "zone", strings.TrimSpace(value))
	}
	return zone, nil
}

// NormalizeDNSZoneMatchMode 归一化匹配模式，未知值回退到 suffix。
func NormalizeDNSZoneMatchMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), DNSZoneMatchExact) {
		return DNSZoneMatchExact
	}
	return DNSZoneMatchSuffix
}

// NormalizeDNSZoneSource 归一化绑定来源。
func NormalizeDNSZoneSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DNSZoneSourceVerify:
		return DNSZoneSourceVerify
	case DNSZoneSourceImport:
		return DNSZoneSourceImport
	default:
		return DNSZoneSourceManual
	}
}

// DNSZoneMatch 是单个域名与绑定的匹配结果。
type DNSZoneMatch struct {
	Domain    string `json:"domain"`
	Zone      string `json:"zone"`
	AccountID int64  `json:"account_id"`
	MatchMode string `json:"match_mode"`
	Priority  int    `json:"priority"`
}

// DNSAccountResolution 是 CSR 域名集合的解析结果。
type DNSAccountResolution struct {
	AccountID int64          `json:"account_id"`
	Zone      string         `json:"zone"`
	Matches   []DNSZoneMatch `json:"matches"`
}

// ResolveDNSRoutesForDomains 按“最长 zone 优先、priority 次之”的规则，
// 把每个域名解析到各自 DNS 账户。不同域名可以命中不同账户；
// 只有单个域名同时命中多个同等级账户时才会报错。
func ResolveDNSRoutesForDomains(domains []string, bindings []model.DNSZoneBinding) ([]model.DNSRoute, error) {
	if len(domains) == 0 {
		return nil, i18n.E("service.csr.dns_required")
	}
	routes := make([]model.DNSRoute, 0, len(domains))
	seen := make(map[string]model.DNSRoute, len(domains))
	for _, raw := range domains {
		domain := canonicalCertificateDomain(raw)
		if domain == "" {
			return nil, i18n.E("service.csr.dns_zone_invalid", "zone", raw)
		}
		best, found, err := bestBindingForDomain(domain, bindings)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, i18n.E("service.csr.dns_account_unresolved", "domain", raw)
		}
		route := model.DNSRoute{
			Domain:       domain,
			Zone:         best.Zone,
			DNSAccountID: best.AccountID,
			MatchMode:    best.MatchMode,
			Priority:     best.Priority,
			ResolvedBy:   "binding",
		}
		if existing, ok := seen[domain]; ok {
			if existing.DNSAccountID != route.DNSAccountID {
				return nil, i18n.E("service.csr.dns_account_ambiguous",
					"domains", domain, "accounts", strconv.FormatInt(existing.DNSAccountID, 10)+", "+strconv.FormatInt(route.DNSAccountID, 10))
			}
			continue
		}
		seen[domain] = route
		routes = append(routes, route)
	}
	return routes, nil
}

// ExplicitDNSRoutesForDomains 为显式指定的 DNS 账户生成全部域名的路由。
// 显式账户语义保持兼容：调用方明确表示该账户可以覆盖整张证书。
func ExplicitDNSRoutesForDomains(domains []string, accountID int64) ([]model.DNSRoute, error) {
	if accountID <= 0 {
		return nil, i18n.E("service.csr.dns_account_required")
	}
	if len(domains) == 0 {
		return nil, i18n.E("service.csr.dns_required")
	}
	routes := make([]model.DNSRoute, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, raw := range domains {
		domain := canonicalCertificateDomain(raw)
		if domain == "" {
			return nil, i18n.E("service.csr.dns_zone_invalid", "zone", raw)
		}
		if seen[domain] {
			continue
		}
		seen[domain] = true
		routes = append(routes, model.DNSRoute{
			Domain:       domain,
			DNSAccountID: accountID,
			ResolvedBy:   "explicit",
		})
	}
	return routes, nil
}

// DNSRoutesCoverDomains 判断路由快照是否覆盖全部域名。
func DNSRoutesCoverDomains(routes []model.DNSRoute, domains []string) bool {
	if len(routes) == 0 || len(domains) == 0 {
		return false
	}
	available := make(map[string]bool, len(routes))
	for _, route := range routes {
		domain := canonicalCertificateDomain(route.Domain)
		if domain != "" && route.DNSAccountID > 0 {
			available[domain] = true
		}
	}
	for _, raw := range domains {
		domain := canonicalCertificateDomain(raw)
		if domain == "" || !available[domain] {
			return false
		}
	}
	return true
}

// DNSRouteAccountIDs 返回路由中使用的账户 ID，去重并按升序排列。
func DNSRouteAccountIDs(routes []model.DNSRoute) []int64 {
	seen := make(map[int64]bool, len(routes))
	ids := make([]int64, 0, len(routes))
	for _, route := range routes {
		if route.DNSAccountID <= 0 || seen[route.DNSAccountID] {
			continue
		}
		seen[route.DNSAccountID] = true
		ids = append(ids, route.DNSAccountID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// DNSRoutesHash 返回路由集合的稳定摘要，用于 CSR 在途申请去重。
// 只包含会影响 TXT 写入位置的字段，避免展示字段变化造成幂等冲突。
func DNSRoutesHash(routes []model.DNSRoute) string {
	type hashRoute struct {
		Domain       string `json:"domain"`
		DNSAccountID int64  `json:"dns_account_id"`
	}
	canonical := make([]hashRoute, 0, len(routes))
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		domain := canonicalCertificateDomain(route.Domain)
		key := domain + "\x00" + strconv.FormatInt(route.DNSAccountID, 10)
		if domain == "" || route.DNSAccountID <= 0 || seen[key] {
			continue
		}
		seen[key] = true
		canonical = append(canonical, hashRoute{
			Domain:       domain,
			DNSAccountID: route.DNSAccountID,
		})
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Domain != canonical[j].Domain {
			return canonical[i].Domain < canonical[j].Domain
		}
		return canonical[i].DNSAccountID < canonical[j].DNSAccountID
	})
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ResolveDNSAccountForDomains 是单账户兼容入口。
// 多账户证书应使用 ResolveDNSRoutesForDomains；这里仍要求全部域名落到同一账户。
func ResolveDNSAccountForDomains(domains []string, bindings []model.DNSZoneBinding) (*DNSAccountResolution, error) {
	routes, err := ResolveDNSRoutesForDomains(domains, bindings)
	if err != nil {
		return nil, err
	}
	accountIDs := DNSRouteAccountIDs(routes)
	if len(accountIDs) != 1 {
		parts := make([]string, 0, len(accountIDs))
		for _, id := range accountIDs {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		return nil, i18n.E("service.csr.dns_account_ambiguous",
			"domains", strings.Join(domains, ", "), "accounts", strings.Join(parts, ", "))
	}
	longestZone := ""
	matches := make([]DNSZoneMatch, 0, len(routes))
	for _, route := range routes {
		if len(route.Zone) > len(longestZone) {
			longestZone = route.Zone
		}
		matches = append(matches, DNSZoneMatch{
			Domain:    route.Domain,
			Zone:      route.Zone,
			AccountID: route.DNSAccountID,
			MatchMode: route.MatchMode,
			Priority:  route.Priority,
		})
	}
	return &DNSAccountResolution{AccountID: accountIDs[0], Zone: longestZone, Matches: matches}, nil
}

// MatchDNSAccountForDomains 返回指定账户覆盖全部域名时的最长 zone。
// 用于显式 dns_account_id 请求的审计信息；不覆盖时返回空字符串。
func MatchDNSAccountForDomains(domains []string, bindings []model.DNSZoneBinding, accountID int64) string {
	longest := ""
	for _, raw := range domains {
		domain := canonicalCertificateDomain(raw)
		if domain == "" {
			return ""
		}
		best, found, err := bestBindingForDomain(domain, bindings)
		if err != nil || !found || best.AccountID != accountID {
			return ""
		}
		if len(best.Zone) > len(longest) {
			longest = best.Zone
		}
	}
	return longest
}

// ZoneFromVerifyResult 从 DNS 凭据验证结果中提取插件返回的 zone。
// 只有 read/auto 模式且插件声明 verify-v1 时才可能得到该检查项。
func ZoneFromVerifyResult(result VerifyResult) string {
	for _, check := range result.Checks {
		if check.Key != "zone" || check.Status != VerifyStatusOK {
			continue
		}
		zone, err := NormalizeDNSZone(check.Message)
		if err == nil {
			return zone
		}
	}
	return ""
}

func bestBindingForDomain(domain string, bindings []model.DNSZoneBinding) (*DNSZoneMatch, bool, error) {
	var best *DNSZoneMatch
	for _, binding := range bindings {
		if !binding.Enabled || binding.Zone == "" {
			continue
		}
		zone := canonicalCertificateDomain(binding.Zone)
		if zone == "" {
			continue
		}
		mode := NormalizeDNSZoneMatchMode(binding.MatchMode)
		matched := domain == zone
		if !matched && mode == DNSZoneMatchSuffix {
			matched = strings.HasSuffix(domain, "."+zone)
		}
		if !matched {
			continue
		}
		candidate := &DNSZoneMatch{
			Domain:    domain,
			Zone:      zone,
			AccountID: binding.DNSAccountID,
			MatchMode: mode,
			Priority:  binding.Priority,
		}
		if best != nil && sameMatchRank(candidate, best) && candidate.AccountID != best.AccountID {
			return nil, false, i18n.E("service.csr.dns_account_ambiguous",
				"domains", domain, "accounts", strconv.FormatInt(best.AccountID, 10)+", "+strconv.FormatInt(candidate.AccountID, 10))
		}
		if best == nil || betterMatch(candidate, best) {
			best = candidate
		}
	}
	return best, best != nil, nil
}

func betterMatch(a, b *DNSZoneMatch) bool {
	if len(a.Zone) != len(b.Zone) {
		return len(a.Zone) > len(b.Zone)
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.AccountID < b.AccountID
}

func sameMatchRank(a, b *DNSZoneMatch) bool {
	return len(a.Zone) == len(b.Zone) && a.Priority == b.Priority
}

// canonicalCertificateDomain 去掉通配符、尾点并转小写。
func canonicalCertificateDomain(value string) string {
	domain := strings.ToLower(strings.TrimSpace(value))
	domain = strings.TrimPrefix(domain, "*.")
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" || !util.ValidDomain(domain) {
		return ""
	}
	return domain
}
