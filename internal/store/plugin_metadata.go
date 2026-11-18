package store

import (
	"database/sql"
	"errors"

	"lactssl/internal/model"
)

// ListPluginMetadata 返回指定类型下全部条目的 manifest 缓存。
func (s *Store) ListPluginMetadata(kind string) ([]model.PluginMetadata, error) {
	rows, err := s.DB.Query(`SELECT kind,id,version,name,manifest,updated_at FROM plugin_metadata WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.PluginMetadata, 0)
	for rows.Next() {
		var item model.PluginMetadata
		if err := rows.Scan(&item.Kind, &item.ID, &item.Version, &item.Name, &item.Manifest, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetPluginMetadata 查询单个条目的 manifest 缓存。
func (s *Store) GetPluginMetadata(kind, id string) (model.PluginMetadata, bool, error) {
	row := s.DB.QueryRow(`SELECT kind,id,version,name,manifest,updated_at FROM plugin_metadata WHERE kind=? AND id=?`, kind, id)
	var item model.PluginMetadata
	err := row.Scan(&item.Kind, &item.ID, &item.Version, &item.Name, &item.Manifest, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PluginMetadata{}, false, nil
	}
	if err != nil {
		return model.PluginMetadata{}, false, err
	}
	return item, true, nil
}

// UpsertPluginMetadata 写入或更新单个条目的 manifest 缓存。
func (s *Store) UpsertPluginMetadata(item model.PluginMetadata) error {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = nowUTC()
	}
	_, err := s.DB.Exec(`INSERT INTO plugin_metadata(kind,id,version,name,manifest,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET version=excluded.version,name=excluded.name,
		manifest=excluded.manifest,updated_at=excluded.updated_at`,
		item.Kind, item.ID, item.Version, item.Name, item.Manifest, item.UpdatedAt)
	return err
}

// DeletePluginMetadata 删除单个条目的 manifest 缓存。
func (s *Store) DeletePluginMetadata(kind, id string) error {
	_, err := s.DB.Exec(`DELETE FROM plugin_metadata WHERE kind=? AND id=?`, kind, id)
	return err
}

// DeleteKindPluginMetadata 删除某类插件的全部 manifest 缓存，仅用于清理场景。
func (s *Store) DeleteKindPluginMetadata(kind string) error {
	_, err := s.DB.Exec(`DELETE FROM plugin_metadata WHERE kind=?`, kind)
	return err
}
