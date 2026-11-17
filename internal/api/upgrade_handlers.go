package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"lactssl/internal/upgrade"
)

// upgradeManager 返回升级工具，未注入时返回统一错误。
func (s *Server) upgradeManager(c *gin.Context) *upgrade.Manager {
	if s.Upgrade == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.upgrade_unavailable")
		return nil
	}
	return s.Upgrade
}

// getUpgradeStatus 返回迁移计划、配置预检和数据库概况，不修改任何数据。
func (s *Server) getUpgradeStatus(c *gin.Context) {
	manager := s.upgradeManager(c)
	if manager == nil {
		return
	}
	status, err := manager.Status(c.Request.Context())
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, status)
}

// runUpgrade 执行迁移、配置优化和数据库优化。
func (s *Server) runUpgrade(c *gin.Context) {
	manager := s.upgradeManager(c)
	if manager == nil {
		return
	}
	var body struct {
		Vacuum   *bool `json:"vacuum"`
		Optimize *bool `json:"optimize"`
		Config   *bool `json:"config"`
	}
	_ = c.ShouldBindJSON(&body)

	vacuum := manager.VacuumAllowed()
	if body.Vacuum != nil {
		vacuum = *body.Vacuum
	}
	optimize := true
	if body.Optimize != nil {
		optimize = *body.Optimize
	}
	configOptimize := true
	if body.Config != nil {
		configOptimize = *body.Config
	}

	report, err := manager.Run(c.Request.Context(), upgrade.RunOptions{
		Migrate:        true,
		Config:         configOptimize,
		Optimize:       optimize,
		Vacuum:         vacuum,
		Analyze:        true,
		Checkpoint:     true,
		CheckIntegrity: true,
		CreateBackup:   true,
		Source:         "manual",
	})
	if err != nil {
		s.Store.AddLogKey("system", 0, "error", "log.system.upgrade_failed", map[string]any{"error": err.Error()})
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("system", 0, "info", "log.system.upgrade_completed", map[string]any{
		"from_version":   report.FromVersion,
		"to_version":     report.ToVersion,
		"config_changes": len(report.Config.Changes),
		"reclaimed":      report.Database.ReclaimedBytes,
	})
	ok(c, report)
}

// optimizeUpgrade 只执行配置与数据库优化，不重复运行数据迁移。
func (s *Server) optimizeUpgrade(c *gin.Context) {
	manager := s.upgradeManager(c)
	if manager == nil {
		return
	}
	var body struct {
		Vacuum   *bool `json:"vacuum"`
		Analyze  *bool `json:"analyze"`
		Config   *bool `json:"config"`
		Database *bool `json:"database"`
	}
	_ = c.ShouldBindJSON(&body)

	vacuum := manager.VacuumAllowed()
	if body.Vacuum != nil {
		vacuum = *body.Vacuum
	}
	analyze := true
	if body.Analyze != nil {
		analyze = *body.Analyze
	}
	configOptimize := true
	if body.Config != nil {
		configOptimize = *body.Config
	}
	databaseOptimize := true
	if body.Database != nil {
		databaseOptimize = *body.Database
	}

	report, err := manager.Run(c.Request.Context(), upgrade.RunOptions{
		Migrate:        false,
		Config:         configOptimize,
		Optimize:       databaseOptimize,
		Vacuum:         vacuum,
		Analyze:        analyze,
		Checkpoint:     true,
		CheckIntegrity: true,
		CreateBackup:   false,
		Source:         "optimize",
	})
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("system", 0, "info", "log.system.upgrade_optimized", map[string]any{
		"config_changes": len(report.Config.Changes),
		"reclaimed":      report.Database.ReclaimedBytes,
	})
	ok(c, report)
}
