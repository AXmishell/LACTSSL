// Package logarchive 负责在日志被删除前写入归档文件。
//
// 归档由一个 NDJSON 数据文件（可选 gzip）和一个 manifest.json 组成：
//   - 数据文件逐行保存 logs 表的原始字段，message/fields 不做本地化或解析；
//   - manifest 记录行数、ID 范围、时间范围、触发原因、操作者与 SHA-256。
//
// 归档目录位于数据目录内，默认 <data_dir>/archives/logs。
// 所有写入采用同目录临时文件 + fsync + 原子重命名，避免留下半截文件。
package logarchive

import (
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/store"
)

const (
	// ReasonManualClear 表示用户主动清空日志。
	ReasonManualClear = "manual_clear"
	// ReasonRetention 表示按保留天数自动清理。
	ReasonRetention = "retention"
	// ReasonMaxRows 表示按最大行数自动清理。
	ReasonMaxRows = "max_rows"

	// QuotaFail 表示归档容量超限时拒绝继续归档并阻止删除。
	QuotaFail = "fail"
	// QuotaPruneOldest 表示容量超限时删除最旧归档。
	QuotaPruneOldest = "prune_oldest"

	archiveBatchSize = 1000
	schemaVersion    = 1
)

var (
	// ErrQuotaExceeded 表示归档目录达到容量上限。
	ErrQuotaExceeded = errors.New("日志归档容量超过上限")
	// ErrArchiveNotFound 表示归档 ID 不存在。
	ErrArchiveNotFound = errors.New("日志归档不存在")
)

// Options 是归档服务的运行参数。
type Options struct {
	RootDir       string
	Compress      bool
	RetentionDays int
	MaxTotalMB    int
	QuotaPolicy   string
	AppVersion    string
	// Now 仅用于测试注入固定时间。
	Now func() time.Time
}

// Service 串行执行归档、删除和归档保留清理。
type Service struct {
	store         *store.Store
	root          string
	compress      bool
	retentionDays int
	maxTotalBytes int64
	quotaPolicy   string
	appVersion    string
	now           func() time.Time

	mu     sync.Mutex
	index  map[string]*Entry
	loaded bool
}

// New 创建归档服务。目录在真正写入时才创建，避免只读环境启动失败。
func New(st *store.Store, opts Options) *Service {
	root := strings.TrimSpace(opts.RootDir)
	if root == "" {
		root = filepath.Join("data", "archives", "logs")
	}
	quotaPolicy := strings.TrimSpace(opts.QuotaPolicy)
	if quotaPolicy == "" {
		quotaPolicy = QuotaFail
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:         st,
		root:          filepath.Clean(root),
		compress:      opts.Compress,
		retentionDays: opts.RetentionDays,
		maxTotalBytes: int64(opts.MaxTotalMB) << 20,
		quotaPolicy:   quotaPolicy,
		appVersion:    opts.AppVersion,
		now:           now,
		index:         make(map[string]*Entry),
	}
}

// Root 返回归档根目录，供启动日志与诊断使用。
func (s *Service) Root() string { return s.root }

// ClearOperationLogs 归档并清空操作日志。
// 返回归档结果（没有可归档记录时为 nil）与实际删除行数。
func (s *Service) ClearOperationLogs(reason string, operatorID int64, operatorEmail string) (*store.LogArchiveResult, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	category := model.LogCategoryOperation
	maxID, err := s.store.MaxLogIDByCategory(category)
	if err != nil {
		return nil, 0, err
	}
	if maxID <= 0 {
		return nil, 0, nil
	}
	result, err := s.writeArchiveLocked(category, reason, operatorID, operatorEmail, maxID+1, nil)
	if err != nil {
		return nil, 0, err
	}
	if result == nil {
		return nil, 0, nil
	}
	deleted, err := s.store.DeleteLogsForArchive(category, maxID+1, nil)
	if err != nil {
		return result, 0, err
	}
	return result, deleted, nil
}

// ClearSystemLogs 归档并清空系统日志。
// 即使没有可归档记录，也会更新 systemLogClearedAt，保持原有清空语义。
func (s *Service) ClearSystemLogs(reason string, operatorID int64, operatorEmail string) (*store.LogArchiveResult, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	category := model.LogCategoryRuntime
	maxID, err := s.store.MaxLogIDByCategory(category)
	if err != nil {
		return nil, 0, err
	}
	var result *store.LogArchiveResult
	if maxID > 0 {
		result, err = s.writeArchiveLocked(category, reason, operatorID, operatorEmail, maxID+1, nil)
		if err != nil {
			return nil, 0, err
		}
	}
	deleted, err := s.store.ClearSystemLogsThrough(maxID)
	if err != nil {
		return result, 0, err
	}
	return result, deleted, nil
}

// PruneOperationLogs 归档并删除超过保留期的操作日志。
func (s *Service) PruneOperationLogs(cutoff time.Time) (*store.LogArchiveResult, error) {
	if cutoff.IsZero() {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	category := model.LogCategoryOperation
	maxID, err := s.store.MaxLogIDByCategory(category)
	if err != nil {
		return nil, err
	}
	if maxID <= 0 {
		return nil, nil
	}
	result, err := s.writeArchiveLocked(category, ReasonRetention, 0, "", maxID+1, &cutoff)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if _, err := s.store.DeleteLogsForArchive(category, maxID+1, &cutoff); err != nil {
		return result, err
	}
	return result, nil
}

// PruneSystemLogs 依次按保留天数与最大行数归档并删除系统日志。
// 任一步归档失败都会中止后续删除，等待下一轮维护重试。
func (s *Service) PruneSystemLogs(cutoff time.Time, maxRows int) ([]*store.LogArchiveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	category := model.LogCategoryRuntime
	results := make([]*store.LogArchiveResult, 0, 2)

	if !cutoff.IsZero() {
		maxID, err := s.store.MaxLogIDByCategory(category)
		if err != nil {
			return results, err
		}
		if maxID > 0 {
			result, err := s.writeArchiveLocked(category, ReasonRetention, 0, "", maxID+1, &cutoff)
			if err != nil {
				return results, err
			}
			if result != nil {
				if _, err := s.store.DeleteLogsForArchive(category, maxID+1, &cutoff); err != nil {
					return results, err
				}
				results = append(results, result)
			}
		}
	}

	if maxRows > 0 {
		boundary, err := s.store.LogBoundaryID(category, maxRows)
		if err != nil {
			return results, err
		}
		if boundary > 0 {
			result, err := s.writeArchiveLocked(category, ReasonMaxRows, 0, "", boundary, nil)
			if err != nil {
				return results, err
			}
			if result != nil {
				if _, err := s.store.DeleteLogsForArchive(category, boundary, nil); err != nil {
					return results, err
				}
				results = append(results, result)
			}
		}
	}
	return results, nil
}

// PruneArchives 按归档保留期删除旧归档；retentionDays=0 时不做任何事。
func (s *Service) PruneArchives(now time.Time) error {
	if s.retentionDays <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadIndexLocked(); err != nil {
		return err
	}
	cutoff := now.UTC().AddDate(0, 0, -s.retentionDays)
	var firstErr error
	for _, entry := range s.sortedEntriesLocked(true) {
		if !entry.CreatedAt.Before(cutoff) {
			continue
		}
		if err := s.removeEntryLocked(entry, true); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// List 返回全部归档元数据，按创建时间倒序。
func (s *Service) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadIndexLocked(); err != nil {
		return nil, err
	}
	entries := s.sortedEntriesLocked(false)
	list := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		list = append(list, *entry)
	}
	return list, nil
}

// Get 按归档 ID 查询元数据。
func (s *Service) Get(id string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadIndexLocked(); err != nil {
		return Entry{}, false, err
	}
	entry, ok := s.index[id]
	if !ok {
		return Entry{}, false, nil
	}
	return *entry, true, nil
}

// Delete 删除一个归档的数据文件与清单。调用方负责鉴权与审计。
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadIndexLocked(); err != nil {
		return err
	}
	entry, ok := s.index[id]
	if !ok {
		return ErrArchiveNotFound
	}
	return s.removeEntryLocked(entry, true)
}

// writeArchiveLocked 把快照写入归档文件。调用方必须持有 s.mu。
// beforeID 是排他上界，归档与删除使用同一上界。
func (s *Service) writeArchiveLocked(category, reason string, operatorID int64, operatorEmail string, beforeID int64, cutoff *time.Time) (*store.LogArchiveResult, error) {
	if beforeID <= 0 {
		return nil, nil
	}
	if err := s.loadIndexLocked(); err != nil {
		return nil, err
	}
	first, err := s.store.LogsForArchive(category, beforeID, cutoff, 0, archiveBatchSize)
	if err != nil {
		return nil, err
	}
	if len(first) == 0 {
		return nil, nil
	}

	createdAt := s.now().UTC()
	archiveID := newArchiveID()
	dir := filepath.Join(s.root, category, createdAt.Format("2006"), createdAt.Format("01"))
	if err := ensureDir(dir); err != nil {
		return nil, err
	}

	base := fmt.Sprintf("%s-%s-%s-%s", category, createdAt.Format("20060102T150405Z"), sanitizeReason(reason), archiveID)
	ext := ".ndjson"
	fileFormat := "ndjson"
	if s.compress {
		ext = ".ndjson.gz"
		fileFormat = "ndjson.gz"
	}
	dataPath := filepath.Join(dir, base+ext)
	manifestPath := filepath.Join(dir, base+".manifest.json")

	tmp, err := os.CreateTemp(dir, "."+base+"-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	counter := &countWriter{}
	hasher := sha256.New()
	var target io.Writer = io.MultiWriter(tmp, hasher, counter)
	var gz *gzip.Writer
	if s.compress {
		gz = gzip.NewWriter(target)
		target = gz
	}
	buf := bufio.NewWriterSize(target, 64*1024)

	var rows int64
	var firstID, lastID int64
	var firstCreated, lastCreated time.Time
	afterID := int64(0)
	batch := first
	for {
		if len(batch) == 0 {
			break
		}
		for _, entry := range batch {
			raw, err := json.Marshal(archiveRow{
				ID:        entry.ID,
				Category:  entry.Category,
				Source:    entry.Source,
				RefType:   entry.RefType,
				RefID:     entry.RefID,
				Level:     entry.Level,
				Message:   entry.Message,
				Fields:    entry.Fields,
				RequestID: entry.RequestID,
				CreatedAt: entry.CreatedAt.UTC(),
			})
			if err != nil {
				_ = buf.Flush()
				_ = gzClose(gz)
				_ = tmp.Close()
				return nil, err
			}
			if _, err := buf.Write(raw); err != nil {
				_ = buf.Flush()
				_ = gzClose(gz)
				_ = tmp.Close()
				return nil, err
			}
			if err := buf.WriteByte('\n'); err != nil {
				_ = buf.Flush()
				_ = gzClose(gz)
				_ = tmp.Close()
				return nil, err
			}
			if rows == 0 {
				firstID = entry.ID
				firstCreated = entry.CreatedAt.UTC()
			}
			lastID = entry.ID
			lastCreated = entry.CreatedAt.UTC()
			rows++
			afterID = entry.ID
		}
		if len(batch) < archiveBatchSize {
			break
		}
		batch, err = s.store.LogsForArchive(category, beforeID, cutoff, afterID, archiveBatchSize)
		if err != nil {
			_ = buf.Flush()
			_ = gzClose(gz)
			_ = tmp.Close()
			return nil, err
		}
	}

	if err := buf.Flush(); err != nil {
		_ = gzClose(gz)
		_ = tmp.Close()
		return nil, err
	}
	if err := gzClose(gz); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, nil
	}
	if err := os.Rename(tmpPath, dataPath); err != nil {
		return nil, err
	}
	committed = true
	syncDir(dir)

	relData, err := filepath.Rel(s.root, dataPath)
	if err != nil {
		_ = os.Remove(dataPath)
		return nil, err
	}
	relData = filepath.ToSlash(relData)

	manifest := Manifest{
		Schema:     schemaVersion,
		ArchiveID:  archiveID,
		Category:   category,
		Reason:     reason,
		CreatedAt:  createdAt,
		AppVersion: s.appVersion,
		Operator: Operator{
			UserID: operatorID,
			Email:  strings.TrimSpace(operatorEmail),
		},
		RowCount:       rows,
		FirstLogID:     firstID,
		LastLogID:      lastID,
		FirstCreatedAt: &firstCreated,
		LastCreatedAt:  &lastCreated,
		Filters: map[string]any{
			"max_id": beforeID - 1,
		},
		File: FileInfo{
			Name:   relData,
			Format: fileFormat,
			Bytes:  counter.n,
			SHA256: hex.EncodeToString(hasher.Sum(nil)),
		},
	}
	if cutoff != nil && !cutoff.IsZero() {
		manifest.Filters["created_before"] = cutoff.UTC().Format(time.RFC3339)
	}
	if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		_ = os.Remove(dataPath)
		return nil, err
	}
	syncDir(dir)

	entry := &Entry{
		Manifest:     manifest,
		ManifestPath: manifestPath,
		DataPath:     dataPath,
	}
	s.index[archiveID] = entry
	if err := s.enforceQuotaLocked(archiveID); err != nil {
		_ = s.removeEntryLocked(entry, false)
		return nil, err
	}

	return &store.LogArchiveResult{
		ID:        archiveID,
		Category:  category,
		Reason:    reason,
		File:      relData,
		Rows:      rows,
		Bytes:     counter.n,
		SHA256:    manifest.File.SHA256,
		CreatedAt: createdAt.Format(time.RFC3339),
	}, nil
}

// enforceQuotaLocked 在归档成功落盘后检查容量。
// 新归档本身超过上限时不再删除旧归档，避免“旧数据已删、新归档又失败”。
func (s *Service) enforceQuotaLocked(newID string) error {
	if s.maxTotalBytes <= 0 {
		return nil
	}
	newEntry := s.index[newID]
	if newEntry == nil {
		return nil
	}
	if newEntry.File.Bytes > s.maxTotalBytes {
		return ErrQuotaExceeded
	}
	total := int64(0)
	for _, entry := range s.index {
		total += entry.File.Bytes
	}
	if total <= s.maxTotalBytes {
		return nil
	}
	if s.quotaPolicy != QuotaPruneOldest {
		return ErrQuotaExceeded
	}
	for _, entry := range s.sortedEntriesLocked(true) {
		if total <= s.maxTotalBytes {
			break
		}
		if entry.ArchiveID == newID {
			continue
		}
		if err := s.removeEntryLocked(entry, true); err != nil {
			return err
		}
		total -= entry.File.Bytes
	}
	if total > s.maxTotalBytes {
		return ErrQuotaExceeded
	}
	return nil
}

// Load 触发一次归档目录扫描，供启动阶段建立索引。
func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadIndexLocked()
}

type archiveRow struct {
	ID        int64     `json:"id"`
	Category  string    `json:"category"`
	Source    string    `json:"source"`
	RefType   string    `json:"ref_type,omitempty"`
	RefID     int64     `json:"ref_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Fields    string    `json:"fields,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func gzClose(w *gzip.Writer) error {
	if w == nil {
		return nil
	}
	return w.Close()
}

func newArchiveID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(raw)
}

func sanitizeReason(reason string) string {
	switch reason {
	case ReasonManualClear, ReasonRetention, ReasonMaxRows:
		return reason
	}
	var builder strings.Builder
	for _, char := range reason {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '_' || char == '-':
			builder.WriteRune(char)
		}
		if builder.Len() >= 24 {
			break
		}
	}
	if builder.Len() == 0 {
		return "archive"
	}
	return builder.String()
}

func ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o700)
	return nil
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	syncDir(dir)
	return nil
}

// syncDir 尽量同步目录项；Windows 等平台不支持时忽略错误。
func syncDir(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}

func (s *Service) sortedEntriesLocked(ascending bool) []*Entry {
	list := make([]*Entry, 0, len(s.index))
	for _, entry := range s.index {
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool {
		if ascending {
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list
}
