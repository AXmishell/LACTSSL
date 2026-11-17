package store

import (
	"database/sql"
	"errors"

	"lactssl/internal/model"
)

// ListPluginInstallations 返回全部仓库安装记录。
func (s *Store) ListPluginInstallations() ([]model.PluginInstallation, error) {
	rows, err := s.DB.Query(`SELECT kind,id,version,filename,sha256,asset_sha256,source,repo_url,installed_at,updated_at FROM plugin_installations ORDER BY kind,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.PluginInstallation, 0)
	for rows.Next() {
		var item model.PluginInstallation
		if err := rows.Scan(&item.Kind, &item.ID, &item.Version, &item.Filename, &item.SHA256, &item.AssetSHA256, &item.Source, &item.RepoURL, &item.InstalledAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetPluginInstallation 查询单个安装记录。
func (s *Store) GetPluginInstallation(kind, id string) (model.PluginInstallation, bool, error) {
	var item model.PluginInstallation
	err := s.DB.QueryRow(`SELECT kind,id,version,filename,sha256,asset_sha256,source,repo_url,installed_at,updated_at FROM plugin_installations WHERE kind=? AND id=?`, kind, id).
		Scan(&item.Kind, &item.ID, &item.Version, &item.Filename, &item.SHA256, &item.AssetSHA256, &item.Source, &item.RepoURL, &item.InstalledAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PluginInstallation{}, false, nil
	}
	if err != nil {
		return model.PluginInstallation{}, false, err
	}
	return item, true, nil
}

// UpsertPluginInstallation 写入或更新安装记录。
func (s *Store) UpsertPluginInstallation(item model.PluginInstallation) error {
	now := nowUTC()
	if item.InstalledAt.IsZero() {
		item.InstalledAt = now
	}
	item.UpdatedAt = now
	_, err := s.DB.Exec(`INSERT INTO plugin_installations(kind,id,version,filename,sha256,asset_sha256,source,repo_url,installed_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET version=excluded.version,filename=excluded.filename,sha256=excluded.sha256,
		asset_sha256=excluded.asset_sha256,source=excluded.source,repo_url=excluded.repo_url,updated_at=excluded.updated_at`,
		item.Kind, item.ID, item.Version, item.Filename, item.SHA256, item.AssetSHA256, item.Source, item.RepoURL, item.InstalledAt, item.UpdatedAt)
	return err
}

// DeletePluginInstallation 删除安装记录。
func (s *Store) DeletePluginInstallation(kind, id string) error {
	_, err := s.DB.Exec(`DELETE FROM plugin_installations WHERE kind=? AND id=?`, kind, id)
	return err
}
