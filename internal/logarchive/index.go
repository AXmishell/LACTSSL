package logarchive

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loadIndexLocked 扫描归档根目录下的 manifest.json。
// 只认清单文件，数据文件写了一半但没有清单时不会进入索引。
func (s *Service) loadIndexLocked() error {
	if s.loaded {
		return nil
	}
	entries := make(map[string]*Entry)
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".manifest.json") {
			return nil
		}
		raw, err := os.ReadFile(path) // #nosec G122 -- 路径来自服务端数据目录的 WalkDir，非外部可控路径
		if err != nil {
			return nil
		}
		var manifest Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil
		}
		if strings.TrimSpace(manifest.ArchiveID) == "" || strings.TrimSpace(manifest.File.Name) == "" {
			return nil
		}
		dataPath, ok := safeJoin(s.root, manifest.File.Name)
		if !ok {
			return nil
		}
		missing := false
		if _, err := os.Stat(dataPath); err != nil {
			missing = true
		}
		if _, exists := entries[manifest.ArchiveID]; exists {
			// 同一 ID 出现多份清单时保留较新的清单，避免索引不确定。
			if entries[manifest.ArchiveID].CreatedAt.After(manifest.CreatedAt) {
				return nil
			}
		}
		entries[manifest.ArchiveID] = &Entry{
			Manifest:     manifest,
			ManifestPath: path,
			DataPath:     dataPath,
			Missing:      missing,
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.index = entries
	s.loaded = true
	return nil
}

// removeEntryLocked 删除磁盘文件并从索引移除。数据文件缺失不视为失败。
// audit 为 true 时写一条操作日志；配额回滚删除刚写入的空归档时传 false。
func (s *Service) removeEntryLocked(entry *Entry, audit bool) error {
	if entry == nil {
		return nil
	}
	if entry.DataPath != "" {
		if err := os.Remove(entry.DataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if entry.ManifestPath != "" {
		if err := os.Remove(entry.ManifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	delete(s.index, entry.ArchiveID)
	if audit {
		s.store.AddLogKey("settings", 0, "warning", "log.settings.archive_deleted", map[string]any{
			"category": entry.Category,
			"count":    entry.RowCount,
			"file":     entry.File.Name,
		})
	}
	return nil
}

// safeJoin 把清单中的相对路径限制在归档根目录内，防止路径穿越。
func safeJoin(root, relative string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) {
		return "", false
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	joined := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
}
