package api

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

// exportSystemLogs 导出当前筛选条件下的全部系统日志。
// 与操作日志一致：先写临时文件，再作为附件返回，避免中途失败产生半截文件。
func (s *Server) exportSystemLogs(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.limiter.Allow("system-logs:export:"+strconv.FormatInt(user.ID, 10), 5, 10*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	filter, err := parseSystemLogFilter(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "csv")))
	if format != "csv" && format != "json" {
		failKey(c, http.StatusBadRequest, "api.error.logs_export_format_invalid")
		return
	}
	location, okLocation := logExportLocation(c.Query("tz"))
	if !okLocation {
		failKey(c, http.StatusBadRequest, "api.error.logs_timezone_invalid")
		return
	}
	maxID, err := s.Store.MaxSystemLogID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	tmp, err := os.CreateTemp("", "lactssl-system-logs-*."+format)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	path := tmp.Name()
	writer := bufio.NewWriter(tmp)
	count, err := s.writeSystemLogExport(writer, format, filter, maxID, location, locale(c))
	if err == nil {
		err = writer.Flush()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		if errors.Is(err, errLogExportTooLarge) {
			failKey(c, http.StatusBadRequest, "api.error.logs_export_too_large")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	defer os.Remove(path)

	s.Store.AddLogKey("settings", 0, "info", "log.settings.system_logs_exported", map[string]any{"count": count})
	filename := "lactssl-system-logs-" + time.Now().In(location).Format("20060102-150405") + "." + format
	contentType := "text/csv; charset=utf-8"
	if format == "json" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(path, filename)
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
	if _, err := w.Write([]byte("[")); err != nil {
		return 0, err
	}
	first := true
	count, err := s.eachSystemLog(filter, maxID, func(row model.SystemLogEntry) error {
		raw, err := json.Marshal(systemLogExportEntry{
			ID:        row.ID,
			Level:     row.Level,
			Component: row.Component,
			Message:   row.Message,
			Fields:    parseSystemLogFields(row.Fields),
			CreatedAt: row.CreatedAt.In(location).Format(time.RFC3339),
		})
		if err != nil {
			return err
		}
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		_, err = w.Write(raw)
		return err
	})
	if err != nil {
		return count, err
	}
	if _, err := w.Write([]byte("]")); err != nil {
		return count, err
	}
	return count, nil
}

// eachSystemLog 按 ID 游标分批读取系统日志，每批结束后释放数据库连接。
func (s *Server) eachSystemLog(filter store.SystemLogFilter, maxID int64, fn func(model.SystemLogEntry) error) (int, error) {
	cursor := int64(0)
	count := 0
	for {
		rows, err := s.Store.ExportSystemLogs(filter, maxID, cursor, logExportPageSize)
		if err != nil {
			return count, err
		}
		if len(rows) == 0 {
			return count, nil
		}
		for _, row := range rows {
			if count >= logExportMaxRows {
				return count, errLogExportTooLarge
			}
			if err := fn(row); err != nil {
				return count, err
			}
			count++
		}
		cursor = rows[len(rows)-1].ID
		if len(rows) < logExportPageSize {
			return count, nil
		}
	}
}

func logComponentLabel(lang i18n.Locale, component string) string {
	key := "log.export.component." + component
	label := i18n.T(lang, key, nil)
	if label == key {
		return component
	}
	return label
}
