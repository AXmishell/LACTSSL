package api

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/store"
)

// exportSystemLogs 导出当前筛选条件下的全部系统日志。
// 与操作日志一致：先写临时文件，再作为附件返回，避免中途失败产生半截文件。
func (s *Server) exportSystemLogs(c *gin.Context) {
	exportLogFile(s, c, logExportSpec[store.SystemLogFilter]{
		tempPrefix:     "lactssl-system-logs",
		filenamePrefix: "lactssl-system-logs",
		rateRule:       ratelimit.SystemLogExport,
		rateKey:        "system-logs:export:",
		auditKey:       "log.settings.system_logs_exported",
		parseFilter:    parseSystemLogFilter,
		maxID:          s.Store.MaxSystemLogID,
		write:          s.writeSystemLogExport,
	})
}

func (s *Server) writeSystemLogExport(w io.Writer, format string, filter store.SystemLogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	if format == "json" {
		return s.writeSystemLogJSON(w, filter, maxID, location, lang)
	}
	return s.writeSystemLogCSV(w, filter, maxID, location, lang)
}

func (s *Server) writeSystemLogCSV(w io.Writer, filter store.SystemLogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	// Excel/WPS 需要 BOM 才能正确识别 UTF-8 中文。
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return 0, err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write([]string{
		i18n.T(lang, "log.export.time", nil),
		i18n.T(lang, "log.export.level", nil),
		i18n.T(lang, "log.export.component", nil),
		i18n.T(lang, "log.export.content", nil),
		i18n.T(lang, "log.export.fields", nil),
	}); err != nil {
		return 0, err
	}
	count, err := s.eachSystemLog(filter, maxID, func(row model.SystemLogEntry) error {
		return writer.Write([]string{
			formatLogTime(row.CreatedAt, location),
			logLevelLabel(lang, row.Level),
			logComponentLabel(lang, row.Component),
			csvSafe(row.Message),
			csvSafe(row.Fields),
		})
	})
	if err != nil {
		return count, err
	}
	writer.Flush()
	return count, writer.Error()
}

type systemLogExportEntry struct {
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	CreatedAt string         `json:"created_at"`
}

func (s *Server) writeSystemLogJSON(w io.Writer, filter store.SystemLogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	return writeJSONArray(w, func(fn func(model.SystemLogEntry) error) (int, error) {
		return s.eachSystemLog(filter, maxID, fn)
	}, func(row model.SystemLogEntry) ([]byte, error) {
		return json.Marshal(systemLogExportEntry{
			ID:        row.ID,
			Level:     row.Level,
			Component: row.Component,
			Message:   row.Message,
			Fields:    parseSystemLogFields(row.Fields),
			CreatedAt: row.CreatedAt.In(location).Format(time.RFC3339),
		})
	})
}

// eachSystemLog 按 ID 游标分批读取系统日志，每批结束后释放数据库连接。
func (s *Server) eachSystemLog(filter store.SystemLogFilter, maxID int64, fn func(model.SystemLogEntry) error) (int, error) {
	return eachLogPage(func(cursor int64) ([]model.SystemLogEntry, error) {
		return s.Store.ExportSystemLogs(filter, maxID, cursor, logExportPageSize)
	}, func(row model.SystemLogEntry) int64 { return row.ID }, fn)
}

func logComponentLabel(lang i18n.Locale, component string) string {
	key := "log.export.component." + component
	label := i18n.T(lang, key, nil)
	if label == key {
		return component
	}
	return label
}
