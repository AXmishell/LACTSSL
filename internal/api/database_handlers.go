package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
)

// maintenanceGate 在数据库转换期间拒绝普通 API，只保留状态轮询与静态页面。
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

// SetMaintenance 由转换管理器回调，切换期间拒绝普通 API。
func (s *Server) SetMaintenance(active bool) { s.maintenance.Store(active) }

func (s *Server) databaseConvertManager(c *gin.Context) *dbconvert.Manager {
	if s.DBConvert == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.db_convert_unavailable")
		return nil
	}
	return s.DBConvert
}

// getDatabaseConvertStatus 返回当前数据库与转换任务进度。
func (s *Server) getDatabaseConvertStatus(c *gin.Context) {
	manager := s.databaseConvertManager(c)
	if manager == nil {
		return
	}
	ok(c, manager.CurrentStatus())
}

type databaseTargetBody struct {
	TargetDriver string `json:"target_driver"`
	TargetPath   string `json:"target_path"`
	TargetDSN    string `json:"target_dsn"`
	Force        bool   `json:"force"`
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
	if driver == db.Postgres && dsn == "" {
		failKey(c, http.StatusBadRequest, "api.error.db_convert_dsn_required")
		return dbconvert.StartRequest{}, false
	}
	return dbconvert.StartRequest{
		TargetDriver: driver,
		TargetPath:   path,
		TargetDSN:    dsn,
		Force:        body.Force,
	}, true
}

// testDatabaseTarget 只测试目标连接，不修改数据。
func (s *Server) testDatabaseTarget(c *gin.Context) {
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

// startDatabaseConvert 启动异步转换。调用方需要二次认证并输入确认词。
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
	if !s.requireReauth(c, user, body.AccountPassword, strings.TrimSpace(body.TOTP)) {
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
		TargetDriver: driver,
		TargetPath:   strings.TrimSpace(body.TargetPath),
		TargetDSN:    strings.TrimSpace(body.TargetDSN),
		Force:        body.Force,
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
	if driver == db.Postgres && req.TargetDSN == "" {
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

// rollbackDatabaseConvert 用当前数据反向转换回转换前的后端。
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
	if !s.requireReauth(c, user, body.AccountPassword, strings.TrimSpace(body.TOTP)) {
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

// cancelDatabaseConvert 取消尚未切换的转换。
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
