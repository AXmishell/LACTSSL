package api

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
)

func (s *Server) listMonitors(c *gin.Context) {
	list, err := s.Store.ListMonitors()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	for index := range list {
		list[index].Message = localize(c, list[index].Message)
	}
	ok(c, list)
}

type monitorBody struct {
	Name          string `json:"name"`
	Target        string `json:"target"`
	IntervalHours int    `json:"interval_hours"`
	Enabled       bool   `json:"enabled"`
}

func (b *monitorBody) normalize() error {
	b.Name = strings.TrimSpace(b.Name)
	b.Target = strings.TrimSpace(b.Target)
	if b.Target == "" {
		return i18n.E("api.error.monitor_target_required")
	}
	if b.Name == "" {
		b.Name = b.Target
	}
	if b.IntervalHours <= 0 {
		b.IntervalHours = 24
	}
	if b.IntervalHours < 1 || b.IntervalHours > 720 {
		return i18n.E("api.error.monitor_interval_range")
	}
	return nil
}

// monitorTargetHost 提取监控目标主机名，支持 URL、host:port 与裸域名/IP。
func monitorTargetHost(target string) string {
	target = strings.TrimSpace(target)
	if strings.Contains(target, "://") {
		if parsed, err := url.Parse(target); err == nil {
			return parsed.Hostname()
		}
		return ""
	}
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return strings.Trim(target, "[]")
}

func internalMonitorIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// monitorTargetIsInternal 判断目标是否落在私网/回环/链路本地。
// 解析失败时返回 false，由实际检测阶段报告 DNS/连接错误。
func monitorTargetIsInternal(target string) bool {
	host := monitorTargetHost(target)
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return internalMonitorIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if internalMonitorIP(address.IP) {
			return true
		}
	}
	return false
}

// callerAllowsInternalMonitor 表示当前调用方是否有权监控内网/回环目标。
func (s *Server) callerAllowsInternalMonitor(c *gin.Context) bool {
	key := apiKeyFromContext(c)
	return key == nil || key.HasScope(apiScopeMonitorsInternal)
}

// requireMonitorScope 限制 API Key 对私网/回环目标做探测。
// monitors:write 只覆盖公网目标；内网探测需要额外的敏感 scope。
func (s *Server) requireMonitorScope(c *gin.Context, target string) bool {
	if s.callerAllowsInternalMonitor(c) {
		return true
	}
	if !monitorTargetIsInternal(target) {
		return true
	}
	failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeMonitorsInternal)
	return false
}

func (s *Server) createMonitor(c *gin.Context) {
	var body monitorBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.normalize(); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if !s.requireMonitorScope(c, body.Target) {
		return
	}
	monitor := &model.Monitor{
		Name:          body.Name,
		Target:        body.Target,
		IntervalHour:  body.IntervalHours,
		Enabled:       body.Enabled,
		AllowInternal: s.callerAllowsInternalMonitor(c),
	}
	if err := s.Store.CreateMonitor(monitor); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("monitor", monitor.ID, "info", "log.monitor.created", map[string]any{"target": monitor.Target})
	if monitor.Enabled {
		s.goSafe("monitor-check", func() { _ = s.Manager.CheckMonitor(monitor.ID) })
	}
	ok(c, monitor)
}

func (s *Server) updateMonitor(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	monitor, err := s.Store.GetMonitor(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.monitor_not_found")
		return
	}
	var body monitorBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.normalize(); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	// 不具备内网监控权限的 Key 不能修改已有的内网监控项，避免借修改
	// 目标/触发检查复用该监控项的内网拨号能力。
	if monitor.AllowInternal && !s.callerAllowsInternalMonitor(c) {
		failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeMonitorsInternal)
		return
	}
	if !s.requireMonitorScope(c, body.Target) {
		return
	}
	monitor.Name = body.Name
	monitor.Target = body.Target
	monitor.IntervalHour = body.IntervalHours
	monitor.Enabled = body.Enabled
	monitor.AllowInternal = s.callerAllowsInternalMonitor(c)
	if err := s.Store.UpdateMonitor(monitor); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if monitor.Enabled {
		s.goSafe("monitor-check", func() { _ = s.Manager.CheckMonitor(monitor.ID) })
	}
	ok(c, monitor)
}

func (s *Server) deleteMonitor(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if err := s.Store.DeleteMonitor(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func (s *Server) checkMonitor(c *gin.Context) {
	if allowed, wait := s.allow(ratelimit.MonitorCheck, "monitor:check:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	id, okID := parseID(c)
	if !okID {
		return
	}
	monitor, err := s.Store.GetMonitor(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.monitor_not_found")
		return
	}
	if monitor.AllowInternal && !s.callerAllowsInternalMonitor(c) {
		failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeMonitorsInternal)
		return
	}
	if !s.requireMonitorScope(c, monitor.Target) {
		return
	}
	s.goSafe("monitor-check", func() { _ = s.Manager.CheckMonitor(id) })
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.monitor_check_started", nil)})
}
