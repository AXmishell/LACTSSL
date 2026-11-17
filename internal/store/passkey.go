package store

import (
	"database/sql"

	"lactssl/internal/model"
)

// CreatePasskey 保存新的通行密钥凭据。
func (s *Store) CreatePasskey(item *model.Passkey) error {
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO passkeys(user_id,name,credential_id,credential,created_at,last_used_at)
		VALUES(?,?,?,?,?,?)`, item.UserID, item.Name, item.CredentialID, item.Credential, now, item.LastUsedAt)
	if err != nil {
		return err
	}
	item.ID, _ = res.LastInsertId()
	item.CreatedAt = now
	return nil
}

// ListPasskeys 返回账户下全部通行密钥。
func (s *Store) ListPasskeys(userID int64) ([]model.Passkey, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,name,credential_id,credential,created_at,last_used_at
		FROM passkeys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.Passkey, 0)
	for rows.Next() {
		item, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// GetPasskeyByCredentialID 按 WebAuthn 凭据 ID 查询通行密钥。
func (s *Store) GetPasskeyByCredentialID(credentialID string) (*model.Passkey, error) {
	row := s.DB.QueryRow(`SELECT id,user_id,name,credential_id,credential,created_at,last_used_at
		FROM passkeys WHERE credential_id=?`, credentialID)
	return scanPasskey(row)
}

// UpdatePasskeyUsage 更新凭据计数器、标记信息与最近使用时间。
func (s *Store) UpdatePasskeyUsage(id int64, credential string) error {
	_, err := s.DB.Exec(`UPDATE passkeys SET credential=?, last_used_at=? WHERE id=?`, credential, nowUTC(), id)
	return err
}

// DeletePasskey 删除账户下的指定通行密钥。
func (s *Store) DeletePasskey(userID, id int64) (bool, error) {
	res, err := s.DB.Exec(`DELETE FROM passkeys WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// CountPasskeys 返回账户下通行密钥数量。
func (s *Store) CountPasskeys(userID int64) (int64, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM passkeys WHERE user_id=?`, userID).Scan(&count)
	return count, err
}

// DeletePasskeys 删除账户下全部通行密钥。
func (s *Store) DeletePasskeys(userID int64) error {
	_, err := s.DB.Exec(`DELETE FROM passkeys WHERE user_id=?`, userID)
	return err
}

type passkeyScanner interface {
	Scan(dest ...any) error
}

func scanPasskey(row passkeyScanner) (*model.Passkey, error) {
	var (
		item       model.Passkey
		lastUsedAt sql.NullTime
	)
	if err := row.Scan(&item.ID, &item.UserID, &item.Name, &item.CredentialID,
		&item.Credential, &item.CreatedAt, &lastUsedAt); err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		at := lastUsedAt.Time
		item.LastUsedAt = &at
	}
	return &item, nil
}
