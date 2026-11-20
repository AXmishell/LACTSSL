package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/upgrade"
)

// programUpdater 返回程序升级工具，未注入时返回统一错误。
func (s *Server) programUpdater(c *gin.Context) *upgrade.ProgramUpdater {
	if s.Program == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.program_unavailable")
		return nil
	}
	return s.Program
}

// getProgramStatus 返回当前版本、最新 Release 与升级能力，不修改任何文件。
func (s *Server) getProgramStatus(c *gin.Context) {
	updater := s.programUpdater(c)
	if updater == nil {
		return
	}
	ok(c, updater.Status(c.Request.Context(), false))
}

// checkProgram 忽略检查间隔立即查询 GitHub Release。
func (s *Server) checkProgram(c *gin.Context) {
	updater := s.programUpdater(c)
	if updater == nil {
		return
	}
	ok(c, updater.Status(c.Request.Context(), true))
}

// applyProgram 下载、校验并替换当前二进制；重启由用户随后触发。
func (s *Server) applyProgram(c *gin.Context) {
	updater := s.programUpdater(c)
	if updater == nil {
		return
	}
	var body struct {
		Version  string `json:"version"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	_ = c.ShouldBindJSON(&body)
	// 浏览器会话替换程序二进制前必须再次证明身份；API 密钥仍由
	// upgrade:write 这一敏感 scope 约束。
	if apiKeyFromContext(c) == nil && !s.requireReauth(c, getUser(c), body.Password, body.TOTP) {
		return
	}

	report, err := updater.Apply(c.Request.Context(), body.Version)
	if err != nil {
		s.Store.AddLogKey("system", 0, "error", "log.system.program_update_failed", map[string]any{"error": err.Error()})
		if errors.Is(err, upgrade.ErrSignatureRequired) {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("system", 0, "info", "log.system.program_update_completed", map[string]any{
		"from_version": report.FromVersion,
		"to_version":   report.ToVersion,
		"asset":        report.Asset,
	})
	ok(c, report)
}

// restartProgram 在响应返回后触发优雅退出，并由 main 重新执行新二进制。
func (s *Server) restartProgram(c *gin.Context) {
	updater := s.programUpdater(c)
	if updater == nil {
		return
	}
	if !updater.RestartPending() {
		failKey(c, http.StatusConflict, "api.error.program_restart_not_pending")
		return
	}
	if s.RestartProgram == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.program_restart_unavailable")
		return
	}
	s.Store.AddLogKey("system", 0, "info", "log.system.program_restart", nil)
	ok(c, gin.H{"restarting": true})
	// 给 HTTP 响应留出写回时间，再关闭监听并 exec 新二进制。
	s.goSafe("program-restart", func() {
		time.Sleep(time.Second)
		s.RestartProgram()
	})
}
