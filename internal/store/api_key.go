package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"lactssl/internal/model"
)

// ---------------------------- API 密钥 ----------------------------

const apiKeyColumns = `id,user_id,name,prefix,token_hash,scopes,enabled,expires_at,last_used_at,created_at,updated_at`

// ListAPIKeys 返回账户下的全部 API 密钥，不包含明文令牌。
func (s *Store) ListAPIKeys(userID int64) ([]model.APIKey, error) {
	rows, err := s.DB.Query(`SELECT `+apiKeyColumns+` FROM api_keys WHERE user_id=? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.APIKey, 0)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *key)
	}
	return list, rows.Err()
}

// GetAPIKey 按 ID 查询某个 API 密钥。
func (s *Store) GetAPIKey(userID, id int64) (*model.APIKey, error) {
	row := s.DB.QueryRow(`SELECT `+apiKeyColumns+` FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	return scanAPIKey(row)
}

// GetAPIKeyByTokenHash 按令牌摘要查找 API 密钥。
func (s *Store) GetAPIKeyByTokenHash(hash string) (*model.APIKey, error) {
	row := s.DB.QueryRow(`SELECT `+apiKeyColumns+` FROM api_keys WHERE token_hash=?`, hash)
	return scanAPIKey(row)
}

// CreateAPIKey 写入一条 API 密钥。
func (s *Store) CreateAPIKey(key *model.APIKey) error {
	scopes, err := json.Marshal(normalizeScopes(key.Scopes))
	if err != nil {
		return err
	}
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO api_keys(user_id,name,prefix,token_hash,scopes,enabled,expires_at,last_used_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		key.UserID, key.Name, key.Prefix, key.TokenHash, string(scopes), boolInt(key.Enabled),
		key.ExpiresAt, key.LastUsedAt, now, now)
	if err != nil {
		return err
	}
	key.ID, _ = res.LastInsertId()
	key.CreatedAt, key.UpdatedAt = now, now
	return nil
}

// UpdateAPIKey 更新名称、权限、启用状态与过期时间。
func (s *Store) UpdateAPIKey(key *model.APIKey) error {
	scopes, err := json.Marshal(normalizeScopes(key.Scopes))
	if err != nil {
		return err
	}
	res, err := s.DB.Exec(`UPDATE api_keys SET name=?,scopes=?,enabled=?,expires_at=?,updated_at=?
		WHERE id=? AND user_id=?`,
		key.Name, string(scopes), boolInt(key.Enabled), key.ExpiresAt, nowUTC(), key.ID, key.UserID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RotateAPIKey 更新令牌摘要与前缀，使旧令牌立即失效。
func (s *Store) RotateAPIKey(userID, id int64, prefix, hash string) error {
	res, err := s.DB.Exec(`UPDATE api_keys SET prefix=?,token_hash=?,updated_at=? WHERE id=? AND user_id=?`,
		prefix, hash, nowUTC(), id, userID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAPIKey 删除 API 密钥，返回是否命中记录。
func (s *Store) DeleteAPIKey(userID, id int64) (bool, error) {
	res, err := s.DB.Exec(`DELETE FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// TouchAPIKey 更新最近使用时间；cutoff 用于限制写入频率。
func (s *Store) TouchAPIKey(id int64, now, cutoff time.Time) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
		now, id, cutoff)
	return err
}

func scanAPIKey(row interface{ Scan(...any) error }) (*model.APIKey, error) {
	var (
		key      model.APIKey
		scopes   string
		enabled  int
		expires  sql.NullTime
		lastUsed sql.NullTime
	)
	if err := row.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.TokenHash, &scopes,
		&enabled, &expires, &lastUsed, &key.CreatedAt, &key.UpdatedAt); err != nil {
		return nil, err
	}
	key.Scopes = []string{}
	_ = json.Unmarshal([]byte(scopes), &key.Scopes)
	if key.Scopes == nil {
		key.Scopes = []string{}
	}
	key.Enabled = enabled == 1
	if expires.Valid {
		value := expires.Time
		key.ExpiresAt = &value
	}
	if lastUsed.Valid {
		value := lastUsed.Time
		key.LastUsedAt = &value
	}
	return &key, nil
}

func normalizeScopes(scopes []string) []string {
	if scopes == nil {
		return []string{}
	}
	return scopes
}
