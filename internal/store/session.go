package store

import (
	"database/sql"
	"time"

	"lactssl/internal/model"
)

// CreateSession 写入一条登录会话。已存在同 ID 记录时保持原值，
// 便于旧版令牌在首次访问时自动补登记。
func (s *Store) CreateSession(session *model.Session) error {
	_, err := s.DB.Exec(`INSERT INTO sessions(id,user_id,ip,user_agent,strong_auth,created_at,last_seen_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		session.ID, session.UserID, session.IP, session.UserAgent, boolInt(session.StrongAuth),
		session.CreatedAt, session.LastSeenAt, session.ExpiresAt)
	return err
}

// GetSession 按会话 ID 查询记录。
func (s *Store) GetSession(id string) (*model.Session, error) {
	var (
		session    model.Session
		revoked    sql.NullTime
		strongAuth int
	)
	err := s.DB.QueryRow(`SELECT id,user_id,ip,user_agent,strong_auth,created_at,last_seen_at,expires_at,revoked_at
		FROM sessions WHERE id=?`, id).
		Scan(&session.ID, &session.UserID, &session.IP, &session.UserAgent, &strongAuth,
			&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &revoked)
	if err != nil {
		return nil, err
	}
	session.StrongAuth = strongAuth == 1
	if revoked.Valid {
		at := revoked.Time
		session.RevokedAt = &at
	}
	return &session, nil
}

// ListSessions 返回账户下所有未撤销且未过期的会话。
func (s *Store) ListSessions(userID int64, now time.Time) ([]model.Session, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,ip,user_agent,strong_auth,created_at,last_seen_at,expires_at
		FROM sessions WHERE user_id=? AND revoked_at IS NULL AND expires_at > ?
		ORDER BY last_seen_at DESC`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]model.Session, 0)
	for rows.Next() {
		var (
			session    model.Session
			strongAuth int
		)
		if err := rows.Scan(&session.ID, &session.UserID, &session.IP, &session.UserAgent, &strongAuth,
			&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt); err != nil {
			return nil, err
		}
		session.StrongAuth = strongAuth == 1
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// TouchSession 更新会话最近活跃时间；cutoff 用于限制写入频率。
func (s *Store) TouchSession(id string, now, cutoff time.Time) error {
	_, err := s.DB.Exec(`UPDATE sessions SET last_seen_at=? WHERE id=? AND last_seen_at < ?`, now, id, cutoff)
	return err
}

// RevokeSession 撤销指定会话，返回是否命中记录。
func (s *Store) RevokeSession(userID int64, id string) (bool, error) {
	res, err := s.DB.Exec(`UPDATE sessions SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`,
		nowUTC(), id, userID)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// RevokeOtherSessions 撤销除当前会话外的所有会话，返回撤销数量。
func (s *Store) RevokeOtherSessions(userID int64, keepID string) (int64, error) {
	res, err := s.DB.Exec(`UPDATE sessions SET revoked_at=?
		WHERE user_id=? AND id<>? AND revoked_at IS NULL AND expires_at > ?`,
		nowUTC(), userID, keepID, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteUserSessions 删除账户的全部会话记录。
// 用于改密、换邮箱等会提升 TokenVersion 的操作。
func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// DeleteAllSessions 删除全部服务端会话记录。
// 进程启动时调用，与启动级 JWT 签名密钥配合，确保重启后旧登录不可恢复，
// 同时避免设置页继续列出实际已不可用的历史会话。
func (s *Store) DeleteAllSessions() error {
	_, err := s.DB.Exec(`DELETE FROM sessions`)
	return err
}
