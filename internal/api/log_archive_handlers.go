package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/logarchive"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
)

// logArchiveFilter 是归档列表的筛选条件。
type logArchiveFilter struct {
	Category string
	Reason   string
	Query    string
	From     *time.Time
	To       *time.Time
}

func parseLogArchiveFilter(c *gin.Context) (logArchiveFilter, error) {
	filter := logArchiveFilter{
		Category: strings.TrimSpace(c.Query("category")),
		Reason:   strings.TrimSpace(c.Query("reason")),
		Query:    strings.TrimSpace(c.Query("q")),
	}
	if filter.Category != "" &&
		filter.Category != model.LogCategoryOperation &&
		filter.Category != model.LogCategoryRuntime {
		return filter, i18n.E("api.error.invalid_params")
	}
	switch filter.Reason {
	case "", logarchive.ReasonManualClear, logarchive.ReasonRetention, logarchive.ReasonMaxRows:
	default:
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

// listLogArchives 分页查询归档元数据。
func (s *Server) listLogArchives(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	if s.LogArchives == nil {
		ok(c, gin.H{
			"items":   []logarchive.Entry{},
			"total":   0,
			"page":    page,
			"size":    size,
			"enabled": false,
			"dir":     "",
		})
		return
	}
	filter, err := parseLogArchiveFilter(c)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	all, err := s.LogArchives.List()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	query := strings.ToLower(filter.Query)
	filtered := make([]logarchive.Entry, 0, len(all))
	for _, entry := range all {
		if filter.Category != "" && entry.Category != filter.Category {
			continue
		}
		if filter.Reason != "" && entry.Reason != filter.Reason {
			continue
		}
		if filter.From != nil && entry.CreatedAt.Before(*filter.From) {
			continue
		}
		if filter.To != nil && !entry.CreatedAt.Before(*filter.To) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				entry.ArchiveID,
				entry.Category,
				entry.Reason,
				entry.File.Name,
				entry.Operator.Email,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	total := len(filtered)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	items := filtered[start:end]
	ok(c, gin.H{
		"items":   items,
		"total":   total,
		"page":    page,
		"size":    size,
		"enabled": true,
		"dir":     s.LogArchives.Root(),
	})
}

// getLogArchive 返回单个归档的清单元数据。
func (s *Server) getLogArchive(c *gin.Context) {
	if s.LogArchives == nil {
		failKey(c, http.StatusBadRequest, "api.error.log_archive_disabled")
		return
	}
	entry, found, err := s.LogArchives.Get(strings.TrimSpace(c.Param("id")))
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !found {
		failKey(c, http.StatusNotFound, "api.error.log_archive_not_found")
		return
	}
	ok(c, entry)
}

// downloadLogArchive 下载归档数据文件。
func (s *Server) downloadLogArchive(c *gin.Context) {
	if s.LogArchives == nil {
		failKey(c, http.StatusBadRequest, "api.error.log_archive_disabled")
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.LogArchiveDownload, "log-archives:download:"+strconv.FormatInt(user.ID, 10)); !allowed {
		rateLimited(c, wait)
		return
	}
	entry, found, err := s.LogArchives.Get(strings.TrimSpace(c.Param("id")))
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !found {
		failKey(c, http.StatusNotFound, "api.error.log_archive_not_found")
		return
	}
	if entry.Missing {
		failKey(c, http.StatusNotFound, "api.error.log_archive_missing")
		return
	}
	if _, err := os.Stat(entry.DataPath); err != nil {
		failKey(c, http.StatusNotFound, "api.error.log_archive_missing")
		return
	}
	filename := filepath.Base(entry.File.Name)
	contentType := "application/x-ndjson; charset=utf-8"
	if strings.HasSuffix(filename, ".gz") {
		contentType = "application/gzip"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	s.Store.AddLogKey("settings", 0, "info", "log.settings.log_archive_downloaded", map[string]any{
		"file": filename,
	})
	c.FileAttachment(entry.DataPath, filename)
}

// deleteLogArchive 删除一个归档文件。删除动作由归档服务写入操作日志。
func (s *Server) deleteLogArchive(c *gin.Context) {
	if s.LogArchives == nil {
		failKey(c, http.StatusBadRequest, "api.error.log_archive_disabled")
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.LogArchiveDelete, "log-archives:delete:"+strconv.FormatInt(user.ID, 10)); !allowed {
		rateLimited(c, wait)
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if _, found, err := s.LogArchives.Get(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	} else if !found {
		failKey(c, http.StatusNotFound, "api.error.log_archive_not_found")
		return
	}
	if err := s.LogArchives.Delete(id); err != nil {
		if errors.Is(err, logarchive.ErrArchiveNotFound) {
			failKey(c, http.StatusNotFound, "api.error.log_archive_not_found")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"deleted": 1})
}
