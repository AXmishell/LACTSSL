package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/store"
)

// 导出上限用于避免一次性把过大的结果集写入临时文件或浏览器内存。
const (
	logExportMaxRows  = 200000
	logExportPageSize = 1000
)

var errLogExportTooLarge = errors.New("log export exceeds row limit")

// listLogs 分页查询日志，支持来源、级别、关键字与时间范围筛选。
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
	exportLogFile(s, c, logExportSpec[store.LogFilter]{
		tempPrefix:     "lactssl-logs",
		filenamePrefix: "lactssl-logs",
		rateRule:       ratelimit.LogExport,
		rateKey:        "logs:export:",
		auditKey:       "log.settings.logs_exported",
		parseFilter:    parseLogFilter,
		maxID:          s.Store.MaxLogID,
		write:          s.writeLogExport,
	})
}

// parseLogFilter 解析并校验列表与导出共用的筛选参数。
func parseLogFilter(c *gin.Context) (store.LogFilter, error) {
	filter := store.LogFilter{
		Source:  strings.TrimSpace(c.Query("source")),
		RefType: strings.TrimSpace(c.Query("ref_type")),
		Level:   strings.ToLower(strings.TrimSpace(c.Query("level"))),
		Query:   strings.TrimSpace(c.Query("q")),
	}
	if len([]rune(filter.RefType)) > 32 {
		return filter, i18n.E("api.error.invalid_params")
	}
	if raw := strings.TrimSpace(c.Query("ref_id")); raw != "" {
		refID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || refID < 0 {
			return filter, i18n.E("api.error.invalid_params")
		}
		filter.RefID = refID
	}
	// 复用系统日志的级别词表时，把 warn 归一化为操作日志使用的 warning。
	if filter.Level == "warn" {
		filter.Level = "warning"
	}
	if filter.Level != "" && !validLogLevel(filter.Level) {
		return filter, i18n.E("api.error.logs_level_invalid")
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

func validLogLevel(level string) bool {
	switch level {
	case "info", "success", "warning", "error":
		return true
	}
	return false
}

// clearLogSpec 描述操作日志与系统日志清空的差异点。
type clearLogSpec struct {
	rateRule string
	rateKey  string
	auditKey string
	clear    func(reason string, operatorID int64, operatorEmail string) (int64, *store.LogArchiveResult, error)
}

// clearLogWithArchive 执行清空、归档结果回传与审计日志写入的公共流程。
// 必须先清空再写审计日志，保证清空动作本身不会被一起删除。
func clearLogWithArchive(s *Server, c *gin.Context, spec clearLogSpec) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(spec.rateRule, spec.rateKey+strconv.FormatInt(user.ID, 10)); !allowed {
		rateLimited(c, wait)
		return
	}
	count, archived, err := spec.clear("manual_clear", user.ID, user.Email)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	params := map[string]any{"count": count}
	if archived != nil {
		params["archive_id"] = archived.ID
		params["archive_file"] = archived.File
	}
	s.Store.AddLogKey("settings", 0, "warning", spec.auditKey, params)
	ok(c, gin.H{"deleted": count, "archived": archived})
}

// clearLogs 清空操作日志。该操作本身会写入一条审计日志，便于事后追溯。
func (s *Server) clearLogs(c *gin.Context) {
	clearLogWithArchive(s, c, clearLogSpec{
		rateRule: ratelimit.LogClear,
		rateKey:  "logs:clear:",
		auditKey: "log.settings.logs_cleared",
		clear:    s.Store.ClearOperationLogsWithArchive,
	})
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
	return writeJSONArray(w, func(fn func(model.LogEntry) error) (int, error) {
		return s.eachLog(filter, maxID, fn)
	}, func(row model.LogEntry) ([]byte, error) {
		return json.Marshal(logExportEntry{
			ID:        row.ID,
			Source:    row.Source,
			RefID:     row.RefID,
			Level:     row.Level,
			Message:   i18n.Render(lang, row.Message),
			CreatedAt: row.CreatedAt.In(location).Format(time.RFC3339),
		})
	})
}

// eachLog 按 ID 游标分批读取日志，每批结束后释放数据库连接。
func (s *Server) eachLog(filter store.LogFilter, maxID int64, fn func(model.LogEntry) error) (int, error) {
	return eachLogPage(func(cursor int64) ([]model.LogEntry, error) {
		return s.Store.ExportLogs(filter, maxID, cursor, logExportPageSize)
	}, func(row model.LogEntry) int64 { return row.ID }, fn)
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
