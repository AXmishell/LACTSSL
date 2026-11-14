package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry 是提供给可选持久化接收器的日志记录。
// Fields 已移除 component，避免与顶层字段重复。
type Entry struct {
	Time      time.Time
	Level     slog.Level
	Component string
	Message   string
	Fields    map[string]any
}

// Sink 接收日志记录。实现方应快速返回，耗时操作需自行异步化。
type Sink func(Entry)

// maxPendingEntries 在数据库尚未就绪时暂存启动阶段的少量日志。
const maxPendingEntries = 512

var sinkState struct {
	sync.Mutex
	sink    Sink
	pending []Entry
}

// SetSink 注册日志接收器，并补发注册前暂存的启动日志。
// 传入 nil 可停止向接收器分发新日志。
func SetSink(sink Sink) {
	sinkState.Lock()
	sinkState.sink = sink
	pending := sinkState.pending
	sinkState.pending = nil
	sinkState.Unlock()

	if sink == nil || len(pending) == 0 {
		return
	}
	for _, entry := range pending {
		sink(entry)
	}
}

func deliver(entry Entry) {
	sinkState.Lock()
	sink := sinkState.sink
	if sink == nil {
		// 只保留最近一段启动日志，避免长期未启用接收器时无限占用内存。
		if len(sinkState.pending) < maxPendingEntries {
			sinkState.pending = append(sinkState.pending, entry)
		}
		sinkState.Unlock()
		return
	}
	sinkState.Unlock()
	sink(entry)
}

// newCaptureHandler 包装输出 handler，在写文件/stdout 的同时生成 Entry。
func newCaptureHandler(next slog.Handler) slog.Handler {
	return &captureHandler{next: next}
}

type captureHandler struct {
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)

	entry := Entry{
		Time:    record.Time,
		Level:   record.Level,
		Message: record.Message,
		Fields:  make(map[string]any),
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	prefix := strings.Join(h.groups, ".")
	for _, attr := range h.attrs {
		addAttr(entry.Fields, prefix, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(entry.Fields, prefix, attr)
		return true
	})
	if component, ok := entry.Fields["component"].(string); ok {
		entry.Component = component
	}
	if entry.Component == "" {
		entry.Component = "app"
	}
	delete(entry.Fields, "component")
	deliver(entry)
	return err
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := &captureHandler{
		next:   h.next.WithAttrs(attrs),
		attrs:  append(append([]slog.Attr{}, h.attrs...), attrs...),
		groups: append([]string{}, h.groups...),
	}
	return clone
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := &captureHandler{
		next:   h.next.WithGroup(name),
		attrs:  append([]slog.Attr{}, h.attrs...),
		groups: append(append([]string{}, h.groups...), name),
	}
	return clone
}

func addAttr(fields map[string]any, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	key := attr.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			addAttr(fields, key, child)
		}
		return
	}
	if key == "" {
		return
	}
	fields[key] = attrValue(attr.Value)
}

func attrValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindAny:
		raw := value.Any()
		if raw == nil {
			return nil
		}
		if err, ok := raw.(error); ok {
			return err.Error()
		}
		if text, ok := raw.(fmt.Stringer); ok {
			return text.String()
		}
		return fmt.Sprint(raw)
	case slog.KindGroup:
		// 嵌套 group 已在 addAttr 中展开，这里仅作为兜底。
		group := make(map[string]any)
		for _, child := range value.Group() {
			addAttr(group, "", child)
		}
		return group
	default:
		return value.String()
	}
}
