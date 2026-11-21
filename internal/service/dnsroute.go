package service

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// routedDNSProvider 把 lego 的单个 DNS provider 拆成按域名路由的组合 provider。
// 一张证书可以包含多个域名，每个域名在 Present 时选择各自的 DNS 账户，
// CleanUp 则按 Present 时记录的结果回到原账户清理。
type routedDNSProvider struct {
	mu        sync.Mutex
	routes    map[string]model.DNSRoute
	providers map[int64]challenge.Provider
	used      map[string]int64
}

func newRoutedDNSProvider(routes []model.DNSRoute, accounts map[int64]*model.DNSAccount) (challenge.Provider, func(), error) {
	if len(routes) == 0 {
		return nil, func() {}, i18n.E("service.acme.dns_account_required")
	}
	routesByDomain := make(map[string]model.DNSRoute, len(routes))
	for _, route := range routes {
		domain := canonicalCertificateDomain(route.Domain)
		if domain == "" || route.DNSAccountID <= 0 {
			return nil, func() {}, i18n.E("service.acme.dns_route_invalid", "domain", route.Domain)
		}
		if existing, ok := routesByDomain[domain]; ok && existing.DNSAccountID != route.DNSAccountID {
			return nil, func() {}, i18n.E("service.acme.dns_route_conflict",
				"domain", domain,
				"accounts", formatAccountIDs([]int64{existing.DNSAccountID, route.DNSAccountID}))
		}
		route.Domain = domain
		routesByDomain[domain] = route
	}

	providers := make(map[int64]challenge.Provider)
	undos := make([]func(), 0, len(accounts))
	undoAll := func() {
		for index := len(undos) - 1; index >= 0; index-- {
			undos[index]()
		}
	}
	for _, accountID := range DNSRouteAccountIDs(routes) {
		account := accounts[accountID]
		if account == nil {
			undoAll()
			return nil, func() {}, i18n.E("service.acme.dns_account_not_found", "id", accountID)
		}
		provider, undo, err := newDNSProvider(account.Provider, account.Config)
		if err != nil {
			undoAll()
			return nil, func() {}, err
		}
		providers[accountID] = provider
		undos = append(undos, undo)
	}
	return &routedDNSProvider{
		routes:    routesByDomain,
		providers: providers,
		used:      make(map[string]int64),
	}, undoAll, nil
}

func (p *routedDNSProvider) routeFor(domain string) (model.DNSRoute, bool) {
	canonical := canonicalCertificateDomain(domain)
	if route, ok := p.routes[canonical]; ok {
		return route, true
	}
	// 兼容只有一个账户的调用方：未显式路由的域名全部走该账户。
	if len(p.providers) == 1 {
		for accountID := range p.providers {
			return model.DNSRoute{Domain: canonical, DNSAccountID: accountID}, true
		}
	}
	return model.DNSRoute{}, false
}

func (p *routedDNSProvider) Present(domain, token, keyAuth string) error {
	route, ok := p.routeFor(domain)
	if !ok {
		return i18n.E("service.acme.dns_route_not_found", "domain", domain)
	}
	provider := p.providers[route.DNSAccountID]
	if provider == nil {
		return i18n.E("service.acme.dns_account_not_found", "id", route.DNSAccountID)
	}
	if err := provider.Present(domain, token, keyAuth); err != nil {
		return err
	}
	p.mu.Lock()
	p.used[dnsRouteKey(domain, token)] = route.DNSAccountID
	p.mu.Unlock()
	return nil
}

func (p *routedDNSProvider) CleanUp(domain, token, keyAuth string) error {
	key := dnsRouteKey(domain, token)
	p.mu.Lock()
	accountID, recorded := p.used[key]
	p.mu.Unlock()

	route, ok := p.routeFor(domain)
	if recorded {
		if existing, found := p.routes[canonicalCertificateDomain(domain)]; found {
			route = existing
		}
		route.DNSAccountID = accountID
		ok = true
	}
	if !ok {
		return i18n.E("service.acme.dns_route_not_found", "domain", domain)
	}
	provider := p.providers[route.DNSAccountID]
	if provider == nil {
		return i18n.E("service.acme.dns_account_not_found", "id", route.DNSAccountID)
	}
	err := provider.CleanUp(domain, token, keyAuth)
	p.mu.Lock()
	delete(p.used, key)
	p.mu.Unlock()
	return err
}

// Timeout 取所有账户 provider 中最长的传播等待时间，避免多账户证书被较短超时提前打断。
func (p *routedDNSProvider) Timeout() (time.Duration, time.Duration) {
	timeout, interval := 180*time.Second, 2*time.Second
	for _, provider := range p.providers {
		timed, ok := provider.(challenge.ProviderTimeout)
		if !ok {
			continue
		}
		providerTimeout, providerInterval := timed.Timeout()
		if providerTimeout > timeout {
			timeout = providerTimeout
		}
		if providerInterval > 0 && (interval <= 0 || providerInterval < interval) {
			interval = providerInterval
		}
	}
	return timeout, interval
}

func dnsRouteKey(domain, token string) string {
	return canonicalCertificateDomain(domain) + "\x00" + token
}

// resolveACMEDNSRoutes 把请求转换为按域名路由与所需账户集合。
// DNSRoutes 优先；只传 DNSAccount 的旧调用方会为整张证书生成同账户路由。
func (m *Manager) resolveACMEDNSRoutes(req ACMERequest) ([]model.DNSRoute, map[int64]*model.DNSAccount, error) {
	routes := req.DNSRoutes
	if len(routes) == 0 {
		if req.DNSAccount == nil {
			return nil, nil, i18n.E("service.acme.dns_account_required")
		}
		var err error
		routes, err = ExplicitDNSRoutesForDomains(req.Domains, req.DNSAccount.ID)
		if err != nil {
			return nil, nil, err
		}
	}
	accounts := make(map[int64]*model.DNSAccount)
	if req.DNSAccount != nil {
		accounts[req.DNSAccount.ID] = req.DNSAccount
	}
	for _, accountID := range DNSRouteAccountIDs(routes) {
		if accounts[accountID] != nil {
			continue
		}
		account, err := m.Store.GetDNSAccount(accountID)
		if err != nil {
			return nil, nil, i18n.E("service.acme.dns_account_not_found", "id", accountID)
		}
		accounts[accountID] = account
	}
	return routes, accounts, nil
}

// resolveTaskDNSRoutes 为本次签发的域名集合确定 DNS 路由。
// 优先使用任务保存的按域名快照；目标流程 Beta 运行时发现的新域名再按绑定补全。
func (m *Manager) resolveTaskDNSRoutes(task *model.Task, cfg model.PipelineConfig, domains []string) ([]model.DNSRoute, error) {
	if task == nil || len(domains) == 0 {
		return nil, i18n.E("task.error.domains_empty")
	}
	explicitID := cfg.DNSAccountID
	if explicitID == 0 && len(task.DNSRoutes) == 0 {
		explicitID = task.DNSAccountID
	}
	if explicitID > 0 {
		return ExplicitDNSRoutesForDomains(domains, explicitID)
	}

	snapshot := make(map[string]model.DNSRoute, len(task.DNSRoutes))
	for _, route := range task.DNSRoutes {
		domain := canonicalCertificateDomain(route.Domain)
		if domain == "" || route.DNSAccountID <= 0 {
			continue
		}
		if existing, ok := snapshot[domain]; ok && existing.DNSAccountID != route.DNSAccountID {
			return nil, i18n.E("service.acme.dns_route_conflict",
				"domain", domain,
				"accounts", formatAccountIDs([]int64{existing.DNSAccountID, route.DNSAccountID}))
		}
		route.Domain = domain
		snapshot[domain] = route
	}

	routes := make([]model.DNSRoute, 0, len(domains))
	missing := make([]string, 0)
	seen := make(map[string]bool, len(domains))
	for _, raw := range domains {
		domain := canonicalCertificateDomain(raw)
		if domain == "" {
			return nil, i18n.E("task.error.domain_invalid", "domain", raw)
		}
		if seen[domain] {
			continue
		}
		seen[domain] = true
		if route, ok := snapshot[domain]; ok {
			routes = append(routes, route)
			continue
		}
		if task.DNSAccountID > 0 {
			routes = append(routes, model.DNSRoute{
				Domain:       domain,
				DNSAccountID: task.DNSAccountID,
				ResolvedBy:   "explicit",
			})
			continue
		}
		missing = append(missing, domain)
	}
	if len(missing) > 0 {
		bindings, err := m.Store.ListEnabledDNSZoneBindings()
		if err != nil {
			return nil, err
		}
		resolved, err := ResolveDNSRoutesForDomains(missing, bindings)
		if err != nil {
			return nil, err
		}
		routes = append(routes, resolved...)
	}
	if len(routes) == 0 {
		return nil, i18n.E("task.error.dns_account_required")
	}
	return routes, nil
}

// lockDNSAccounts 按账户 ID 升序获取 dnsLocks，避免多账户并发时出现死锁。
func (m *Manager) lockDNSAccounts(accountIDs []int64) func() {
	unlocks := make([]func(), 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		unlocks = append(unlocks, m.dnsLocks.Lock(strconv.FormatInt(accountID, 10)))
	}
	return func() {
		for index := len(unlocks) - 1; index >= 0; index-- {
			unlocks[index]()
		}
	}
}

func formatAccountIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ")
}
