// Package logging 提供进程级结构化日志。
//
// 业务日志（logs 表）记录“谁在什么时间做了什么”，用于界面展示与审计；
// 本包记录服务运行时的底层信息（启动、HTTP、调度、插件发现、ACME 等），
// 面向排障与运维。两者用途不同，不应互相替代。
package logging

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"
)

// 日志级别名称，与 slog 的内置级别对应。
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// 输出格式。
const (
	FormatText = "text"
	FormatJSON = "json"
)

// Config 是进程日志的运行配置。
type Config struct {
	// Level 是最低输出级别：debug、info、warn、error。
	Level string
	// Format 是输出格式：text 或 json。
	Format string
	// File 是日志文件路径；为空时只输出到 stdout。
	File string
	// AlsoStdout 表示写入文件时是否同时输出到 stdout。
	AlsoStdout bool
	// MaxSizeMB 是单个日志文件的大小上限，超过后轮转。
	MaxSizeMB int
	// MaxBackups 是保留的历史文件数量，0 表示不限制。
	MaxBackups int
	// MaxAgeDays 是历史文件保留天数，0 表示不限制。
	MaxAgeDays int
	// Compress 表示是否压缩历史文件。
	Compress bool
}

// DefaultConfig 返回与示例配置一致的默认值。
func DefaultConfig() Config {
	return Config{
		Level:      LevelInfo,
		Format:     FormatText,
		AlsoStdout: true,
		MaxSizeMB:  20,
		MaxBackups: 5,
		MaxAgeDays: 30,
		Compress:   true,
	}
}

// Normalize 补齐空值并统一大小写，避免零值配置被误用。
func (c Config) Normalize() Config {
	def := DefaultConfig()
	c.Level = strings.ToLower(strings.TrimSpace(c.Level))
	c.Format = strings.ToLower(strings.TrimSpace(c.Format))
	if c.Level == "" {
		c.Level = def.Level
	}
	if c.Level == "warning" {
		c.Level = LevelWarn
	}
	if c.Format == "" {
		c.Format = def.Format
	}
	c.File = strings.TrimSpace(c.File)
	if c.MaxSizeMB == 0 {
		c.MaxSizeMB = def.MaxSizeMB
	}
	// MaxBackups 与 MaxAgeDays 的 0 表示不限制，不能在这里补默认值。
	return c
}

// Validate 校验配置范围。启动阶段会直接拒绝非法值，避免静默降级。
func (c Config) Validate() error {
	switch c.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	default:
		return fmt.Errorf("log.level 必须是 debug、info、warn 或 error")
	}
	switch c.Format {
	case FormatText, FormatJSON:
	default:
		return fmt.Errorf("log.format 必须是 text 或 json")
	}
	if c.MaxSizeMB < 1 || c.MaxSizeMB > 1024 {
		return fmt.Errorf("log.max_size_mb 必须在 1-1024 之间")
	}
	if c.MaxBackups < 0 || c.MaxBackups > 100 {
		return fmt.Errorf("log.max_backups 必须在 0-100 之间")
	}
	if c.MaxAgeDays < 0 || c.MaxAgeDays > 3650 {
		return fmt.Errorf("log.max_age_days 必须在 0-3650 之间")
	}
	return nil
}

// Init 初始化全局 logger，并把标准库 log 桥接到同一输出。
// 返回的函数用于关闭文件并释放资源；调用方应在进程退出前调用。
func Init(cfg Config) (func() error, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	writers := make([]io.Writer, 0, 2)
	closeFn := func() error { return nil }
	if cfg.File != "" {
		writer, err := newRotatingWriter(cfg.File, rotateOptions{
			MaxSize:    int64(cfg.MaxSizeMB) << 20,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     timeDays(cfg.MaxAgeDays),
			Compress:   cfg.Compress,
		})
		if err != nil {
			return nil, err
		}
		writers = append(writers, writer)
		closeFn = writer.Close
	}
	if cfg.File == "" || cfg.AlsoStdout {
		writers = append(writers, os.Stdout)
	}
	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}

	handler := newCaptureHandler(newHandler(teeWriter(writers), cfg))
	logger := slog.New(handler)
	slog.SetDefault(logger)
	bridgeStandardLog(logger)
	return closeFn, nil
}

// Component 返回带 component 字段的 logger，便于按模块筛选。
func Component(name string) *slog.Logger {
	return slog.Default().With("component", name)
}

// Level 返回名称对应的 slog.Level；未知值回退到 info。
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn, "warning":
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newHandler(w io.Writer, cfg Config) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:     Level(cfg.Level),
		AddSource: cfg.Level == LevelDebug,
	}
	switch cfg.Format {
	case FormatJSON:
		return slog.NewJSONHandler(w, opts)
	default:
		return slog.NewTextHandler(w, opts)
	}
}

// teeWriter 尝试写入所有目标，即使其中一个失败也不阻塞其他输出。
// io.MultiWriter 在第一个错误处停止，会让文件写失败同时吞掉 stdout 日志。
type teeWriter []io.Writer

func (t teeWriter) Write(p []byte) (int, error) {
	var firstErr error
	written := 0
	for _, w := range t {
		n, err := w.Write(p)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil && n > written {
			written = n
		}
	}
	return written, firstErr
}

// bridgeStandardLog 让既有 log.Printf / log.Fatalf 调用继续可用，
// 同时统一进入结构化日志的格式与输出目标。
func bridgeStandardLog(logger *slog.Logger) {
	bridge := slog.NewLogLogger(logger.Handler(), slog.LevelInfo)
	bridge.SetFlags(0)
	log.SetOutput(bridge.Writer())
	log.SetFlags(0)
	log.SetPrefix("")
}

func timeDays(days int) time.Duration {
	if days <= 0 {
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}
