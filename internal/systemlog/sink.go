// Package systemlog 把进程日志异步写入数据库，供「系统日志」页面查看。
//
// 写入采用有界队列与批量事务：日志记录不会阻塞请求，数据库异常时只丢弃
// 当前批次并输出到 stderr，避免“写日志失败又触发日志”的递归。
package systemlog

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lactssl/internal/logging"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

const (
	queueSize     = 2048
	batchSize     = 64
	flushInterval = time.Second
	maxFields     = 32
	maxFieldsJSON = 32000
)

// Options 控制哪些进程日志进入数据库。
type Options struct {
	// MinLevel 是最低写入级别。
	MinLevel slog.Level
	// IncludeAccess 表示是否把 HTTP 访问日志也写入数据库。
	// 访问日志频率高，默认只写入 stdout/文件。
	IncludeAccess bool
}

// Sink 是异步数据库日志接收器。
type Sink struct {
	store         *store.Store
	minLevel      slog.Level
	includeAccess bool

	queue chan logging.Entry
	done  chan struct{}

	mu      sync.RWMutex
	closed  bool
	dropped atomic.Int64

	closeOnce sync.Once
	// lastError 仅由 run goroutine 读写，避免数据库异常时刷屏。
	lastError time.Time
}

// New 创建并启动接收器。调用方应在退出前调用 Close，确保队列中的日志落库。
func New(st *store.Store, opts Options) *Sink {
	sink := &Sink{
		store:         st,
		minLevel:      opts.MinLevel,
		includeAccess: opts.IncludeAccess,
		queue:         make(chan logging.Entry, queueSize),
		done:          make(chan struct{}),
	}
	go sink.run()
	return sink
}

// Write 实现 logging.Sink。队列满时直接丢弃，绝不阻塞业务请求。
func (s *Sink) Write(entry logging.Entry) {
	if entry.Level < s.minLevel {
		return
	}
	// 访问日志数量大，默认不进入数据库；安全入口已在访问日志中脱敏。
	if !s.includeAccess && entry.Component == "http" && entry.Message == "http request" {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- entry:
	default:
		s.dropped.Add(1)
	}
}

// Dropped 返回因队列满而丢弃的日志条数，用于诊断。
func (s *Sink) Dropped() int64 {
	return s.dropped.Load()
}

// Close 停止接收新日志，并等待队列中的日志写入数据库。
func (s *Sink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
	})
	<-s.done
	return nil
}

func (s *Sink) run() {
	defer close(s.done)
	batch := make([]model.SystemLogEntry, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case entry, ok := <-s.queue:
			if !ok {
				s.flush(batch)
				return
			}
			batch = append(batch, buildEntry(entry))
			if len(batch) >= batchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				s.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

func (s *Sink) flush(batch []model.SystemLogEntry) {
	if len(batch) == 0 {
		return
	}
	// 复制一份再交给 Store，避免下一轮复用 batch 时影响正在写入的数据。
	entries := append([]model.SystemLogEntry(nil), batch...)
	if err := s.store.AddSystemLogs(entries); err != nil {
		now := time.Now()
		if s.lastError.IsZero() || now.Sub(s.lastError) >= time.Minute {
			s.lastError = now
			_, _ = fmt.Fprintf(os.Stderr, "systemlog: 写入系统日志失败: %v\n", err)
		}
	}
}

func buildEntry(entry logging.Entry) model.SystemLogEntry {
	component := strings.TrimSpace(entry.Component)
	if component == "" {
		component = "app"
	}
	fields := sanitizeFields(entry.Fields)
	fieldsJSON := ""
	if len(fields) > 0 {
		if data, err := json.Marshal(fields); err == nil {
			fieldsJSON = string(data)
			if len(fieldsJSON) > maxFieldsJSON {
				fieldsJSON = `{"_truncated":true}`
			}
		}
	}
	createdAt := entry.Time
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return model.SystemLogEntry{
		Level:     strings.ToLower(entry.Level.String()),
		Component: component,
		Message:   entry.Message,
		Fields:    fieldsJSON,
		CreatedAt: createdAt,
	}
}

func sanitizeFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	count := 0
	for key, value := range fields {
		if count >= maxFields {
			break
		}
		out[truncateRunes(key, 64)] = sanitizeValue(key, value)
		count++
	}
	return out
}

func sanitizeValue(key string, value any) any {
	if text, ok := value.(string); ok {
		limit := 2000
		// panic 堆栈较长，单独放宽上限；仍远小于数据库和页面可承受范围。
		if key == "stack" {
			limit = 8000
		}
		return truncateRunes(text, limit)
	}
	switch value.(type) {
	case nil, bool, int, int64, uint64, float64, json.Number:
		return value
	}
	return truncateRunes(fmt.Sprint(value), 2000)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
