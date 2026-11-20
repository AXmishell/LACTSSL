package logging

// 本文件把进程日志异步写入数据库，供「系统日志」页面查看。
//
// 写入采用有界队列与批量事务：日志记录不会阻塞请求，数据库异常时只丢弃
// 当前批次并输出到 stderr，避免“写日志失败又触发日志”的递归。

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lactssl/internal/model"
)

const (
	dbQueueSize     = 2048
	dbBatchSize     = 64
	dbFlushInterval = time.Second
	dbMaxFields     = 32
	dbMaxFieldsJSON = 32000
)

// DBWriter 是系统日志落库所需的最小接口，*store.Store 满足该接口。
// 使用接口而不是直接依赖 store，避免 logging 包反向依赖数据访问层。
type DBWriter interface {
	AddSystemLogs(entries []model.SystemLogEntry) error
}

// DBSinkOptions 控制哪些进程日志进入数据库。
type DBSinkOptions struct {
	// MinLevel 是最低写入级别。
	MinLevel slog.Level
	// IncludeAccess 表示是否把 HTTP 访问日志也写入数据库。
	// 访问日志频率高，默认只写入 stdout/文件。
	IncludeAccess bool
}

// DBSink 是异步数据库日志接收器。
type DBSink struct {
	store         DBWriter
	minLevel      slog.Level
	includeAccess bool

	queue chan Entry
	done  chan struct{}

	mu      sync.RWMutex
	closed  bool
	dropped atomic.Int64

	closeOnce sync.Once
	// lastError 仅由 run goroutine 读写，避免数据库异常时刷屏。
	lastError time.Time
}

// NewDBSink 创建并启动接收器。调用方应在退出前调用 Close，确保队列中的日志落库。
func NewDBSink(st DBWriter, opts DBSinkOptions) *DBSink {
	sink := &DBSink{
		store:         st,
		minLevel:      opts.MinLevel,
		includeAccess: opts.IncludeAccess,
		queue:         make(chan Entry, dbQueueSize),
		done:          make(chan struct{}),
	}
	go sink.run()
	return sink
}

// Write 实现 Sink 回调。队列满时直接丢弃，绝不阻塞业务请求。
func (s *DBSink) Write(entry Entry) {
	if entry.Level < s.minLevel {
		return
	}
	// 访问日志数量大，默认不进入数据库；门禁入口已在访问日志中脱敏。
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
func (s *DBSink) Dropped() int64 {
	return s.dropped.Load()
}

// Close 停止接收新日志，并等待队列中的日志写入数据库。
func (s *DBSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
	})
	<-s.done
	return nil
}

func (s *DBSink) run() {
	defer close(s.done)
	batch := make([]model.SystemLogEntry, 0, dbBatchSize)
	ticker := time.NewTicker(dbFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case entry, ok := <-s.queue:
			if !ok {
				s.flush(batch)
				return
			}
			batch = append(batch, buildEntry(entry))
			if len(batch) >= dbBatchSize {
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

func (s *DBSink) flush(batch []model.SystemLogEntry) {
	if len(batch) == 0 {
		return
	}
	// 复制一份再交给 Store，避免下一轮复用 batch 时影响正在写入的数据。
	entries := append([]model.SystemLogEntry(nil), batch...)
	if err := s.store.AddSystemLogs(entries); err != nil {
		now := time.Now()
		if s.lastError.IsZero() || now.Sub(s.lastError) >= time.Minute {
			s.lastError = now
			_, _ = fmt.Fprintf(os.Stderr, "logging: 写入系统日志失败: %v\n", err)
		}
	}
}

func buildEntry(entry Entry) model.SystemLogEntry {
	component := strings.TrimSpace(entry.Component)
	if component == "" {
		component = "app"
	}
	fields := sanitizeFields(entry.Fields)
	fieldsJSON := ""
	if len(fields) > 0 {
		if data, err := json.Marshal(fields); err == nil {
			fieldsJSON = string(data)
			if len(fieldsJSON) > dbMaxFieldsJSON {
				fieldsJSON = `{"_truncated":true}`
			}
		}
	}
	createdAt := entry.Time
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	refType, refID := extractLogRef(fields)
	requestID, _ := fields["request_id"].(string)
	return model.SystemLogEntry{
		Level:     strings.ToLower(entry.Level.String()),
		Component: component,
		Message:   entry.Message,
		Fields:    fieldsJSON,
		RefType:   refType,
		RefID:     refID,
		RequestID: requestID,
		CreatedAt: createdAt,
	}
}

// extractLogRef 从结构化字段中提取任务/监控/证书 ID，用于把运行日志关联到对象。
func extractLogRef(fields map[string]any) (string, int64) {
	if len(fields) == 0 {
		return "", 0
	}
	for _, candidate := range []struct {
		key  string
		kind string
	}{
		{"task_id", "task"},
		{"monitor_id", "monitor"},
		{"cert_id", "cert"},
	} {
		value, ok := fields[candidate.key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return candidate.kind, int64(typed)
		case int64:
			return candidate.kind, typed
		case uint64:
			if typed > math.MaxInt64 {
				continue
			}
			return candidate.kind, int64(typed)
		case float64:
			return candidate.kind, int64(typed)
		case json.Number:
			if id, err := typed.Int64(); err == nil {
				return candidate.kind, id
			}
		}
	}
	return "", 0
}

func sanitizeFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	count := 0
	for key, value := range fields {
		if count >= dbMaxFields {
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
