package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
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
	monitor := &model.Monitor{Name: body.Name, Target: body.Target, IntervalHour: body.IntervalHours, Enabled: body.Enabled}
	if err := s.Store.CreateMonitor(monitor); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("monitor", monitor.ID, "info", "log.monitor.created", map[string]any{"target": monitor.Target})
	if monitor.Enabled {
		go func() { _ = s.Manager.CheckMonitor(monitor.ID) }()
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
	monitor.Name = body.Name
	monitor.Target = body.Target
	monitor.IntervalHour = body.IntervalHours
	monitor.Enabled = body.Enabled
	if err := s.Store.UpdateMonitor(monitor); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if monitor.Enabled {
		go func() { _ = s.Manager.CheckMonitor(monitor.ID) }()
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
	id, okID := parseID(c)
	if !okID {
		return
	}
	if _, err := s.Store.GetMonitor(id); err != nil {
		failKey(c, http.StatusNotFound, "api.error.monitor_not_found")
		return
	}
	go func() { _ = s.Manager.CheckMonitor(id) }()
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.monitor_check_started", nil)})
}

func (s *Server) listLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	source := strings.TrimSpace(c.Query("source"))
	list, total, err := s.Store.ListLogs(source, size, (page-1)*size)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"items": localizeLogs(c, list), "total": total, "page": page, "size": size})
}
