package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"lactssl/internal/model"
)

// ListPluginFileMeta 返回指定类型下全部 helper 文件的 manifest 缓存。
func (s *Store) ListPluginFileMeta(kind string) ([]model.PluginFileMeta, error) {
	rows, err := s.DB.Query(`SELECT kind,filename,name,version,entries,sha256,updated_at FROM plugin_file_meta WHERE kind=? ORDER BY filename`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.PluginFileMeta, 0)
	for rows.Next() {
		item, err := scanPluginFileMeta(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetPluginFileMeta 查询单个 helper 文件的 manifest 缓存。
func (s *Store) GetPluginFileMeta(kind, filename string) (model.PluginFileMeta, bool, error) {
	row := s.DB.QueryRow(`SELECT kind,filename,name,version,entries,sha256,updated_at FROM plugin_file_meta WHERE kind=? AND filename=?`, kind, filename)
	item, err := scanPluginFileMeta(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PluginFileMeta{}, false, nil
	}
	if err != nil {
		return model.PluginFileMeta{}, false, err
	}
	return item, true, nil
}

// UpsertPluginFileMeta 写入或更新 helper 文件的 manifest 缓存。
func (s *Store) UpsertPluginFileMeta(item model.PluginFileMeta) error {
	entries, err := json.Marshal(item.Entries)
	if err != nil {
		return err
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = nowUTC()
	}
	_, err = s.DB.Exec(`INSERT INTO plugin_file_meta(kind,filename,name,version,entries,sha256,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(kind,filename) DO UPDATE SET name=excluded.name,version=excluded.version,
		entries=excluded.entries,sha256=excluded.sha256,updated_at=excluded.updated_at`,
		item.Kind, item.Filename, item.Name, item.Version, string(entries), item.SHA256, item.UpdatedAt)
	return err
}

// DeletePluginFileMeta 删除 helper 文件的 manifest 缓存。
func (s *Store) DeletePluginFileMeta(kind, filename string) error {
	_, err := s.DB.Exec(`DELETE FROM plugin_file_meta WHERE kind=? AND filename=?`, kind, filename)
	return err
}

// DeleteKindPluginFileMeta 删除某类插件的全部 manifest 缓存，仅用于清理场景。
func (s *Store) DeleteKindPluginFileMeta(kind string) error {
	_, err := s.DB.Exec(`DELETE FROM plugin_file_meta WHERE kind=?`, kind)
	return err
}

func scanPluginFileMeta(scan func(...any) error) (model.PluginFileMeta, error) {
	var (
		item    model.PluginFileMeta
		entries string
	)
	if err := scan(&item.Kind, &item.Filename, &item.Name, &item.Version, &entries, &item.SHA256, &item.UpdatedAt); err != nil {
		return model.PluginFileMeta{}, err
	}
	item.Entries = []model.PluginFileEntry{}
	if entries != "" {
		if err := json.Unmarshal([]byte(entries), &item.Entries); err != nil {
			// 缓存损坏时按空清单处理，Discover 成功后会重新写入。
			item.Entries = []model.PluginFileEntry{}
		}
	}
	return item, nil
}
