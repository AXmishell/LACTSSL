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
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

// 导出上限用于避免一次性把过大的结果集写入临时文件或浏览器内存。
const (
	logExportMaxRows  = 200000
	logExportPageSize = 1000
)

var errLogExportTooLarge = errors.New("log export exceeds row limit")

// listLogs 分页查询日志，支持来源、级别与时间范围筛选。
func (s *Server) listLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	filter, err := parseLogFilter(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	list, total, err := s.Store.ListLogsFiltered(filter, size, (page-1)*size)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"items": localizeLogs(c, list), "total": total, "page": page, "size": size})
}

// exportLogs 导出当前筛选条件下的全部日志。
// 先写临时文件再发送，确保查询中途失败时不会产生半截 CSV。
func (s *Server) exportLogs(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.limiter.Allow("logs:export:"+strconv.FormatInt(user.ID, 10), 5, 10*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	filter, err := parseLogFilter(c)
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
	maxID, err := s.Store.MaxLogID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	tmp, err := os.CreateTemp("", "lactssl-logs-*."+format)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	path := tmp.Name()
	writer := bufio.NewWriter(tmp)
	count, err := s.writeLogExport(writer, format, filter, maxID, location, locale(c))
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

	s.Store.AddLogKey("settings", 0, "info", "log.settings.logs_exported", map[string]any{"count": count})
	filename := "lactssl-logs-" + time.Now().In(location).Format("20060102-150405") + "." + format
	contentType := "text/csv; charset=utf-8"
	if format == "json" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(path, filename)
}

// parseLogFilter 解析并校验列表与导出共用的筛选参数。
func parseLogFilter(c *gin.Context) (store.LogFilter, error) {
	filter := store.LogFilter{
		Source: strings.TrimSpace(c.Query("source")),
		Level:  strings.TrimSpace(c.Query("level")),
	}
	if filter.Level != "" && !validLogLevel(filter.Level) {
		return filter, i18n.E("api.error.logs_level_invalid")
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

func validLogLevel(level string) bool {
	switch level {
	case "info", "success", "warning", "error":
		return true
	}
	return false
}

// parseLogTime 支持 RFC3339 与日期（按 UTC 零点解释）。
func parseLogTime(value string) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), true, nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), true, nil
	}
	return time.Time{}, false, errors.New("invalid log time")
}

// logExportLocation 解析时区；未指定时沿用服务端本地时区（Docker 默认 Asia/Shanghai）。
func logExportLocation(name string) (*time.Location, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local, true
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return location, true
}

func (s *Server) writeLogExport(w io.Writer, format string, filter store.LogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	if format == "json" {
		return s.writeLogJSON(w, filter, maxID, location, lang)
	}
	return s.writeLogCSV(w, filter, maxID, location, lang)
}

func (s *Server) writeLogCSV(w io.Writer, filter store.LogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	// Excel/WPS 需要 BOM 才能正确识别 UTF-8 中文。
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return 0, err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write([]string{
		i18n.T(lang, "log.export.time", nil),
		i18n.T(lang, "log.export.level", nil),
		i18n.T(lang, "log.export.source", nil),
		i18n.T(lang, "log.export.content", nil),
		i18n.T(lang, "log.export.ref_id", nil),
	}); err != nil {
		return 0, err
	}
	count, err := s.eachLog(filter, maxID, func(row model.LogEntry) error {
		return writer.Write([]string{
			formatLogTime(row.CreatedAt, location),
			logLevelLabel(lang, row.Level),
			logSourceLabel(lang, row.Source),
			csvSafe(i18n.Render(lang, row.Message)),
			strconv.FormatInt(row.RefID, 10),
		})
	})
	if err != nil {
		return count, err
	}
	writer.Flush()
	return count, writer.Error()
}

type logExportEntry struct {
	ID        int64  `json:"id"`
	Source    string `json:"source"`
	RefID     int64  `json:"ref_id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) writeLogJSON(w io.Writer, filter store.LogFilter, maxID int64, location *time.Location, lang i18n.Locale) (int, error) {
	if _, err := w.Write([]byte("[")); err != nil {
		return 0, err
	}
	first := true
	count, err := s.eachLog(filter, maxID, func(row model.LogEntry) error {
		raw, err := json.Marshal(logExportEntry{
			ID:        row.ID,
			Source:    row.Source,
			RefID:     row.RefID,
			Level:     row.Level,
			Message:   i18n.Render(lang, row.Message),
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

// eachLog 按 ID 游标分批读取日志，每批结束后释放数据库连接。
func (s *Server) eachLog(filter store.LogFilter, maxID int64, fn func(model.LogEntry) error) (int, error) {
	cursor := int64(0)
	count := 0
	for {
		rows, err := s.Store.ExportLogs(filter, maxID, cursor, logExportPageSize)
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

// csvSafe 防止 CSV 公式注入。日志内容可能包含用户填写的任务名、账户名等。
// Excel 会忽略单元格开头的空白，因此先跳过空白再判断公式前缀。
func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	}
	return value
}

func formatLogTime(value time.Time, location *time.Location) string {
	return value.In(location).Format("2006-01-02 15:04:05 -07:00")
}

func logLevelLabel(lang i18n.Locale, level string) string {
	key := "log.export.level." + level
	label := i18n.T(lang, key, nil)
	if label == key {
		return level
	}
	return label
}

func logSourceLabel(lang i18n.Locale, source string) string {
	key := "log.export.source." + source
	label := i18n.T(lang, key, nil)
	if label == key {
		return source
	}
	return label
}
