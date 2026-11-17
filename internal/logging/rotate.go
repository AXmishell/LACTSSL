package logging

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const backupTimeLayout = "20060102-150405.000000000"

type rotateOptions struct {
	MaxSize    int64
	MaxBackups int
	MaxAge     time.Duration
	Compress   bool
}

// rotatingWriter 是带大小轮转的日志文件写入器。
// 只依赖标准库，避免为简单的文件轮转引入额外依赖。
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int
	maxAge     time.Duration
	compress   bool
	now        func() time.Time
	file       *os.File
	size       int64
	closed     bool
}

func newRotatingWriter(path string, opt rotateOptions) (*rotatingWriter, error) {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件 %s 失败: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("读取日志文件状态失败: %w", err)
	}
	return &rotatingWriter{
		path:       path,
		maxSize:    opt.MaxSize,
		maxBackups: opt.MaxBackups,
		maxAge:     opt.MaxAge,
		compress:   opt.Compress,
		now:        time.Now,
		file:       file,
		size:       info.Size(),
	}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file == nil {
		if err := w.openLocked(); err != nil {
			return 0, err
		}
	}
	if w.maxSize > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			// 轮转失败不能让日志彻底丢失：继续写入原文件，并把错误写到 stderr。
			_, _ = fmt.Fprintf(os.Stderr, "logging: 轮转日志失败: %v\n", err)
		}
	}
	if w.file == nil {
		if err := w.openLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) openLocked() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s 失败: %w", w.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("读取日志文件状态失败: %w", err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

// rotateLocked 关闭当前文件，按时间戳归档，重新打开同名文件。
func (w *rotatingWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.file = nil
			return err
		}
		w.file = nil
	}
	backup := w.path + "." + w.now().Format(backupTimeLayout)
	if err := os.Rename(w.path, backup); err != nil {
		// 文件被外部移动或删除时，直接重新创建即可。
		if !os.IsNotExist(err) {
			_ = w.openLocked()
			return fmt.Errorf("归档日志文件失败: %w", err)
		}
	} else if w.compress {
		w.compressLocked(backup)
	}
	w.cleanupLocked()
	return w.openLocked()
}

// compressLocked 把归档文件压缩为 .gz；失败时保留原始归档，避免丢日志。
func (w *rotatingWriter) compressLocked(path string) {
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()

	dstPath := path + ".gz"
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err != nil {
		_ = gz.Close()
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return
	}
	if err := gz.Close(); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dstPath)
		return
	}
	_ = os.Remove(path)
}

// cleanupLocked 按数量和天数清理历史归档。0 表示不限制。
func (w *rotatingWriter) cleanupLocked() {
	if w.maxBackups <= 0 && w.maxAge <= 0 {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(w.path))
	if err != nil {
		return
	}
	prefix := filepath.Base(w.path) + "."
	type backupFile struct {
		path    string
		modTime time.Time
	}
	backups := make([]backupFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if _, ok := backupTime(name[len(prefix):]); !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupFile{
			path:    filepath.Join(filepath.Dir(w.path), name),
			modTime: info.ModTime(),
		})
	}
	// 新文件在前，超过保留数量的从旧到新删除。
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].path > backups[j].path
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	now := w.now()
	for index, item := range backups {
		remove := false
		if w.maxBackups > 0 && index >= w.maxBackups {
			remove = true
		}
		if w.maxAge > 0 && now.Sub(item.modTime) > w.maxAge {
			remove = true
		}
		if remove {
			_ = os.Remove(item.path)
		}
	}
}

// backupTime 解析归档文件名中的时间戳；兼容 .gz 后缀。
func backupTime(value string) (time.Time, bool) {
	value = strings.TrimSuffix(value, ".gz")
	parsed, err := time.ParseInLocation(backupTimeLayout, value, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
