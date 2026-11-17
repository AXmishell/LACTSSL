package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/store"
)

type systemLogItem struct {
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// listSystemLogs 分页查询进程日志，支持级别、组件、关键字与时间范围筛选。
func (s *Server) listSystemLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	filter, err := parseSystemLogFilter(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	list, total, err := s.Store.ListSystemLogs(filter, size, (page-1)*size)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]systemLogItem, 0, len(list))
	for _, entry := range list {
		items = append(items, systemLogItem{
			ID:        entry.ID,
			Level:     entry.Level,
			Component: entry.Component,
			Message:   entry.Message,
			Fields:    parseSystemLogFields(entry.Fields),
			CreatedAt: entry.CreatedAt,
		})
	}
	components, err := s.Store.SystemLogComponents()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{
		"items":          items,
		"total":          total,
		"page":           page,
		"size":           size,
		"components":     components,
		"retention_days": s.Store.SystemLogRetentionDays,
		"enabled":        s.SystemLogEnabled,
	})
}

// clearSystemLogs 清空系统日志。该操作写入操作日志，便于事后追溯。
func (s *Server) clearSystemLogs(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.limiter.Allow("system-logs:clear:"+strconv.FormatInt(user.ID, 10), 3, 10*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	count, err := s.Store.ClearSystemLogs()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.system_logs_cleared", map[string]any{"count": count})
	ok(c, gin.H{"deleted": count})
}

// parseSystemLogFilter 解析「系统日志」页面的筛选参数。
func parseSystemLogFilter(c *gin.Context) (store.SystemLogFilter, error) {
	filter := store.SystemLogFilter{
		Level:     strings.ToLower(strings.TrimSpace(c.Query("level"))),
		Component: strings.TrimSpace(c.Query("component")),
		Query:     strings.TrimSpace(c.Query("q")),
	}
	if filter.Level == "warning" {
		filter.Level = "warn"
	}
	if filter.Level != "" && !validSystemLogLevel(filter.Level) {
		return filter, i18n.E("api.error.invalid_params")
	}
	if filter.Component != "" && !validSystemLogComponent(filter.Component) {
		return filter, i18n.E("api.error.invalid_params")
	}
	if len([]rune(filter.Query)) > 100 {
		return filter, i18n.E("api.error.invalid_params")
	}
	from, hasFrom, err := parseLogTime(c.Query("from"))
	if err != nil {
		return filter, i18n.E("api.error.logs_time_invalid")
	}
	if hasFrom {
		filter.From = &from
	}
	to, hasTo, err := parseLogTime(c.Query("to"))
	if err != nil {
		return filter, i18n.E("api.error.logs_time_invalid")
	}
	if hasTo {
		filter.To = &to
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return filter, i18n.E("api.error.logs_time_invalid")
	}
	return filter, nil
}

func validSystemLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func validSystemLogComponent(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

func parseSystemLogFields(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	fields := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return map[string]any{"_raw": raw}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
