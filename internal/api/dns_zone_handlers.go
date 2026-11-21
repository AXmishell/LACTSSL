package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

type dnsZoneBody struct {
	Zone      string `json:"zone"`
	MatchMode string `json:"match_mode"`
	Priority  int    `json:"priority"`
	// Enabled 省略时默认 true，兼容只关心 zone 的简单调用。
	Enabled *bool `json:"enabled"`
}

type replaceDNSZonesBody struct {
	Zones []dnsZoneBody `json:"zones"`
}

type probeDNSZoneBody struct {
	Domain string `json:"domain"`
	Mode   string `json:"mode"`
	Apply  bool   `json:"apply"`
}

type resolveDNSBody struct {
	Domains []string `json:"domains"`
}

// listDNSAccountZones 返回账户已声明的域名绑定。
func (s *Server) listDNSAccountZones(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if s.denyIfAPIKeyDNSAccountHidden(c, id) {
		return
	}
	if _, err := s.Store.GetDNSAccount(id); err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	list, err := s.Store.ListDNSZoneBindingsByAccount(id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

// replaceDNSAccountZones 原子替换账户的全部绑定。
// 保存前统一做规范化和冲突检查，避免同一 zone 被多个账户声明。
func (s *Server) replaceDNSAccountZones(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if s.denyIfAPIKeyDNSAccountHidden(c, id) {
		return
	}
	if _, err := s.Store.GetDNSAccount(id); err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	var body replaceDNSZonesBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if len(body.Zones) > 500 {
		failKey(c, http.StatusBadRequest, "api.error.dns_zones_too_many", "max", 500)
		return
	}
	bindings := make([]model.DNSZoneBinding, 0, len(body.Zones))
	seen := make(map[string]bool, len(body.Zones))
	for _, item := range body.Zones {
		zone, err := service.NormalizeDNSZone(item.Zone)
		if err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if seen[zone] {
			failKey(c, http.StatusBadRequest, "api.error.dns_zone_duplicate", "zone", zone)
			return
		}
		seen[zone] = true
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		bindings = append(bindings, model.DNSZoneBinding{
			DNSAccountID: id,
			Zone:         zone,
			MatchMode:    service.NormalizeDNSZoneMatchMode(item.MatchMode),
			Priority:     item.Priority,
			Source:       service.DNSZoneSourceManual,
			Enabled:      enabled,
		})
	}
	if err := s.Store.ReplaceDNSZoneBindings(id, bindings); err != nil {
		var conflict *store.DNSZoneConflictError
		if errors.As(err, &conflict) {
			failKey(c, http.StatusConflict, "api.error.dns_zone_conflict",
				"zone", conflict.Zone, "account_id", conflict.AccountID)
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("dns", id, "info", "log.dns.zones_saved", map[string]any{"count": len(bindings)})
	list, err := s.Store.ListDNSZoneBindingsByAccount(id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

// probeDNSAccountZone 用指定域名验证该账户的可用性，并把插件返回的 zone
// 回填为绑定。探测默认使用只读模式，避免为了“发现”而写入 TXT 记录。
func (s *Server) probeDNSAccountZone(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var body probeDNSZoneBody
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyWriteGuard(c, apiScopeDNSWrite) {
		return
	}
	if s.denyIfAPIKeyDNSAccountHidden(c, id) {
		return
	}
	if !s.verifyRateLimit(c, "verify:dns:zone:"+strconv.FormatInt(id, 10)) {
		return
	}
	account, err := s.Store.GetDNSAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	domain := service.NormalizeVerifyDomain(body.Domain)
	if domain == "" {
		failKey(c, http.StatusBadRequest, "api.error.dns_probe_domain_required")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "read"
	}
	result := s.Manager.VerifyDNSAccount(c.Request.Context(), locale(c), strconv.FormatInt(account.ID, 10),
		account.Provider, account.Config, domain, mode)
	s.persistDNSVerify(account, result)
	zone := service.ZoneFromVerifyResult(result)
	applied := false
	if body.Apply && zone != "" {
		if _, err := s.Store.UpsertDNSZoneBinding(account.ID, model.DNSZoneBinding{
			DNSAccountID: account.ID,
			Zone:         zone,
			MatchMode:    service.DNSZoneMatchSuffix,
			Source:       service.DNSZoneSourceVerify,
			Enabled:      true,
		}); err != nil {
			var conflict *store.DNSZoneConflictError
			if errors.As(err, &conflict) {
				failKey(c, http.StatusConflict, "api.error.dns_zone_conflict",
					"zone", conflict.Zone, "account_id", conflict.AccountID)
				return
			}
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		applied = true
		s.Store.AddLogKey("dns", account.ID, "info", "log.dns.zone_probed",
			map[string]any{"zone": zone, "domain": domain})
	}
	ok(c, gin.H{
		"zone":    zone,
		"applied": applied,
		"result":  result,
	})
}

// resolveDNSAccounts 是只读解析预览，供客户端在提交 CSR 前自检。
// 与 CSR 创建共用同一套最长后缀匹配算法，不发起任何出站请求。
func (s *Server) resolveDNSAccounts(c *gin.Context) {
	var body resolveDNSBody
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Domains) == 0 {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	domains := util.NormalizeDomains(body.Domains)
	if len(domains) == 0 || len(domains) > 100 {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	bindings, err := s.Store.ListEnabledDNSZoneBindings()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	bindings = filterDNSBindingsForAPIKey(c, bindings)
	routes, err := service.ResolveDNSRoutesForDomains(domains, bindings)
	if err != nil {
		failErr(c, csrResolveStatus(err), err)
		return
	}
	accountIDs := service.DNSRouteAccountIDs(routes)
	primary := int64(0)
	accountName := ""
	if len(accountIDs) == 1 {
		account, err := s.Store.GetDNSAccount(accountIDs[0])
		if err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		primary = account.ID
		accountName = account.Name
	}
	longest := ""
	matches := make([]service.DNSZoneMatch, 0, len(routes))
	for _, route := range routes {
		if len(route.Zone) > len(longest) {
			longest = route.Zone
		}
		matches = append(matches, service.DNSZoneMatch{
			Domain:    route.Domain,
			Zone:      route.Zone,
			AccountID: route.DNSAccountID,
			MatchMode: route.MatchMode,
			Priority:  route.Priority,
		})
	}
	ok(c, gin.H{
		"account_id":   primary,
		"account_name": accountName,
		"account_ids":  accountIDs,
		"zone":         longest,
		"resolved_by":  "binding",
		"routes":       routes,
		"matches":      matches,
	})
}
