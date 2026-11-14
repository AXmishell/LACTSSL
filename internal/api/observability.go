package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

const requestIDKey = "request_id"

// requestID 为每个请求生成可追踪的 ID，并回写到响应头。
// 攻击者可以伪造该头，因此只接受长度受限的常见字符，避免日志注入。
func (s *Server) requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitizeRequestID(c.GetHeader("X-Request-ID"))
		if id == "" {
			id = newRequestID()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// accessLog 记录 HTTP 访问日志。
// 只记录路由模板或脱敏后的路径，不记录查询参数、请求体和认证头，
// 避免安全入口、令牌等敏感值进入进程日志。
func (s *Server) accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.AccessLog {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusTooManyRequests:
			level = slog.LevelWarn
		}
		attrs := []any{
			"request_id", c.GetString(requestIDKey),
			"method", c.Request.Method,
			"path", s.logPath(c),
			"status", status,
			"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
			"size", max(c.Writer.Size(), 0),
			"ip", c.ClientIP(),
		}
		if value, exists := c.Get("user"); exists {
			if user, ok := value.(*model.User); ok && user != nil {
				attrs = append(attrs, "user_id", user.ID)
			}
		}
		s.logger().Log(c.Request.Context(), level, "http request", attrs...)
	}
}

// recovery 替代 gin.Recovery，把 panic 与堆栈写入结构化日志，
// 同时保持统一的 JSON 错误响应格式。
func (s *Server) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				c.Abort()
				return
			}
			s.logger().Error("http panic recovered",
				"request_id", c.GetString(requestIDKey),
				"method", c.Request.Method,
				"path", s.logPath(c),
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)
			if !c.Writer.Written() {
				fail(c, http.StatusInternalServerError, i18n.T(locale(c), "api.error.internal", nil))
				return
			}
			c.Abort()
		}()
		c.Next()
	}
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// logPath 优先使用 Gin 路由模板（其中安全入口是 :entry 占位符）。
// 未匹配路由时，若首段等于当前安全入口则替换为 :entry。
func (s *Server) logPath(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	path := c.Request.URL.Path
	entry, _ := s.entry.Load().(string)
	if entry == "" {
		return path
	}
	prefix := "/" + entry
	if path == prefix {
		return "/:entry"
	}
	if strings.HasPrefix(path, prefix+"/") {
		return "/:entry" + strings.TrimPrefix(path, prefix)
	}
	return path
}

func sanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return ""
		}
	}
	return value
}

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
