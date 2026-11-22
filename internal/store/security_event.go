package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"lactssl/internal/model"
)

const securityEventColumns = `id,user_id,kind,fingerprint,ip,user_agent,detail,recipients,channels,
	notify,attempts,last_error,deliveries,smtp_snapshot,created_at,next_attempt_at,delivered_at`

// CreateSecurityEvent 写入一条安全事件。事件同时是通知 outbox：
// NextAttemptAt 为空表示立即尝试投递。
func (s *Store) CreateSecurityEvent(event *model.SecurityEvent) error {
	if event == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = nowUTC()
	}
	if event.NextAttemptAt.IsZero() {
		event.NextAttemptAt = event.CreatedAt
	}
	detail, err := json.Marshal(event.Detail)
	if err != nil {
		return err
	}
	recipients, err := json.Marshal(event.Recipients)
	if err != nil {
		return err
	}
	channels, err := json.Marshal(event.Channels)
	if err != nil {
		return err
	}
	deliveries, err := json.Marshal(event.Deliveries)
	if err != nil {
		return err
	}
	smtpSnapshot := ""
	if event.SMTP != nil {
		raw, err := json.Marshal(event.SMTP)
		if err != nil {
			return err
		}
		encrypted, err := s.Cipher.Encrypt(string(raw))
		if err != nil {
			return err
		}
		smtpSnapshot = encrypted
	}
	id, err := s.DB.InsertID(`INSERT INTO security_events(
		user_id,kind,fingerprint,ip,user_agent,detail,recipients,channels,
		notify,attempts,last_error,deliveries,smtp_snapshot,created_at,next_attempt_at,delivered_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.UserID, event.Kind, event.Fingerprint, event.IP, event.UserAgent,
		string(detail), string(recipients), string(channels),
		boolInt(event.Notify), event.Attempts, event.LastError, string(deliveries), smtpSnapshot,
		event.CreatedAt, nullableTime(&event.NextAttemptAt), nullableTime(event.DeliveredAt))
	if err != nil {
		return err
	}
	event.ID = id
	return nil
}

// GetSecurityEvent 按 ID 查询安全事件。
func (s *Store) GetSecurityEvent(id int64) (*model.SecurityEvent, error) {
	row := s.DB.QueryRow(`SELECT `+securityEventColumns+` FROM security_events WHERE id=?`, id)
	return s.scanSecurityEvent(row)
}

// ListPendingSecurityEvents 返回到达投递时间、尚未完成的事件。
// next_attempt_at 为空表示已放弃重试，不再返回。
func (s *Store) ListPendingSecurityEvents(now time.Time, limit int) ([]model.SecurityEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT `+securityEventColumns+` FROM security_events
		WHERE notify=1 AND delivered_at IS NULL AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?
		ORDER BY id LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]model.SecurityEvent, 0)
	for rows.Next() {
		event, err := s.scanSecurityEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

// HasPendingSecurityEvents 判断队列中是否还有未完成的事件。
// 与 ListPendingSecurityEvents 不同，等待重试（next_attempt_at 在未来）的事件
// 也视为积压，供进程启动时决定是否需要拉起 worker。
func (s *Store) HasPendingSecurityEvents() (bool, error) {
	var exists int
	err := s.DB.QueryRow(`SELECT 1 FROM security_events
		WHERE notify=1 AND delivered_at IS NULL AND next_attempt_at IS NOT NULL LIMIT 1`).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ClaimSecurityEvent 用租约抢占一条待投递事件，避免多个进程或并发 worker
// 同时处理同一事件导致重复发送。只有成功把 next_attempt_at 从“已到期”
// 推进到未来的一方会得到 true；租约到期后事件会重新进入队列，兼顾进程崩溃恢复。
func (s *Store) ClaimSecurityEvent(id int64, now, leaseUntil time.Time) (bool, error) {
	res, err := s.DB.Exec(`UPDATE security_events SET next_attempt_at=?
		WHERE id=? AND notify=1 AND delivered_at IS NULL
			AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?`,
		leaseUntil, id, now)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// ListSecurityEvents 返回账户最近的安全事件，供设置页和测试使用。
func (s *Store) ListSecurityEvents(userID int64, limit int) ([]model.SecurityEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT `+securityEventColumns+` FROM security_events
		WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]model.SecurityEvent, 0)
	for rows.Next() {
		event, err := s.scanSecurityEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

// CountSecurityEventsSince 统计时间窗口内进入投递队列的事件数量，用于每小时上限。
func (s *Store) CountSecurityEventsSince(userID int64, since time.Time) (int, error) {
	var count int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM security_events
		WHERE user_id=? AND notify=1 AND created_at>=?`, userID, since).Scan(&count)
	return count, err
}

// HasSecurityEventKindSince 判断时间窗口内是否已经存在指定类型的事件，用于摘要去重。
func (s *Store) HasSecurityEventKindSince(userID int64, kind string, since time.Time) (bool, error) {
	var count int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM security_events
		WHERE user_id=? AND kind=? AND created_at>=? LIMIT 1`, userID, kind, since).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateSecurityEventDelivery 更新一次投递尝试的结果。
// nextAttemptAt 为空且 deliveredAt 为空表示重试已耗尽，不再进入队列。
func (s *Store) UpdateSecurityEventDelivery(id int64, deliveries map[string]string, attempts int,
	lastError string, nextAttemptAt *time.Time, deliveredAt *time.Time) error {
	encoded, err := json.Marshal(deliveries)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE security_events
		SET deliveries=?, attempts=?, last_error=?, next_attempt_at=?, delivered_at=?,
			smtp_snapshot=CASE WHEN ? IS NULL THEN '' ELSE smtp_snapshot END
		WHERE id=?`,
		string(encoded), attempts, lastError, nullableTime(nextAttemptAt), nullableTime(deliveredAt),
		nullableTime(nextAttemptAt), id)
	return err
}

// HasRecentSecurityLogin 判断同一设备指纹在时间窗口内是否已登录过。
// 阶段 0 只写入数据，阶段 1 的登录通知会使用该查询。
func (s *Store) HasRecentSecurityLogin(userID int64, fingerprint string, since time.Time) (bool, error) {
	if fingerprint == "" {
		return false, nil
	}
	var exists int
	err := s.DB.QueryRow(`SELECT 1 FROM security_events
		WHERE user_id=? AND fingerprint=? AND kind IN (?,?) AND created_at>=? LIMIT 1`,
		userID, fingerprint, model.SecurityEventLoginPassword, model.SecurityEventLoginPasskey, since).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// LatestSecurityEventStatus 返回最近一条需要投递的事件状态，用于设置页展示。
func (s *Store) LatestSecurityEventStatus(userID int64) (string, *time.Time, error) {
	var (
		lastError   string
		deliveredAt sql.NullTime
	)
	err := s.DB.QueryRow(`SELECT last_error, delivered_at FROM security_events
		WHERE user_id=? AND notify=1 ORDER BY id DESC LIMIT 1`, userID).Scan(&lastError, &deliveredAt)
	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if deliveredAt.Valid {
		value := deliveredAt.Time
		return lastError, &value, nil
	}
	return lastError, nil, nil
}

// CleanupSecurityEvents 删除保留期外的已投递或已放弃事件。
// 仍在重试中的事件不会被删除，避免清理动作把未送达的告警吞掉。
func (s *Store) CleanupSecurityEvents(before time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM security_events
		WHERE created_at < ? AND (delivered_at IS NOT NULL OR notify=0 OR next_attempt_at IS NULL)`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return *value
}

func (s *Store) scanSecurityEvent(row interface{ Scan(...any) error }) (*model.SecurityEvent, error) {
	var (
		event        model.SecurityEvent
		detail       string
		recipients   string
		channels     string
		deliveries   string
		smtpSnapshot string
		notify       int
		next         sql.NullTime
		delivered    sql.NullTime
	)
	if err := row.Scan(&event.ID, &event.UserID, &event.Kind, &event.Fingerprint, &event.IP,
		&event.UserAgent, &detail, &recipients, &channels, &notify, &event.Attempts,
		&event.LastError, &deliveries, &smtpSnapshot, &event.CreatedAt, &next, &delivered); err != nil {
		return nil, err
	}
	if smtpSnapshot != "" {
		if decrypted, err := s.Cipher.Decrypt(smtpSnapshot); err == nil {
			var snapshot model.SMTPConfigSnapshot
			if json.Unmarshal([]byte(decrypted), &snapshot) == nil {
				event.SMTP = &snapshot
			}
		}
	}
	event.Notify = notify == 1
	_ = json.Unmarshal([]byte(detail), &event.Detail)
	_ = json.Unmarshal([]byte(recipients), &event.Recipients)
	_ = json.Unmarshal([]byte(channels), &event.Channels)
	_ = json.Unmarshal([]byte(deliveries), &event.Deliveries)
	if event.Detail == nil {
		event.Detail = map[string]string{}
	}
	if event.Deliveries == nil {
		event.Deliveries = map[string]string{}
	}
	if next.Valid {
		event.NextAttemptAt = next.Time
	}
	if delivered.Valid {
		value := delivered.Time
		event.DeliveredAt = &value
	}
	return &event, nil
}
