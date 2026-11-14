package acme

import (
	"net/http"
	"time"

	"lactssl/internal/logging"
)

// statusRecorder 记录响应状态码与字节数，供访问日志使用。
// Unwrap 保留 ResponseController 等扩展接口的访问能力。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// logRequests 记录 ACME 协议与 PKI 请求。
// 成功请求使用 debug 级别避免轮询刷屏；4xx/5xx 分别用 warn/error 保留排障信息。
func (s *Server) logRequests(next http.Handler) http.Handler {
	logger := logging.Component("acme")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
			"size", recorder.bytes,
			"ip", s.clientIP(r),
		}
		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("acme request", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("acme request", attrs...)
		default:
			logger.Debug("acme request", attrs...)
		}
	})
}
