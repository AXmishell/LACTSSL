package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

// getZotrusOrder 返回任务当前等待手动 DNS 验证的零信订单。
func (s *Server) getZotrusOrder(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	task, err := s.Store.GetTask(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.task_not_found")
		return
	}
	order, err := s.Manager.GetZotrusOrder(task.ID)
	if err != nil {
		failErr(c, http.StatusNotFound, err)
		return
	}
	ok(c, order)
}

// continueZotrusTask 在用户确认已添加 DNS 记录后继续零信订单。
func (s *Server) continueZotrusTask(c *gin.Context) {
	s.runZotrusTask(c, true)
}

// resetZotrusTask 丢弃当前验证记录并重新创建零信订单。
func (s *Server) resetZotrusTask(c *gin.Context) {
	s.runZotrusTask(c, false)
}

func (s *Server) runZotrusTask(c *gin.Context, resume bool) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	task, err := s.Store.GetTask(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.task_not_found")
		return
	}
	if task.Status == "running" {
		failKey(c, http.StatusBadRequest, "api.error.task_running")
		return
	}
	if task.CAPoolID != 0 || strings.TrimSpace(task.CA) != "zotrus" {
		failKey(c, http.StatusBadRequest, "api.error.zotrus_not_waiting")
		return
	}
	if !s.requireAPIKeyTaskDNSAccess(c, s.taskDNSAccountIDs(task)) {
		return
	}
	if s.taskUsesPrivateCA(task.EffectivePipeline(), task.CAPoolID) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if s.taskUsesCustomCA(task.EffectivePipeline(), task.CA, task.CAPoolID) && !s.requireAPIKeyCARead(c) {
		return
	}
	if requiresDeployExecutionFor(task.FlowMode, task.EffectivePipeline(), task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if s.taskUsesDeployCommand(task.FlowMode, task.EffectivePipeline(), task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	if resume && task.Status != "waiting_dns" {
		failKey(c, http.StatusBadRequest, "api.error.zotrus_not_waiting")
		return
	}
	if !resume {
		if err := s.Manager.ResetZotrusOrder(task.ID); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	snapshot := *task
	s.goSafe("task-zotrus", func() {
		_ = s.Manager.RunTaskWithOptions(id, "task.trigger.manual", service.RunOptions{
			ForceIssue:   true,
			ResumeZotrus: resume,
			Task:         &snapshot,
		})
	})
	messageKey := "api.ok.zotrus_started"
	if resume {
		messageKey = "api.ok.zotrus_verifying"
	}
	ok(c, gin.H{"message": i18n.T(locale(c), messageKey, nil)})
}
