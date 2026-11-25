package api

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
)

// writeJSONArray 以流式方式输出 JSON 数组，避免把全部日志载入内存。
func writeJSONArray[T any](w io.Writer, each func(func(T) error) (int, error), marshal func(T) ([]byte, error)) (int, error) {
	if _, err := w.Write([]byte("[")); err != nil {
		return 0, err
	}
	first := true
	count, err := each(func(row T) error {
		raw, err := marshal(row)
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

// eachLogPage 按 ID 游标分批读取日志，每批结束后释放数据库连接。
func eachLogPage[T any](fetch func(cursor int64) ([]T, error), id func(T) int64, fn func(T) error) (int, error) {
	cursor := int64(0)
	count := 0
	for {
		rows, err := fetch(cursor)
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
		cursor = id(rows[len(rows)-1])
		if len(rows) < logExportPageSize {
			return count, nil
		}
	}
}

// logExportSpec 描述一类日志导出的差异点。操作日志与系统日志的鉴权、
// 限流、格式校验、临时文件和附件响应流程完全一致，只有筛选类型和
// 序列化函数不同。
type logExportSpec[T any] struct {
	tempPrefix     string
	filenamePrefix string
	rateRule       string
	rateKey        string
	auditKey       string
	parseFilter    func(*gin.Context) (T, error)
	maxID          func() (int64, error)
	write          func(io.Writer, string, T, int64, *time.Location, i18n.Locale) (int, error)
}

// exportLogFile 执行导出公共流程：鉴权、限流、筛选、临时文件、失败清理、
// 审计日志与附件响应。调用方只提供差异化的筛选和写入函数。
func exportLogFile[T any](s *Server, c *gin.Context, spec logExportSpec[T]) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(spec.rateRule, spec.rateKey+strconv.FormatInt(user.ID, 10)); !allowed {
		rateLimited(c, wait)
		return
	}
	filter, err := spec.parseFilter(c)
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
	maxID, err := spec.maxID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	tmp, err := os.CreateTemp("", spec.tempPrefix+"-*."+format)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	path := tmp.Name()
	writer := bufio.NewWriter(tmp)
	count, err := spec.write(writer, format, filter, maxID, location, locale(c))
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

	s.Store.AddLogKey("settings", 0, "info", spec.auditKey, map[string]any{"count": count})
	filename := spec.filenamePrefix + "-" + time.Now().In(location).Format("20060102-150405") + "." + format
	contentType := "text/csv; charset=utf-8"
	if format == "json" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(path, filename)
}
