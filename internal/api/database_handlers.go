package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
	"lactssl/internal/i18n"
	"lactssl/internal/ratelimit"
)

// maintenanceGate 在数据库切换期间拒绝普通 API，只保留状态轮询与静态页面。
func (s *Server) maintenanceGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.maintenance.Load() {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		allowed := strings.HasSuffix(path, "/api/upgrade/database") ||
			strings.HasSuffix(path, "/api/upgrade/database/convert") ||
			strings.HasSuffix(path, "/api/upgrade/database/convert/cancel") ||
			strings.HasSuffix(path, "/api/auth/me") ||
			strings.HasSuffix(path, "/api/health") ||
			!strings.Contains(path, "/api/")
		if allowed {
			c.Next()
			return
		}
		failKey(c, http.StatusServiceUnavailable, "api.error.maintenance")
		c.Abort()
	}
}

// SetMaintenance 由切换管理器回调，切换期间拒绝普通 API。
func (s *Server) SetMaintenance(active bool) { s.maintenance.Store(active) }

func (s *Server) databaseConvertManager(c *gin.Context) *dbconvert.Manager {
	if s.DBConvert == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.db_convert_unavailable")
		return nil
	}
	return s.DBConvert
}

// getDatabaseConvertStatus 返回当前数据库与切换任务进度。
func (s *Server) getDatabaseConvertStatus(c *gin.Context) {
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	ok(c, manager.CurrentStatus())
}

type databaseTargetBody struct {
	TargetDriver    string `json:"target_driver"`
	TargetPath      string `json:"target_path"`
	TargetDSN       string `json:"target_dsn"`
	Force           bool   `json:"force"`
	AllowSameDriver bool   `json:"allow_same_driver"`
}

func (s *Server) targetRequest(c *gin.Context) (dbconvert.StartRequest, bool) {
	var body databaseTargetBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return dbconvert.StartRequest{}, false
	}
	if strings.TrimSpace(body.TargetDriver) == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_driver_invalid")
		return dbconvert.StartRequest{}, false
	}
	driver, valid := db.ParseDialect(body.TargetDriver)
	if !valid || driver == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_driver_invalid")
		return dbconvert.StartRequest{}, false
	}
	path := strings.TrimSpace(body.TargetPath)
	if driver == db.SQLite {
		if path == "" {
			failKey(c, http.StatusBadRequest, "api.error.db_convert_path_required")
			return dbconvert.StartRequest{}, false
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.invalid_params")
			return dbconvert.StartRequest{}, false
		}
		dataDir, _ := filepath.Abs(s.DataDir)
		rel, err := filepath.Rel(dataDir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			failKey(c, http.StatusBadRequest, "api.error.db_convert_path_outside")
			return dbconvert.StartRequest{}, false
		}
		path = abs
	}
	dsn := strings.TrimSpace(body.TargetDSN)
	if driver != db.SQLite {
		if dsn == "" {
			failKey(c, http.StatusBadRequest, "api.error.db_convert_dsn_required")
			return dbconvert.StartRequest{}, false
		}
		if err := validateDatabaseTargetDSN(c.Request.Context(), driver, dsn); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return dbconvert.StartRequest{}, false
		}
	}
	return dbconvert.StartRequest{
		TargetDriver:    driver,
		TargetPath:      path,
		TargetDSN:       dsn,
		Force:           body.Force,
		AllowSameDriver: body.AllowSameDriver,
	}, true
}

// databaseTargetRateLimit 限制数据库连通性测试频率，避免被当作高速端口扫描器。
func (s *Server) databaseTargetRateLimit(c *gin.Context) bool {
	key := "db-target:" + s.clientRateKey(c)
	if apiKeyFromContext(c) != nil {
		key = "db-target:" + currentSessionID(c)
	}
	if allowed, wait := s.allow(ratelimit.DatabaseOperation, key); !allowed {
		rateLimited(c, wait)
		return false
	}
	return true
}

// validateDatabaseTargetDSN 拒绝云元数据/链路本地等不应作为数据库目标的主机，
// 防止把“数据库连通性测试”变成元数据服务探测器。私网/回环仍允许用于真实迁移。
func validateDatabaseTargetDSN(ctx context.Context, driver db.Dialect, dsn string) error {
	host := ""
	if driver.IsMySQL() {
		address, ok := db.MySQLAddress(dsn)
		if !ok {
			return nil
		}
		host, _, _ = net.SplitHostPort(address)
	} else {
		parsed, err := url.Parse(strings.TrimSpace(dsn))
		if err != nil || parsed.Hostname() == "" {
			// 关键字式 DSN（host=...）交给 pgx 解析；这里只处理 URL 形式。
			return nil
		}
		host = parsed.Hostname()
	}
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedDatabaseTargetIP(ip) {
			return i18n.E("api.error.db_convert_target_blocked", "host", host)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		// 解析失败由实际连接阶段报错，不额外改变错误语义。
		return nil
	}
	for _, address := range addresses {
		if blockedDatabaseTargetIP(address.IP) {
			return i18n.E("api.error.db_convert_target_blocked", "host", host)
		}
	}
	return nil
}

func blockedDatabaseTargetIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// 阿里云元数据地址（不在 169.254.0.0/16）。
	if ip.Equal(net.ParseIP("100.100.100.200")) {
		return true
	}
	return false
}

// testDatabaseTarget 只测试目标连接，不修改数据。
func (s *Server) testDatabaseTarget(c *gin.Context) {
	if !s.databaseTargetRateLimit(c) {
		return
	}
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	req, valid := s.targetRequest(c)
	if !valid {
		return
	}
	result, err := manager.TestTarget(c.Request.Context(), req)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ok(c, result)
}

// preflightDatabaseConvert 返回表数量、行数与目标空库状态。
func (s *Server) preflightDatabaseConvert(c *gin.Context) {
	if !s.databaseTargetRateLimit(c) {
		return
	}
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	req, valid := s.targetRequest(c)
	if !valid {
		return
	}
	report, err := manager.Preflight(c.Request.Context(), req)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ok(c, report)
}

// startDatabaseConvert 启动异步切换。调用方需要二次认证并输入确认词。
func (s *Server) startDatabaseConvert(c *gin.Context) {
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	user := getUser(c)
	var body struct {
		TargetDriver    string `json:"target_driver"`
		TargetPath      string `json:"target_path"`
		TargetDSN       string `json:"target_dsn"`
		Force           bool   `json:"force"`
		AllowSameDriver bool   `json:"allow_same_driver"`
		AccountPassword string `json:"account_password"`
		TOTP            string `json:"totp"`
		Confirm         string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if strings.TrimSpace(body.Confirm) != "CONVERT" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_confirm")
		return
	}
	if !s.requireSessionReauth(c, user, body.AccountPassword, strings.TrimSpace(body.TOTP)) {
		return
	}
	if strings.TrimSpace(body.TargetDriver) == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_driver_invalid")
		return
	}
	driver, valid := db.ParseDialect(body.TargetDriver)
	if !valid || driver == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_driver_invalid")
		return
	}
	req := dbconvert.StartRequest{
		TargetDriver:    driver,
		TargetPath:      strings.TrimSpace(body.TargetPath),
		TargetDSN:       strings.TrimSpace(body.TargetDSN),
		Force:           body.Force,
		AllowSameDriver: body.AllowSameDriver,
	}
	if driver == db.SQLite {
		abs, err := filepath.Abs(req.TargetPath)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.invalid_params")
			return
		}
		dataDir, _ := filepath.Abs(s.DataDir)
		rel, err := filepath.Rel(dataDir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			failKey(c, http.StatusBadRequest, "api.error.db_convert_path_outside")
			return
		}
		req.TargetPath = abs
	}
	if driver != db.SQLite && req.TargetDSN == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_dsn_required")
		return
	}
	s.Store.AddLogKey("system", 0, "warning", "log.system.database_convert_started", map[string]any{
		"from": manager.SourceDriver.String(),
		"to":   driver.String(),
	})
	state, err := manager.Start(req)
	if err != nil {
		if errors.Is(err, dbconvert.ErrBusy) {
			failKey(c, http.StatusConflict, "api.error.db_convert_busy")
			return
		}
		failErr(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": state})
}

// rollbackDatabaseConvert 用当前数据反向切换回切换前的后端。
func (s *Server) rollbackDatabaseConvert(c *gin.Context) {
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	user := getUser(c)
	var body struct {
		AccountPassword string `json:"account_password"`
		TOTP            string `json:"totp"`
		Confirm         string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if strings.TrimSpace(body.Confirm) != "ROLLBACK" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_rollback_confirm")
		return
	}
	if !s.requireSessionReauth(c, user, body.AccountPassword, strings.TrimSpace(body.TOTP)) {
		return
	}
	s.Store.AddLogKey("system", 0, "warning", "log.system.database_convert_started", map[string]any{
		"from": manager.SourceDriver.String(),
		"to":   "rollback",
	})
	state, err := manager.Rollback()
	if err != nil {
		if errors.Is(err, dbconvert.ErrBusy) {
			failKey(c, http.StatusConflict, "api.error.db_convert_busy")
			return
		}
		failErr(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": state})
}

// cancelDatabaseConvert 取消尚未进入切换阶段的切换任务。
func (s *Server) cancelDatabaseConvert(c *gin.Context) {
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	if err := manager.Cancel(); err != nil {
		failErr(c, http.StatusConflict, err)
		return
	}
	ok(c, gin.H{"cancelled": true})
}
