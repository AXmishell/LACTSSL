package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"lactssl/internal/model"
)

// ---------------------------- API 密钥 ----------------------------

const apiKeyColumns = `id,user_id,name,prefix,token_hash,scopes,allowed_ips,allowed_dns_account_ids,enabled,expires_at,last_used_at,last_used_ip,created_at,updated_at` // #nosec G101 -- SQL 列名列表，非凭据

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
	scopes, err := json.Marshal(normalizeStringList(key.Scopes))
	if err != nil {
		return err
	}
	allowedIPs, err := json.Marshal(normalizeStringList(key.AllowedIPs))
	if err != nil {
		return err
	}
	allowedDNSAccounts, err := json.Marshal(normalizeInt64List(key.AllowedDNSAccountIDs))
	if err != nil {
		return err
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO api_keys(user_id,name,prefix,token_hash,scopes,allowed_ips,allowed_dns_account_ids,enabled,expires_at,last_used_at,last_used_ip,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		key.UserID, key.Name, key.Prefix, key.TokenHash, string(scopes), string(allowedIPs), string(allowedDNSAccounts),
		boolInt(key.Enabled), key.ExpiresAt, key.LastUsedAt, "", now, now)
	if err != nil {
		return err
	}
	key.ID = id
	key.CreatedAt, key.UpdatedAt = now, now
	return nil
}

// UpdateAPIKey 更新名称、权限、IP 白名单、启用状态与过期时间。
func (s *Store) UpdateAPIKey(key *model.APIKey) error {
	scopes, err := json.Marshal(normalizeStringList(key.Scopes))
	if err != nil {
		return err
	}
	allowedIPs, err := json.Marshal(normalizeStringList(key.AllowedIPs))
	if err != nil {
		return err
	}
	allowedDNSAccounts, err := json.Marshal(normalizeInt64List(key.AllowedDNSAccountIDs))
	if err != nil {
		return err
	}
	res, err := s.DB.Exec(`UPDATE api_keys SET name=?,scopes=?,allowed_ips=?,allowed_dns_account_ids=?,enabled=?,expires_at=?,updated_at=?
		WHERE id=? AND user_id=?`,
		key.Name, string(scopes), string(allowedIPs), string(allowedDNSAccounts),
		boolInt(key.Enabled), key.ExpiresAt, nowUTC(), key.ID, key.UserID)
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

// TouchAPIKey 更新最近使用时间与来源 IP；cutoff 用于限制写入频率。
// ip 为空时保留原值，避免解析失败时把已有诊断信息清掉。
func (s *Store) TouchAPIKey(id int64, now, cutoff time.Time, ip string) error {
	if ip == "" {
		_, err := s.DB.Exec(`UPDATE api_keys SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
			now, id, cutoff)
		return err
	}
	_, err := s.DB.Exec(`UPDATE api_keys SET last_used_at=?,last_used_ip=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
		now, ip, id, cutoff)
	return err
}

func scanAPIKey(row interface{ Scan(...any) error }) (*model.APIKey, error) {
	var (
		key        model.APIKey
		scopes     string
		allowedIPs string
		allowedDNS string
		enabled    int
		expires    sql.NullTime
		lastUsed   sql.NullTime
	)
	if err := row.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.TokenHash, &scopes,
		&allowedIPs, &allowedDNS, &enabled, &expires, &lastUsed, &key.LastUsedIP, &key.CreatedAt, &key.UpdatedAt); err != nil {
		return nil, err
	}
	key.Scopes = []string{}
	_ = json.Unmarshal([]byte(scopes), &key.Scopes)
	if key.Scopes == nil {
		key.Scopes = []string{}
	}
	key.AllowedIPs = []string{}
	_ = json.Unmarshal([]byte(allowedIPs), &key.AllowedIPs)
	if key.AllowedIPs == nil {
		key.AllowedIPs = []string{}
	}
	key.AllowedDNSAccountIDs = []int64{}
	_ = json.Unmarshal([]byte(allowedDNS), &key.AllowedDNSAccountIDs)
	if key.AllowedDNSAccountIDs == nil {
		key.AllowedDNSAccountIDs = []int64{}
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

// normalizeStringList 保证 JSON 列写入 [] 而不是 null，读取端无需区分两种空值。
func normalizeStringList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// normalizeInt64List 保证 JSON 列写入 [] 而不是 null。
func normalizeInt64List(values []int64) []int64 {
	if values == nil {
		return []int64{}
	}
	return values
}
