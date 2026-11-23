// Package store 封装 SQLite 数据访问。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// Store 是数据访问入口。
type Store struct {
	DB     *db.Handle
	Cipher *util.Cipher
	// SystemLogRetentionDays 与 SystemLogMaxRows 控制 logs 表中
	// category='runtime' 记录的清理策略。由 main 根据 log.system 配置注入；为 0 时不限制。
	SystemLogRetentionDays int
	SystemLogMaxRows       int

	// systemLogMu 保证“清空”与异步批量写入不会互相穿插，
	// 避免用户刚清空后队列中的旧日志又被写回。
	systemLogMu        sync.RWMutex
	systemLogClearedAt time.Time

	// Archiver 在删除日志前执行归档。由 main 在启用归档时注入；
	// 为 nil 表示未启用归档，保留旧版直接删除行为。
	Archiver LogArchiver
}

// LogArchiveResult 描述一次成功写入的日志归档。
// 文件路径相对于归档根目录，避免把服务器绝对路径暴露给前端。
type LogArchiveResult struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Reason    string `json:"reason"`
	File      string `json:"file"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"created_at"`
}

// LogArchiver 是日志归档服务在 Store 侧的抽象。
// internal/logarchive 实现该接口，Store 不反向依赖具体包。
type LogArchiver interface {
	ClearOperationLogs(reason string, operatorID int64, operatorEmail string) (*LogArchiveResult, int64, error)
	ClearSystemLogs(reason string, operatorID int64, operatorEmail string) (*LogArchiveResult, int64, error)
	PruneOperationLogs(cutoff time.Time) (*LogArchiveResult, error)
	PruneSystemLogs(cutoff time.Time, maxRows int) ([]*LogArchiveResult, error)
	PruneArchives(now time.Time) error
}

// 系统日志默认保留策略，避免测试或未注入配置时无限增长。
const (
	DefaultSystemLogRetentionDays = 7
	DefaultSystemLogMaxRows       = 20000
)

// New 创建数据访问对象。
func New(conn *db.Handle, cipher *util.Cipher) *Store {
	return &Store{
		DB:                     conn,
		Cipher:                 cipher,
		SystemLogRetentionDays: DefaultSystemLogRetentionDays,
		SystemLogMaxRows:       DefaultSystemLogMaxRows,
	}
}

func nowUTC() time.Time { return time.Now().UTC() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const deleteChunkSize = 500

type rowQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

// selectInt64IDs 先取出待修改的主键，再分批执行 DELETE/UPDATE。
// MySQL 5.7 不允许在 UPDATE/DELETE 的子查询里引用目标表，也不允许
// LIMIT 出现在 IN 子查询中；两阶段写法可同时兼容三种数据库。
func selectInt64IDs(queryer rowQuerier, query string, args ...any) ([]int64, error) {
	rows, err := queryer.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func chunkInt64s(ids []int64, size int) [][]int64 {
	if size <= 0 {
		size = deleteChunkSize
	}
	chunks := make([][]int64, 0, (len(ids)+size-1)/size)
	for len(ids) > 0 {
		end := size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[:end:end])
		ids = ids[end:]
	}
	return chunks
}

func int64Placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return args
}

// ---------------------------- 用户 ----------------------------

// CountUsers 返回账户数量，用于判断是否需要初始化。
func (s *Store) CountUsers() (int64, error) {
	var n int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// GetUser 返回唯一账户。
func (s *Store) GetUser() (*model.User, error) {
	row := s.DB.QueryRow(`SELECT id,email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at FROM users ORDER BY id LIMIT 1`)
	return s.scanUser(row)
}

// GetUserByID 按 ID 查询账户。
func (s *Store) GetUserByID(id int64) (*model.User, error) {
	row := s.DB.QueryRow(`SELECT id,email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at FROM users WHERE id=?`, id)
	return s.scanUser(row)
}

// GetUserByEmail 按邮箱查询账户。
func (s *Store) GetUserByEmail(email string) (*model.User, error) {
	row := s.DB.QueryRow(`SELECT id,email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at FROM users WHERE email=?`, email)
	return s.scanUser(row)
}

// CreateUser 创建账户。
func (s *Store) CreateUser(email, hash string) (*model.User, error) {
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO users(email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at) VALUES(?,?,'',0,1,?,?)`, email, hash, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(id)
}

// CreateFirstUser 原子地创建首个管理员账户，避免初始化接口并发请求创建多个管理员。
func (s *Store) CreateFirstUser(email, hash string) (*model.User, error) {
	now := nowUTC()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if s.DB.Dialect().IsPostgres() {
		// SQLite 由单连接串行化；PG 需要数据库级互斥，避免两个不同邮箱的
		// 首次初始化请求同时通过 NOT EXISTS。
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, int64(0x4c414354)); err != nil {
			return nil, err
		}
	}
	if s.DB.Dialect().IsMySQL() {
		lockConn, err := s.DB.Conn(context.Background())
		if err != nil {
			return nil, err
		}
		defer lockConn.Close()
		var locked sql.NullInt64
		if err := lockConn.QueryRowContext(context.Background(), `SELECT GET_LOCK(?, 5)`, "lactssl-first-user").Scan(&locked); err != nil {
			return nil, err
		}
		defer func() {
			_, _ = lockConn.ExecContext(context.Background(), `SELECT RELEASE_LOCK(?)`, "lactssl-first-user")
		}()
		if !locked.Valid || locked.Int64 != 1 {
			return nil, i18n.E("api.error.already_initialized")
		}
	}
	insertSQL := `INSERT INTO users(email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at)
		SELECT ?,?,'',0,1,?,?`
	if s.DB.Dialect().IsMySQL() {
		insertSQL += ` FROM DUAL`
	}
	insertSQL += ` WHERE NOT EXISTS (SELECT 1 FROM users)`
	res, err := tx.Exec(insertSQL, email, hash, now, now)
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, i18n.E("api.error.already_initialized")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByEmail(email)
}

// UpdatePassword 更新密码并提升令牌版本，同时吊销全部 API 密钥与通行密钥。
//
// 浏览器会话通过 token_version 失效；API 密钥不参与 JWT 校验，必须显式删除，
// 否则会话失窃后攻击者创建的密钥在受害者改密后仍然有效。通行密钥同理：
// 持有会话的攻击者可以用自己的认证器注册通行密钥，改密后仍能登录。
func (s *Store) UpdatePassword(id int64, hash string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET password_hash=?, token_version=token_version+1, updated_at=? WHERE id=?`,
		hash, nowUTC(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM api_keys WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM passkeys WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM password_resets WHERE user_id=? AND used=0`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateEmail 更新邮箱并提升令牌版本，同时作废未使用的密码重置令牌。
func (s *Store) UpdateEmail(id int64, email string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET email=?, token_version=token_version+1, updated_at=? WHERE id=?`, email, nowUTC(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM password_resets WHERE user_id=? AND used=0`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTOTP 更新 TOTP 密钥与启用状态。密钥加密后落库。
func (s *Store) SetTOTP(id int64, secret string, enabled bool) error {
	encrypted, err := s.Cipher.Encrypt(secret)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE users SET totp_secret=?, totp_enabled=?, updated_at=? WHERE id=?`, encrypted, boolInt(enabled), nowUTC(), id)
	return err
}

// MigrateTOTPSecrets 把旧版本明文保存的 TOTP 密钥加密。
func (s *Store) MigrateTOTPSecrets() error {
	rows, err := s.DB.Query(`SELECT id,totp_secret FROM users WHERE totp_secret<>''`)
	if err != nil {
		return err
	}
	type item struct {
		id     int64
		secret string
	}
	var pending []item
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.secret); err != nil {
			_ = rows.Close()
			return err
		}
		if !util.IsEncrypted(value.secret) {
			pending = append(pending, value)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, value := range pending {
		encrypted, err := s.Cipher.Encrypt(value.secret)
		if err != nil {
			return err
		}
		if _, err := s.DB.Exec(`UPDATE users SET totp_secret=? WHERE id=?`, encrypted, value.id); err != nil {
			return err
		}
	}
	return nil
}

// BumpTokenVersion 使现有登录令牌全部失效。
func (s *Store) BumpTokenVersion(id int64) error {
	_, err := s.DB.Exec(`UPDATE users SET token_version=token_version+1, updated_at=? WHERE id=?`, nowUTC(), id)
	return err
}

// ResetRunningTasks 在服务重启后把中断的任务重新排队。
func (s *Store) ResetRunningTasks() {
	_, _ = s.DB.Exec(`UPDATE tasks SET status='pending', message=?, next_run_at=? WHERE status='running'`, i18n.Encode("task.status.requeued", nil), nowUTC())
}

// Maintenance 清理过期数据，避免数据库无限增长。
func (s *Store) Maintenance() error {
	var firstErr error
	exec := func(query string, args ...any) {
		if _, err := s.DB.Exec(query, args...); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	exec(`DELETE FROM sessions WHERE expires_at < ?`, nowUTC())
	exec(`DELETE FROM sessions WHERE revoked_at IS NOT NULL AND revoked_at < ?`, nowUTC().AddDate(0, 0, -7))
	exec(`DELETE FROM password_resets WHERE expires_at < ?`, nowUTC())
	if s.Archiver != nil {
		result, err := s.Archiver.PruneOperationLogs(nowUTC().AddDate(0, 0, -90))
		if err != nil {
			setErr(err)
		} else {
			s.recordLogArchive(result)
		}
	} else {
		exec(`DELETE FROM logs WHERE category=? AND created_at < ?`, model.LogCategoryOperation, nowUTC().AddDate(0, 0, -90))
	}
	exec(`DELETE FROM deploy_records WHERE created_at < ?`, nowUTC().AddDate(0, 0, -180))
	exec(`DELETE FROM certificate_requests WHERE status IN ('issued','failed','cancelled','expired') AND updated_at < ?`, nowUTC().AddDate(0, 0, -90))
	// 零信待验证订单包含临时私钥：失败/已签发订单保留 30 天供排查，
	// 长期未完成的 pending 订单也一并清理，避免密钥无限期驻留。
	exec(`DELETE FROM zotrus_orders WHERE status IN ('issued','failed') AND updated_at < ?`, nowUTC().AddDate(0, 0, -30))
	exec(`DELETE FROM zotrus_orders WHERE created_at < ?`, nowUTC().AddDate(0, 0, -30))
	if err := s.CleanupSystemLogs(); err != nil {
		setErr(err)
	}
	if s.Archiver != nil {
		if err := s.Archiver.PruneArchives(nowUTC()); err != nil {
			setErr(err)
		}
	}
	return firstErr
}

// recordLogArchive 为自动清理产生的归档补一条操作日志，便于事后追溯。
// 手动清空由接口层记录，避免同一动作重复写两条审计。
func (s *Store) recordLogArchive(result *LogArchiveResult) {
	if result == nil || result.Rows == 0 {
		return
	}
	s.AddLogKey("settings", 0, "info", "log.settings.logs_archived", map[string]any{
		"category": result.Category,
		"reason":   result.Reason,
		"count":    result.Rows,
		"file":     result.File,
	})
}

func (s *Store) scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var enabled int
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.TOTPSecret, &enabled, &u.TokenVersion, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	// 兼容旧版本明文密钥；新写入的密钥带 enc:v1: 前缀。
	if decrypted, err := s.Cipher.Decrypt(u.TOTPSecret); err == nil {
		u.TOTPSecret = decrypted
	} else {
		u.TOTPSecret = ""
	}
	u.TOTPEnabled = enabled == 1
	return &u, nil
}

// ---------------------------- 设置 ----------------------------

// GetSetting 读取单个设置。
func (s *Store) GetSetting(key string) (string, error) {
	v, _, err := s.LookupSetting(key)
	return v, err
}

// LookupSetting 读取设置并返回该键是否存在。
// 用于需要区分“未设置”与“显式设置为空”的配置项（例如插件仓库公钥）。
func (s *Store) LookupSetting(key string) (string, bool, error) {
	var v string
	// key/value 在 MySQL 中是关键字，统一按方言引用。
	keyColumn := s.DB.Dialect().QuoteIdent("key")
	valueColumn := s.DB.Dialect().QuoteIdent("value")
	err := s.DB.QueryRow(`SELECT `+valueColumn+` FROM settings WHERE `+keyColumn+`=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// GetSecret 读取加密设置。
func (s *Store) GetSecret(key string) (string, error) {
	v, err := s.GetSetting(key)
	if err != nil || v == "" {
		return "", err
	}
	return s.Cipher.Decrypt(v)
}

// SetSetting 写入设置。
func (s *Store) SetSetting(key, value string) error {
	keyColumn := s.DB.Dialect().QuoteIdent("key")
	valueColumn := s.DB.Dialect().QuoteIdent("value")
	query := `INSERT INTO settings(` + keyColumn + `,` + valueColumn + `) VALUES(?,?)
		ON CONFLICT(` + keyColumn + `) DO UPDATE SET ` + valueColumn + `=excluded.` + valueColumn
	if s.DB.Dialect().IsMySQL() {
		query = `INSERT INTO settings(` + keyColumn + `,` + valueColumn + `) VALUES(?,?)
			ON DUPLICATE KEY UPDATE ` + valueColumn + `=VALUES(` + valueColumn + `)`
	}
	_, err := s.DB.Exec(query, key, value)
	return err
}

// SetSecret 加密后写入设置。
func (s *Store) SetSecret(key, value string) error {
	enc, err := s.Cipher.Encrypt(value)
	if err != nil {
		return err
	}
	return s.SetSetting(key, enc)
}

// DeleteSetting 删除设置。
func (s *Store) DeleteSetting(key string) error {
	_, err := s.DB.Exec(`DELETE FROM settings WHERE `+s.DB.Dialect().QuoteIdent("key")+`=?`, key)
	return err
}

// AllSettings 返回全部设置键值。
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT ` + s.DB.Dialect().QuoteIdent("key") + `,` + s.DB.Dialect().QuoteIdent("value") + ` FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ---------------------------- 密码重置 ----------------------------

// CreatePasswordReset 保存重置令牌摘要，并使该用户旧令牌失效。
func (s *Store) CreatePasswordReset(userID int64, tokenHash string, expires time.Time) error {
	_, _ = s.DB.Exec(`DELETE FROM password_resets WHERE user_id=? AND used=0`, userID)
	_, err := s.DB.Exec(`INSERT INTO password_resets(user_id,token_hash,expires_at,used,created_at) VALUES(?,?,?,0,?)`, userID, tokenHash, expires, nowUTC())
	return err
}

// PasswordResetValid 检查重置令牌是否存在且未过期。
func (s *Store) PasswordResetValid(tokenHash string) bool {
	var expires time.Time
	var used int
	err := s.DB.QueryRow(`SELECT used,expires_at FROM password_resets WHERE token_hash=? ORDER BY id DESC LIMIT 1`, tokenHash).Scan(&used, &expires)
	return err == nil && used == 0 && time.Now().Before(expires)
}

// ConsumePasswordReset 校验并标记重置令牌为已使用，返回用户 ID。
func (s *Store) ConsumePasswordReset(tokenHash string) (int64, error) {
	var id, userID int64
	var used int
	var expires time.Time
	err := s.DB.QueryRow(`SELECT id,user_id,used,expires_at FROM password_resets WHERE token_hash=? ORDER BY id DESC LIMIT 1`, tokenHash).Scan(&id, &userID, &used, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, i18n.E("api.error.reset_link_invalid")
	}
	if err != nil {
		return 0, err
	}
	if used == 1 {
		return 0, i18n.E("api.error.reset_link_used")
	}
	if time.Now().After(expires) {
		return 0, i18n.E("api.error.reset_link_expired")
	}
	// 使用条件更新原子地消费令牌，避免并发请求把同一个令牌使用两次。
	res, err := s.DB.Exec(`UPDATE password_resets SET used=1 WHERE id=? AND used=0`, id)
	if err != nil {
		return 0, err
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return 0, i18n.E("api.error.reset_link_used")
	}
	return userID, nil
}

// ResetPassword 通过密码重置流程更新密码：提升令牌版本、清理通行密钥与 API 密钥、
// 关闭 TOTP，确保攻击者借助失窃会话建立的所有长期凭据一起失效。
func (s *Store) ResetPassword(id int64, hash string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE users SET password_hash=?, token_version=token_version+1,
		totp_secret='', totp_enabled=0, updated_at=? WHERE id=?`, hash, nowUTC(), id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM passkeys WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM api_keys WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// CleanExpiredResets 清理过期令牌。
func (s *Store) CleanExpiredResets() {
	_, _ = s.DB.Exec(`DELETE FROM password_resets WHERE expires_at < ?`, nowUTC())
}

// ---------------------------- DNS 账户 ----------------------------

func (s *Store) marshalConfig(cfg map[string]string) (string, error) {
	if cfg == nil {
		cfg = map[string]string{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return s.Cipher.Encrypt(string(raw))
}

func (s *Store) unmarshalConfig(value string) map[string]string {
	if value == "" {
		return map[string]string{}
	}
	plain, err := s.Cipher.Decrypt(value)
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(plain), &out)
	return out
}

// ListDNSAccounts 返回全部 DNS 账户。
func (s *Store) ListDNSAccounts() ([]model.DNSAccount, error) {
	rows, err := s.DB.Query(`SELECT id,name,provider,config,remark,verify_status,verify_message,verify_mode,verified_at,created_at,updated_at FROM dns_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.DNSAccount, 0)
	for rows.Next() {
		var a model.DNSAccount
		var cfg string
		if err := rows.Scan(&a.ID, &a.Name, &a.Provider, &cfg, &a.Remark,
			&a.VerifyStatus, &a.VerifyMessage, &a.VerifyMode, &a.VerifiedAt,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Config = s.unmarshalConfig(cfg)
		list = append(list, a)
	}
	return list, rows.Err()
}

// GetDNSAccount 查询单个 DNS 账户。
func (s *Store) GetDNSAccount(id int64) (*model.DNSAccount, error) {
	var a model.DNSAccount
	var cfg string
	err := s.DB.QueryRow(`SELECT id,name,provider,config,remark,verify_status,verify_message,verify_mode,verified_at,created_at,updated_at FROM dns_accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.Provider, &cfg, &a.Remark,
			&a.VerifyStatus, &a.VerifyMessage, &a.VerifyMode, &a.VerifiedAt,
			&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Config = s.unmarshalConfig(cfg)
	return &a, nil
}

// CreateDNSAccount 新建 DNS 账户。
func (s *Store) CreateDNSAccount(a *model.DNSAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO dns_accounts(name,provider,config,remark,verify_status,verify_message,verify_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Provider, cfg, a.Remark, "", "", "", now, now)
	if err != nil {
		return err
	}
	a.ID = id
	a.CreatedAt, a.UpdatedAt = now, now
	return nil
}

// UpdateDNSAccount 更新 DNS 账户。
func (s *Store) UpdateDNSAccount(a *model.DNSAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE dns_accounts SET name=?,provider=?,config=?,remark=?,verify_status=?,verify_message=?,verify_mode=?,verified_at=?,updated_at=? WHERE id=?`,
		a.Name, a.Provider, cfg, a.Remark, a.VerifyStatus, a.VerifyMessage, a.VerifyMode, a.VerifiedAt, nowUTC(), a.ID)
	return err
}

// SetDNSAccountVerification 保存最近一次可用性验证结果。
// 不更新 updated_at：验证不代表凭据配置发生变化。
func (s *Store) SetDNSAccountVerification(id int64, status, message, mode string, at *time.Time) error {
	_, err := s.DB.Exec(`UPDATE dns_accounts SET verify_status=?,verify_message=?,verify_mode=?,verified_at=? WHERE id=?`,
		status, message, mode, at, id)
	return err
}

// DeleteDNSAccount 删除 DNS 账户及其域名绑定。
func (s *Store) DeleteDNSAccount(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM dns_zone_bindings WHERE dns_account_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM dns_accounts WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------- 目标凭据 ----------------------------

// ListDeployAccounts 返回全部目标凭据。
func (s *Store) ListDeployAccounts() ([]model.DeployAccount, error) {
	rows, err := s.DB.Query(`SELECT id,name,type,config,remark,verify_status,verify_message,verify_mode,verified_at,created_at,updated_at FROM deploy_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.DeployAccount, 0)
	for rows.Next() {
		var a model.DeployAccount
		var cfg string
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &cfg, &a.Remark,
			&a.VerifyStatus, &a.VerifyMessage, &a.VerifyMode, &a.VerifiedAt,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Config = s.unmarshalConfig(cfg)
		list = append(list, a)
	}
	return list, rows.Err()
}

// GetDeployAccount 查询单个目标凭据。
func (s *Store) GetDeployAccount(id int64) (*model.DeployAccount, error) {
	var a model.DeployAccount
	var cfg string
	err := s.DB.QueryRow(`SELECT id,name,type,config,remark,verify_status,verify_message,verify_mode,verified_at,created_at,updated_at FROM deploy_accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.Type, &cfg, &a.Remark,
			&a.VerifyStatus, &a.VerifyMessage, &a.VerifyMode, &a.VerifiedAt,
			&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Config = s.unmarshalConfig(cfg)
	return &a, nil
}

// CreateDeployAccount 新建目标凭据。
func (s *Store) CreateDeployAccount(a *model.DeployAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO deploy_accounts(name,type,config,remark,verify_status,verify_message,verify_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Type, cfg, a.Remark, "", "", "", now, now)
	if err != nil {
		return err
	}
	a.ID = id
	a.CreatedAt, a.UpdatedAt = now, now
	return nil
}

// UpdateDeployAccount 更新目标凭据。
func (s *Store) UpdateDeployAccount(a *model.DeployAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE deploy_accounts SET name=?,type=?,config=?,remark=?,verify_status=?,verify_message=?,verify_mode=?,verified_at=?,updated_at=? WHERE id=?`,
		a.Name, a.Type, cfg, a.Remark, a.VerifyStatus, a.VerifyMessage, a.VerifyMode, a.VerifiedAt, nowUTC(), a.ID)
	return err
}

// SetDeployAccountVerification 保存最近一次可用性验证结果。
func (s *Store) SetDeployAccountVerification(id int64, status, message, mode string, at *time.Time) error {
	_, err := s.DB.Exec(`UPDATE deploy_accounts SET verify_status=?,verify_message=?,verify_mode=?,verified_at=? WHERE id=?`,
		status, message, mode, at, id)
	return err
}

// UpdateDeployAccountConfig 只更新凭据配置，保留名称、类型与备注。
// 用于 SSH 首次连接后回写 TOFU 主机指纹，避免整行覆盖丢失用户备注。
func (s *Store) UpdateDeployAccountConfig(id int64, config map[string]string) error {
	cfg, err := s.marshalConfig(config)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE deploy_accounts SET config=?,updated_at=? WHERE id=?`, cfg, nowUTC(), id)
	return err
}

// DeleteDeployAccount 删除目标凭据。
func (s *Store) DeleteDeployAccount(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM deploy_accounts WHERE id=?`, id)
	return err
}

// ---------------------------- 私有 CA ----------------------------

// CreatePrivateCA 保存私有 CA，私钥加密后落库。
func (s *Store) CreatePrivateCA(c *model.PrivateCA) error {
	encryptedKey, err := s.Cipher.Encrypt(c.KeyPEM)
	if err != nil {
		return err
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO private_cas(name,common_name,organization,country,key_type,source,serial,parent_id,ca_type,path_len,cert_pem,key_pem,not_before,not_after,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Name, c.CommonName, c.Organization, c.Country, c.KeyType, c.Source, c.Serial, c.ParentID, c.CAType, c.PathLen, c.CertPEM, encryptedKey, c.NotBefore, c.NotAfter, now, now)
	if err != nil {
		return err
	}
	c.ID = id
	c.CreatedAt, c.UpdatedAt = now, now
	return nil
}

// ListPrivateCAs 返回全部私有 CA，不读取证书与私钥内容。
func (s *Store) ListPrivateCAs() ([]model.PrivateCA, error) {
	rows, err := s.DB.Query(`SELECT c.id,c.name,c.common_name,c.organization,c.country,c.key_type,c.source,c.serial,c.parent_id,COALESCE(p.name,''),c.ca_type,c.path_len,c.not_before,c.not_after,c.created_at,c.updated_at
		FROM private_cas c LEFT JOIN private_cas p ON p.id=c.parent_id ORDER BY c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.PrivateCA, 0)
	for rows.Next() {
		var c model.PrivateCA
		if err := rows.Scan(&c.ID, &c.Name, &c.CommonName, &c.Organization, &c.Country, &c.KeyType, &c.Source, &c.Serial, &c.ParentID, &c.ParentName, &c.CAType, &c.PathLen, &c.NotBefore, &c.NotAfter, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ListPrivateCATrustCertificates 返回仍在有效期内的私有 CA 公钥证书，
// 供出站 TLS 信任链使用。列表接口不返回证书内容，因此单独提供此方法。
func (s *Store) ListPrivateCATrustCertificates() ([]string, error) {
	rows, err := s.DB.Query(`SELECT cert_pem FROM private_cas WHERE cert_pem<>'' AND not_after>?`, nowUTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var cert string
		if err := rows.Scan(&cert); err != nil {
			return nil, err
		}
		if strings.TrimSpace(cert) != "" {
			out = append(out, cert)
		}
	}
	return out, rows.Err()
}

// GetPrivateCA 查询单个私有 CA，并解密私钥。
func (s *Store) GetPrivateCA(id int64) (*model.PrivateCA, error) {
	var c model.PrivateCA
	var encryptedKey string
	err := s.DB.QueryRow(`SELECT c.id,c.name,c.common_name,c.organization,c.country,c.key_type,c.source,c.serial,c.parent_id,COALESCE(p.name,''),c.ca_type,c.path_len,c.cert_pem,c.key_pem,c.not_before,c.not_after,c.created_at,c.updated_at
		FROM private_cas c LEFT JOIN private_cas p ON p.id=c.parent_id WHERE c.id=?`, id).
		Scan(&c.ID, &c.Name, &c.CommonName, &c.Organization, &c.Country, &c.KeyType, &c.Source, &c.Serial, &c.ParentID, &c.ParentName, &c.CAType, &c.PathLen, &c.CertPEM, &encryptedKey, &c.NotBefore, &c.NotAfter, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.KeyPEM, err = s.Cipher.Decrypt(encryptedKey)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeletePrivateCA 删除私有 CA。存在下级 CA 时拒绝删除，避免破坏信任链。
func (s *Store) DeletePrivateCA(id int64) error {
	var children int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM private_cas WHERE parent_id=?`, id).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return i18n.E("api.error.private_ca_has_children", "count", children)
	}
	var certificates int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE private_ca_id=?`, id).Scan(&certificates); err != nil {
		return err
	}
	if certificates > 0 {
		return i18n.E("api.error.private_ca_has_certificates", "count", certificates)
	}
	var endpoints int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_endpoints WHERE ca_id=?`, id).Scan(&endpoints); err != nil {
		return err
	}
	if endpoints > 0 {
		return i18n.E("api.error.private_ca_has_acme_endpoints", "count", endpoints)
	}
	_, err := s.DB.Exec(`DELETE FROM private_cas WHERE id=?`, id)
	return err
}

// ---------------------------- 任务 ----------------------------

// ListTasks 返回全部任务。
func (s *Store) ListTasks() ([]model.Task, error) {
	rows, err := s.DB.Query(`SELECT id,name,domains,challenge,dns_account_id,dns_routes,ca,ca_pool_id,key_type,no_key_storage,auto_renew,renew_days,deploy_ids,pipeline,flow_mode,target_flow,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at FROM tasks ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

// GetTask 查询单个任务。
func (s *Store) GetTask(id int64) (*model.Task, error) {
	row := s.DB.QueryRow(`SELECT id,name,domains,challenge,dns_account_id,dns_routes,ca,ca_pool_id,key_type,no_key_storage,auto_renew,renew_days,deploy_ids,pipeline,flow_mode,target_flow,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// CreateTask 新建任务。
func (s *Store) CreateTask(t *model.Task) error {
	domains, _ := json.Marshal(t.Domains)
	dnsRoutes, _ := json.Marshal(t.DNSRoutes)
	deployIDs, _ := json.Marshal(t.DeployIDs)
	pipeline, _ := json.Marshal(t.Pipeline)
	targetFlow, _ := json.Marshal(t.TargetFlow)
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO tasks(name,domains,challenge,dns_account_id,dns_routes,ca,ca_pool_id,key_type,no_key_storage,auto_renew,renew_days,deploy_ids,pipeline,flow_mode,target_flow,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?,0,?,?)`,
		t.Name, string(domains), t.Challenge, t.DNSAccountID, string(dnsRoutes), t.CA, t.CAPoolID, t.KeyType, boolInt(t.NoKeyStorage), boolInt(t.AutoRenew), t.RenewDays, string(deployIDs), string(pipeline), normalizeFlowMode(t.FlowMode), string(targetFlow), boolInt(t.Enabled), t.Status, t.Message, now, now, now)
	if err != nil {
		return err
	}
	t.ID = id
	t.CreatedAt, t.UpdatedAt = now, now
	return nil
}

// UpdateTask 更新任务基础配置。
func (s *Store) UpdateTask(t *model.Task) error {
	domains, _ := json.Marshal(t.Domains)
	dnsRoutes, _ := json.Marshal(t.DNSRoutes)
	deployIDs, _ := json.Marshal(t.DeployIDs)
	pipeline, _ := json.Marshal(t.Pipeline)
	targetFlow, _ := json.Marshal(t.TargetFlow)
	_, err := s.DB.Exec(`UPDATE tasks SET name=?,domains=?,challenge=?,dns_account_id=?,dns_routes=?,ca=?,ca_pool_id=?,key_type=?,no_key_storage=?,auto_renew=?,renew_days=?,deploy_ids=?,pipeline=?,flow_mode=?,target_flow=?,enabled=?,updated_at=? WHERE id=?`,
		t.Name, string(domains), t.Challenge, t.DNSAccountID, string(dnsRoutes), t.CA, t.CAPoolID, t.KeyType, boolInt(t.NoKeyStorage), boolInt(t.AutoRenew), t.RenewDays, string(deployIDs), string(pipeline), normalizeFlowMode(t.FlowMode), string(targetFlow), boolInt(t.Enabled), nowUTC(), t.ID)
	return err
}

// UpdateTaskTargetFlow 只更新目标流程配置，避免执行器回写摘要时覆盖任务其他字段。
func (s *Store) UpdateTaskTargetFlow(id int64, cfg model.TargetFlowConfig) error {
	data, _ := json.Marshal(cfg)
	_, err := s.DB.Exec(`UPDATE tasks SET target_flow=?,updated_at=? WHERE id=?`, string(data), nowUTC(), id)
	return err
}

// DeleteTask 删除任务。
func (s *Store) DeleteTask(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM task_certificate_links WHERE task_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM zotrus_orders WHERE task_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateTaskResult 更新任务执行结果。
func (s *Store) UpdateTaskResult(id int64, status, message string, lastRun, nextRun *time.Time, certID int64) error {
	_, err := s.DB.Exec(`UPDATE tasks SET status=?,message=?,last_run_at=?,next_run_at=?,cert_id=?,updated_at=? WHERE id=?`,
		status, message, lastRun, nextRun, certID, nowUTC(), id)
	return err
}

// SetTaskNextRun 单独调整下一次执行时间。
func (s *Store) SetTaskNextRun(id int64, next *time.Time) error {
	_, err := s.DB.Exec(`UPDATE tasks SET next_run_at=?,updated_at=? WHERE id=?`, next, nowUTC(), id)
	return err
}

// SetTasksEnabled 批量更新任务启用状态与下一次执行时间。
func (s *Store) SetTasksEnabled(ids []int64, enabled bool, nextRuns map[int64]*time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUTC()
	var updated int64
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE tasks SET enabled=?,next_run_at=?,updated_at=? WHERE id=?`, boolInt(enabled), nextRuns[id], now, id)
		if err != nil {
			return 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			updated += n
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

// DueTasks 返回到达执行时间的任务。
func (s *Store) DueTasks(now time.Time) ([]model.Task, error) {
	rows, err := s.DB.Query(`SELECT id,name,domains,challenge,dns_account_id,dns_routes,ca,ca_pool_id,key_type,no_key_storage,auto_renew,renew_days,deploy_ids,pipeline,flow_mode,target_flow,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at
		FROM tasks WHERE enabled=1 AND status<>'running' AND next_run_at IS NOT NULL AND next_run_at<=? ORDER BY next_run_at LIMIT 10`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

func normalizeFlowMode(value string) string {
	if value == model.FlowTargetBeta {
		return model.FlowTargetBeta
	}
	return model.FlowStandard
}

func scanTask(row interface{ Scan(...any) error }) (*model.Task, error) {
	var t model.Task
	var domainsJSON, dnsRoutesJSON, deployJSON, pipelineJSON, targetFlowJSON string
	var autoRenew, enabled, noKeyStorage int
	var lastRun, nextRun sql.NullTime
	err := row.Scan(&t.ID, &t.Name, &domainsJSON, &t.Challenge, &t.DNSAccountID, &dnsRoutesJSON, &t.CA, &t.CAPoolID, &t.KeyType, &noKeyStorage, &autoRenew, &t.RenewDays, &deployJSON, &pipelineJSON, &t.FlowMode, &targetFlowJSON, &enabled, &t.Status, &t.Message, &lastRun, &nextRun, &t.CertID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(domainsJSON), &t.Domains)
	_ = json.Unmarshal([]byte(dnsRoutesJSON), &t.DNSRoutes)
	_ = json.Unmarshal([]byte(deployJSON), &t.DeployIDs)
	_ = json.Unmarshal([]byte(pipelineJSON), &t.Pipeline)
	_ = json.Unmarshal([]byte(targetFlowJSON), &t.TargetFlow)
	if t.Domains == nil {
		t.Domains = []string{}
	}
	if t.DNSRoutes == nil {
		t.DNSRoutes = []model.DNSRoute{}
	}
	if t.DeployIDs == nil {
		t.DeployIDs = []int64{}
	}
	if t.Pipeline == nil {
		t.Pipeline = []model.PipelineNode{}
	}
	t.FlowMode = normalizeFlowMode(t.FlowMode)
	t.NoKeyStorage = noKeyStorage == 1
	t.AutoRenew = autoRenew == 1
	t.Enabled = enabled == 1
	if lastRun.Valid {
		t.LastRunAt = &lastRun.Time
	}
	if nextRun.Valid {
		t.NextRunAt = &nextRun.Time
	}
	return &t, nil
}

// ---------------------------- 证书 ----------------------------

// CreateCertificate 保存证书。证书私钥与其他敏感字段一样加密落库。
func (s *Store) CreateCertificate(c *model.Certificate) error {
	return s.insertCertificate(s.DB, c)
}

// certificateExecer 同时兼容 *db.Handle 与 *db.Tx。
type certificateExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
	InsertID(query string, args ...any) (int64, error)
}

// insertCertificate 在指定执行器上插入证书记录。
func (s *Store) insertCertificate(exec certificateExecer, c *model.Certificate) error {
	domains, _ := json.Marshal(c.Domains)
	now := nowUTC()
	source := c.Source
	if source == "" {
		source = "task"
	}
	encryptedKey, err := s.Cipher.Encrypt(c.KeyPEM)
	if err != nil {
		return err
	}
	id, err := exec.InsertID(`INSERT INTO certificates(task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.TaskID, string(domains), c.PrimaryDomain, c.Issuer, c.CA, source, c.PrivateCAID, c.ACMEOrderID, c.ACMEAccountID, c.CSRRequestID, c.CertPEM, encryptedKey, c.FullchainPEM, c.Serial, c.NotBefore, c.NotAfter, c.Status, now)
	if err != nil {
		return err
	}
	c.ID = id
	c.CreatedAt = now
	return nil
}

// CreateCertificateForACMEOrder 在同一事务内写入证书并把订单标记为 valid。
// 避免证书已落库但订单状态更新失败时，客户端重试 finalize 重复签发。
func (s *Store) CreateCertificateForACMEOrder(orderID int64, c *model.Certificate) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.insertCertificate(tx, c); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE acme_orders SET status='valid',error='',certificate_id=?,updated_at=? WHERE id=?`,
		c.ID, nowUTC(), orderID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// MigrateCertificateKeys 把旧版本明文保存的证书私钥加密。
func (s *Store) MigrateCertificateKeys() error {
	rows, err := s.DB.Query(`SELECT id,key_pem FROM certificates WHERE key_pem<>''`)
	if err != nil {
		return err
	}
	type item struct {
		id  int64
		key string
	}
	var pending []item
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.key); err != nil {
			_ = rows.Close()
			return err
		}
		if !util.IsEncrypted(value.key) {
			pending = append(pending, value)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, value := range pending {
		encrypted, err := s.Cipher.Encrypt(value.key)
		if err != nil {
			return err
		}
		if _, err := s.DB.Exec(`UPDATE certificates SET key_pem=? WHERE id=?`, encrypted, value.id); err != nil {
			return err
		}
	}
	return nil
}

// GetCertificate 查询证书。
func (s *Store) GetCertificate(id int64) (*model.Certificate, error) {
	row := s.DB.QueryRow(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END FROM certificates WHERE id=?`, id)
	return s.scanCertificate(row)
}

// LatestTaskCertificate 返回任务最近一张证书（含私钥），没有时返回 nil。
func (s *Store) LatestTaskCertificate(taskID int64) (*model.Certificate, error) {
	row := s.DB.QueryRow(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END FROM certificates WHERE task_id=? ORDER BY id DESC LIMIT 1`, taskID)
	cert, err := s.scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return cert, err
}

// ReplaceTaskCertificateLinks 用本次执行实际使用的证书替换任务关联。
// 目标流程可能复用属于其他任务的证书，因此需要独立关联表；普通任务仍然
// 通过 certificates.task_id 关联。
func (s *Store) ReplaceTaskCertificateLinks(taskID int64, certIDs []int64) error {
	if taskID <= 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM task_certificate_links WHERE task_id=?`, taskID); err != nil {
		return err
	}
	now := nowUTC()
	seen := make(map[int64]bool, len(certIDs))
	for _, certID := range certIDs {
		if certID <= 0 || seen[certID] {
			continue
		}
		seen[certID] = true
		if _, err := tx.Exec(`INSERT INTO task_certificate_links(task_id,cert_id,created_at) VALUES(?,?,?)`, taskID, certID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListTaskCertificates 返回任务关联的全部证书（含私钥），用于目标流程 Beta
// 计算最早续期时间与证书复用。
func (s *Store) ListTaskCertificates(taskID int64) ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END
		FROM certificates
		WHERE task_id=? OR id IN (SELECT cert_id FROM task_certificate_links WHERE task_id=?)
		ORDER BY id DESC`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Certificate, 0)
	for rows.Next() {
		cert, err := s.scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *cert)
	}
	return list, rows.Err()
}

// ListReusableCertificates 返回指定 CA 下仍有效、未撤销且保存了私钥的证书。
// 用于任务执行前复用同一组域名的证书，避免重复向上游 CA 下单。
func (s *Store) ListReusableCertificates(ca string) ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END FROM certificates WHERE ca=? AND status='valid' AND revoked_at IS NULL AND not_after>? AND key_pem<>'' ORDER BY id DESC`, ca, nowUTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Certificate, 0)
	for rows.Next() {
		cert, err := s.scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *cert)
	}
	return list, rows.Err()
}

// ListReusableCertificatesForCAs 返回多个 CA 中仍有效、未撤销且保存了私钥的证书。
// 用于 CA 调度组的证书复用，避免只匹配单个 CA 时无法复用组内其他 CA 的证书。
func (s *Store) ListReusableCertificatesForCAs(cas []string) ([]model.Certificate, error) {
	if len(cas) == 0 {
		return []model.Certificate{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(cas)), ",")
	args := make([]any, 0, len(cas)+1)
	for _, ca := range cas {
		args = append(args, ca)
	}
	args = append(args, nowUTC())
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END FROM certificates WHERE ca IN (`+placeholders+`) AND status='valid' AND revoked_at IS NULL AND not_after>? AND key_pem<>'' ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Certificate, 0)
	for rows.Next() {
		cert, err := s.scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *cert)
	}
	return list, rows.Err()
}

// ListCertificates 返回全部证书（不包含 PEM 大字段）。
func (s *Store) ListCertificates() ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,'','','',serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END FROM certificates ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Certificate, 0)
	for rows.Next() {
		c, err := s.scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *c)
	}
	return list, rows.Err()
}

// DeleteCertificate 删除证书。
func (s *Store) DeleteCertificate(id int64) error {
	_, err := s.deleteCertificatesWhere(`id=?`, id)
	return err
}

// CertCleanupFilter 描述一键清理证书时使用的筛选条件。
type CertCleanupFilter struct {
	// Scope 为 expired、revoked、valid 或 all。
	Scope string
	// ExpiredDays 仅保留过期时间不足该天数的证书，0 表示全部已过期证书。
	ExpiredDays int
	// RevokedDays 仅保留撤销时间不足该天数的证书，0 表示全部已撤销证书。
	RevokedDays int
	// OlderThanDays 仅清理签发时间早于该天数的证书，0 表示不限。
	OlderThanDays int
	// Domain 按域名关键字过滤。
	Domain string
	// CA 按颁发机构标识过滤。
	CA string
	// Source 按证书来源过滤，例如 task、acme。
	Source string
	// KeepLatestPerTask 为 true 时保留每个任务最新的一张证书。
	KeepLatestPerTask bool
}

// where 生成清理条件与参数。
func (f CertCleanupFilter) where(dialect db.Dialect, now time.Time) (string, []any) {
	conditions := make([]string, 0, 6)
	args := make([]any, 0, 8)
	switch f.Scope {
	case "expired":
		conditions = append(conditions, `not_after < ?`)
		args = append(args, now.UTC().AddDate(0, 0, -max(f.ExpiredDays, 0)))
	case "revoked":
		conditions = append(conditions, `status = 'revoked'`)
		if f.RevokedDays > 0 {
			conditions = append(conditions, `revoked_at IS NOT NULL AND revoked_at < ?`)
			args = append(args, now.UTC().AddDate(0, 0, -f.RevokedDays))
		}
	case "valid":
		conditions = append(conditions, `status = 'valid'`)
	case "all":
		// 不加状态条件。
	}
	if f.OlderThanDays > 0 {
		conditions = append(conditions, `created_at < ?`)
		args = append(args, now.UTC().AddDate(0, 0, -f.OlderThanDays))
	}
	if domain := strings.TrimSpace(f.Domain); domain != "" {
		conditions = append(conditions, `(`+dialect.ContainsFoldExpr("primary_domain")+` OR `+dialect.ContainsFoldExpr("domains")+`)`)
		args = append(args, domain, domain)
	}
	if ca := strings.TrimSpace(f.CA); ca != "" {
		conditions = append(conditions, `ca = ?`)
		args = append(args, ca)
	}
	if source := strings.TrimSpace(f.Source); source != "" {
		conditions = append(conditions, `source = ?`)
		args = append(args, source)
	}
	if f.KeepLatestPerTask {
		conditions = append(conditions, `id NOT IN (SELECT MAX(id) FROM certificates WHERE task_id > 0 GROUP BY task_id)`)
		// 目标流程 Beta 一个任务可能有多张当前有效证书，不能按“每个任务一张”
		// 清理，否则会误删其他证书组。开启保留策略时对 Beta 证书整体跳过清理。
		conditions = append(conditions, `task_id NOT IN (SELECT id FROM tasks WHERE flow_mode='target_beta')`)
	}
	if len(conditions) == 0 {
		return `1=1`, nil
	}
	return strings.Join(conditions, ` AND `), args
}

// CleanupExpiredCertificates 删除已过期证书并解除任务引用，返回删除数量。
func (s *Store) CleanupExpiredCertificates(now time.Time) (int64, error) {
	return s.CleanupCertificates(CertCleanupFilter{Scope: "expired"}, now)
}

// CleanupAllCertificates 删除全部证书并解除任务引用，返回删除数量。
func (s *Store) CleanupAllCertificates() (int64, error) {
	return s.CleanupCertificates(CertCleanupFilter{Scope: "all"}, time.Now())
}

// valid 限制清理范围，避免未知 scope 退化为 1=1 删除全部证书。
func (f CertCleanupFilter) valid() bool {
	switch f.Scope {
	case "expired", "revoked", "valid", "all":
		return true
	default:
		return false
	}
}

// CountCertificatesForCleanup 返回符合清理条件的证书数量。
func (s *Store) CountCertificatesForCleanup(filter CertCleanupFilter, now time.Time) (int64, error) {
	if !filter.valid() {
		return 0, fmt.Errorf("非法的证书清理范围: %q", filter.Scope)
	}
	where, args := filter.where(s.DB.Dialect(), now)
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE `+where, args...).Scan(&count)
	return count, err
}

// CleanupCertificates 按条件删除证书，并在同一事务中清理任务和 ACME 订单的引用。
func (s *Store) CleanupCertificates(filter CertCleanupFilter, now time.Time) (int64, error) {
	if !filter.valid() {
		return 0, fmt.Errorf("非法的证书清理范围: %q", filter.Scope)
	}
	where, args := filter.where(s.DB.Dialect(), now)
	return s.deleteCertificatesWhere(where, args...)
}

// deleteCertificatesWhere 在事务中删除符合条件的证书，避免任务仍引用已删除的证书。
// 先查主键再分批修改引用表，兼容 MySQL 不允许在修改语句里引用目标表的限制。
func (s *Store) deleteCertificatesWhere(where string, args ...any) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := selectInt64IDs(tx, `SELECT id FROM certificates WHERE `+where, args...)
	if err != nil {
		return 0, err
	}
	var deleted int64
	for _, chunk := range chunkInt64s(ids, deleteChunkSize) {
		placeholders := int64Placeholders(len(chunk))
		chunkArgs := int64Args(chunk)
		if _, err := tx.Exec(`UPDATE tasks SET cert_id=0 WHERE cert_id IN (`+placeholders+`)`, chunkArgs...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE acme_orders SET certificate_id=0 WHERE certificate_id IN (`+placeholders+`)`, chunkArgs...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE certificate_requests SET certificate_id=0 WHERE certificate_id IN (`+placeholders+`)`, chunkArgs...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM task_certificate_links WHERE cert_id IN (`+placeholders+`)`, chunkArgs...); err != nil {
			return 0, err
		}
		res, err := tx.Exec(`DELETE FROM certificates WHERE id IN (`+placeholders+`)`, chunkArgs...)
		if err != nil {
			return 0, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) scanCertificate(row interface{ Scan(...any) error }) (*model.Certificate, error) {
	var c model.Certificate
	var domainsJSON string
	var revokedAt sql.NullTime
	var keyAvailable int
	if err := row.Scan(&c.ID, &c.TaskID, &domainsJSON, &c.PrimaryDomain, &c.Issuer, &c.CA, &c.Source, &c.PrivateCAID, &c.ACMEOrderID, &c.ACMEAccountID, &c.CSRRequestID, &c.CertPEM, &c.KeyPEM, &c.FullchainPEM, &c.Serial, &c.NotBefore, &c.NotAfter, &c.Status, &revokedAt, &c.RevokeReason, &c.CreatedAt, &keyAvailable); err != nil {
		return nil, err
	}
	c.KeyAvailable = keyAvailable == 1
	if revokedAt.Valid {
		c.RevokedAt = &revokedAt.Time
	}
	// 兼容旧版本明文私钥；解密失败时按空值处理，避免把密文当成私钥使用。
	if decrypted, err := s.Cipher.Decrypt(c.KeyPEM); err == nil {
		c.KeyPEM = decrypted
	} else {
		c.KeyPEM = ""
	}
	_ = json.Unmarshal([]byte(domainsJSON), &c.Domains)
	if c.Domains == nil {
		c.Domains = []string{}
	}
	return &c, nil
}

// ---------------------------- 部署记录 ----------------------------

// CreateDeployRecord 写入部署记录。
func (s *Store) CreateDeployRecord(r *model.DeployRecord) error {
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO deploy_records(cert_id,deploy_account_id,account_name,task_id,status,message,created_at) VALUES(?,?,?,?,?,?,?)`,
		r.CertID, r.DeployAccountID, r.AccountName, r.TaskID, r.Status, r.Message, now)
	if err != nil {
		return err
	}
	r.ID = id
	r.CreatedAt = now
	return nil
}

// ListDeployRecords 返回最近的部署记录。
func (s *Store) ListDeployRecords(limit int) ([]model.DeployRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.Query(`SELECT id,cert_id,deploy_account_id,account_name,task_id,status,message,created_at FROM deploy_records ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.DeployRecord, 0)
	for rows.Next() {
		var r model.DeployRecord
		if err := rows.Scan(&r.ID, &r.CertID, &r.DeployAccountID, &r.AccountName, &r.TaskID, &r.Status, &r.Message, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// ClearDeployRecords 删除全部部署记录，返回删除行数。
func (s *Store) ClearDeployRecords() (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM deploy_records`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------- 监控 ----------------------------

// ListMonitors 返回监控项。
func (s *Store) ListMonitors() ([]model.Monitor, error) {
	rows, err := s.DB.Query(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at,allow_internal FROM monitors ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Monitor, 0)
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, rows.Err()
}

// GetMonitor 查询监控项。
func (s *Store) GetMonitor(id int64) (*model.Monitor, error) {
	row := s.DB.QueryRow(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at,allow_internal FROM monitors WHERE id=?`, id)
	return scanMonitor(row)
}

// CreateMonitor 新建监控项。
func (s *Store) CreateMonitor(m *model.Monitor) error {
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO monitors(name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at,allow_internal) VALUES(?,?,?,?,?,?,NULL,'',NULL,?,?,?)`,
		m.Name, m.Target, m.IntervalHour, boolInt(m.Enabled), "pending", "", now, now, boolInt(m.AllowInternal))
	if err != nil {
		return err
	}
	m.ID = id
	m.Status = "pending"
	m.CreatedAt = now
	m.NextCheckAt = &now
	return nil
}

// UpdateMonitor 更新监控配置。
func (s *Store) UpdateMonitor(m *model.Monitor) error {
	_, err := s.DB.Exec(`UPDATE monitors SET name=?,target=?,interval_hours=?,enabled=?,allow_internal=?,next_check_at=? WHERE id=?`,
		m.Name, m.Target, m.IntervalHour, boolInt(m.Enabled), boolInt(m.AllowInternal), nowUTC(), m.ID)
	return err
}

// DeleteMonitor 删除监控项。
func (s *Store) DeleteMonitor(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM monitors WHERE id=?`, id)
	return err
}

// UpdateMonitorResult 更新监控结果。
func (s *Store) UpdateMonitorResult(id int64, status, message, issuer string, expires *time.Time, lastCheck, nextCheck *time.Time) error {
	_, err := s.DB.Exec(`UPDATE monitors SET status=?,message=?,issuer=?,expires_at=?,last_check_at=?,next_check_at=? WHERE id=?`,
		status, message, issuer, expires, lastCheck, nextCheck, id)
	return err
}

// DueMonitors 返回需要检查的监控项。
func (s *Store) DueMonitors(now time.Time) ([]model.Monitor, error) {
	rows, err := s.DB.Query(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at,allow_internal
		FROM monitors WHERE enabled=1 AND next_check_at IS NOT NULL AND next_check_at<=? ORDER BY next_check_at LIMIT 10`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.Monitor, 0)
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, rows.Err()
}

func scanMonitor(row interface{ Scan(...any) error }) (*model.Monitor, error) {
	var m model.Monitor
	var enabled, allowInternal int
	var expires, lastCheck, nextCheck sql.NullTime
	if err := row.Scan(&m.ID, &m.Name, &m.Target, &m.IntervalHour, &enabled, &m.Status, &m.Message, &expires, &m.Issuer, &lastCheck, &nextCheck, &m.CreatedAt, &allowInternal); err != nil {
		return nil, err
	}
	m.Enabled = enabled == 1
	m.AllowInternal = allowInternal == 1
	if expires.Valid {
		m.ExpiresAt = &expires.Time
	}
	if lastCheck.Valid {
		m.LastCheckAt = &lastCheck.Time
	}
	if nextCheck.Valid {
		m.NextCheckAt = &nextCheck.Time
	}
	return &m, nil
}

// ---------------------------- 日志 ----------------------------

// AddLog 写入一条操作日志。
func (s *Store) AddLog(source string, refID int64, level, message string) {
	_ = s.AddLogEvent(model.LogEntry{
		Category:  model.LogCategoryOperation,
		Source:    source,
		RefType:   source,
		RefID:     refID,
		Level:     level,
		Message:   message,
		CreatedAt: nowUTC(),
	})
}

// AddLogKey 写入一条语言无关的结构化日志，读取时按请求语言渲染。
func (s *Store) AddLogKey(source string, refID int64, level, key string, params map[string]any) {
	s.AddLog(source, refID, level, i18n.Encode(key, params))
}

// AddLogEvent 写入一条统一日志。Category 为空时按操作日志处理。
// 业务日志保持同步单条写入；进程日志由 AddSystemLogs 批量写入。
func (s *Store) AddLogEvent(entry model.LogEntry) error {
	category := strings.TrimSpace(entry.Category)
	if category == "" {
		category = model.LogCategoryOperation
	}
	source := strings.TrimSpace(entry.Source)
	if source == "" {
		source = "app"
	}
	refType := strings.TrimSpace(entry.RefType)
	if refType == "" {
		refType = source
	}
	level := strings.ToLower(strings.TrimSpace(entry.Level))
	if level == "" {
		level = "info"
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}
	_, err := s.DB.Exec(
		`INSERT INTO logs(category,source,ref_type,ref_id,level,message,fields,request_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		truncateLogText(category, 16),
		truncateLogText(source, 64),
		truncateLogText(refType, 32),
		entry.RefID,
		truncateLogText(level, 16),
		entry.Message,
		entry.Fields,
		truncateLogText(entry.RequestID, 64),
		createdAt.UTC(),
	)
	return err
}

// ListLogsByRef 查询某个任务或对象的操作日志。
func (s *Store) ListLogsByRef(source string, refID int64, limit int) ([]model.LogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(
		`SELECT id,category,source,ref_type,ref_id,level,message,fields,request_id,created_at
		 FROM logs WHERE category=? AND source=? AND ref_id=?
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		model.LogCategoryOperation, source, refID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, entry)
	}
	return list, rows.Err()
}

// DeleteTaskLogs 删除任务关联的执行日志（任务日志与部署日志），返回删除行数。
// 仅删除操作日志分类，不影响系统日志与其它任务。
func (s *Store) DeleteTaskLogs(taskID int64) (int64, error) {
	if taskID <= 0 {
		return 0, nil
	}
	result, err := s.DB.Exec(
		`DELETE FROM logs WHERE category=? AND ref_id=? AND source IN (?,?)`,
		model.LogCategoryOperation, taskID, "task", "deploy",
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// LogFilter 是日志查询的过滤条件。
// Category 为空表示不限制分类；From/To 为左闭右开区间，零值表示不限制。
type LogFilter struct {
	Category string
	Source   string
	RefType  string
	RefID    int64
	Level    string
	Query    string
	From     *time.Time
	To       *time.Time
}

// likeEscapeClause 返回 LIKE 的转义字符声明。使用 '!' 而不是反斜杠，
// 避免 MySQL NO_BACKSLASH_ESCAPES 与 PostgreSQL standard_conforming_strings
// 等 sql_mode/会话设置改变字符串字面量语义。
func (s *Store) likeEscapeClause() string {
	return `ESCAPE '!'`
}

func (s *Store) likeOperator() string {
	if s.DB != nil && s.DB.Dialect().IsPostgres() {
		// SQLite 的 LIKE 默认对 ASCII 大小写不敏感，PG 用 ILIKE 保持搜索体验一致。
		return "ILIKE"
	}
	return "LIKE"
}

// logFilterWhere 构造过滤条件，供列表与导出复用。
// Category 为空时按操作日志处理，保持旧接口只返回业务日志的行为；
// 系统日志的调用方会显式传入 runtime。
func logFilterWhere(filter LogFilter, likeOp, escapeClause string) (string, []any) {
	if likeOp == "" {
		likeOp = "LIKE"
	}
	category := filter.Category
	if category == "" {
		category = model.LogCategoryOperation
	}
	clauses := make([]string, 0, 8)
	args := make([]any, 0, 8)
	clauses = append(clauses, "category=?")
	args = append(args, category)
	if filter.Source != "" {
		clauses = append(clauses, "source=?")
		args = append(args, filter.Source)
	}
	if filter.RefType != "" {
		clauses = append(clauses, "ref_type=?")
		args = append(args, filter.RefType)
	}
	if filter.RefID > 0 {
		clauses = append(clauses, "ref_id=?")
		args = append(args, filter.RefID)
	}
	if filter.Level != "" {
		clauses = append(clauses, "level=?")
		args = append(args, filter.Level)
	}
	if filter.Query != "" {
		// 两类日志都同时匹配正文与附加字段：操作日志的 message 是 i18n 信封，
		// 参数值（任务名、域名等）也存放在其中，可以直接被搜索到。
		pattern := "%" + escapeLike(filter.Query) + "%"
		clauses = append(clauses, `(message `+likeOp+` ? `+escapeClause+` OR fields `+likeOp+` ? `+escapeClause+`)`)
		args = append(args, pattern, pattern)
	}
	if filter.From != nil {
		clauses = append(clauses, "created_at>=?")
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		clauses = append(clauses, "created_at<?")
		args = append(args, *filter.To)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListLogsFiltered 按过滤条件分页查询日志。
func (s *Store) ListLogsFiltered(filter LogFilter, limit, offset int) ([]model.LogEntry, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, whereArgs := logFilterWhere(filter, s.likeOperator(), s.likeEscapeClause())
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM logs`+where, whereArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args := append(append([]any{}, whereArgs...), limit, offset)
	rows, err := s.DB.Query(
		`SELECT id,category,source,ref_type,ref_id,level,message,fields,request_id,created_at
		 FROM logs`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, entry)
	}
	return list, total, rows.Err()
}

// ListLogs 分页查询操作日志，保留原有调用方式。
func (s *Store) ListLogs(source string, limit, offset int) ([]model.LogEntry, int64, error) {
	return s.ListLogsFiltered(LogFilter{Category: model.LogCategoryOperation, Source: source}, limit, offset)
}

// ClearOperationLogs 清空全部操作日志，返回删除行数。
// 启用归档时先归档再删除；调用方负责在清空后写入审计日志。
func (s *Store) ClearOperationLogs() (int64, error) {
	deleted, _, err := s.ClearOperationLogsWithArchive("manual_clear", 0, "")
	return deleted, err
}

// ClearOperationLogsWithArchive 归档并清空操作日志。
// 归档未启用时退回直接删除，并返回 nil 归档结果。
func (s *Store) ClearOperationLogsWithArchive(reason string, operatorID int64, operatorEmail string) (int64, *LogArchiveResult, error) {
	if s.Archiver == nil {
		deleted, err := s.clearOperationLogsDirect()
		return deleted, nil, err
	}
	result, deleted, err := s.Archiver.ClearOperationLogs(reason, operatorID, operatorEmail)
	return deleted, result, err
}

func (s *Store) clearOperationLogsDirect() (int64, error) {
	result, err := s.DB.Exec(`DELETE FROM logs WHERE category=?`, model.LogCategoryOperation)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ---------- 归档使用的底层原语 ----------

// MaxLogIDByCategory 返回指定分类当前最大日志 ID。
func (s *Store) MaxLogIDByCategory(category string) (int64, error) {
	var id int64
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM logs WHERE category=?`, category).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// LogBoundaryID 返回“保留最新 keep 条”时最旧保留记录的 ID。
// 调用方应以该 ID 作为排他上界删除更旧的记录；返回 0 表示当前记录数不超过 keep。
func (s *Store) LogBoundaryID(category string, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM logs WHERE category=? ORDER BY id DESC LIMIT 1 OFFSET ?`, category, keep-1).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// LogsForArchive 按 ID 升序分批读取待归档日志。
// beforeID 是排他上界（不包含该 ID）；cutoff 非空时只读取 created_at < cutoff 的记录。
func (s *Store) LogsForArchive(category string, beforeID int64, cutoff *time.Time, afterID int64, limit int) ([]model.LogEntry, error) {
	if beforeID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	clauses := []string{"category=?", "id<?", "id>?"}
	args := []any{category, beforeID, afterID}
	if cutoff != nil && !cutoff.IsZero() {
		clauses = append(clauses, "created_at<?")
		args = append(args, cutoff.UTC())
	}
	args = append(args, limit)
	rows, err := s.DB.Query(
		`SELECT id,category,source,ref_type,ref_id,level,message,fields,request_id,created_at
		 FROM logs WHERE `+strings.Join(clauses, " AND ")+` ORDER BY id ASC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0, limit)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, entry)
	}
	return list, rows.Err()
}

// DeleteLogsForArchive 删除与归档快照完全一致的日志范围。
// beforeID 是排他上界（不包含该 ID），因此可以安全处理删除后产生的 ID 空洞。
func (s *Store) DeleteLogsForArchive(category string, beforeID int64, cutoff *time.Time) (int64, error) {
	if beforeID <= 0 {
		return 0, nil
	}
	sqlText := `DELETE FROM logs WHERE category=? AND id<?`
	args := []any{category, beforeID}
	if cutoff != nil && !cutoff.IsZero() {
		sqlText += ` AND created_at<?`
		args = append(args, cutoff.UTC())
	}
	result, err := s.DB.Exec(sqlText, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// MaxLogID 返回当前最大日志 ID，用于导出时固定快照上界。
func (s *Store) MaxLogID() (int64, error) {
	var id int64
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM logs`).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// ExportLogs 按 ID 游标倒序读取日志，避免导出时长时间持有结果集。
// maxID 固定快照上界，beforeID 为上一批最后一条日志的 ID（0 表示从 maxID 开始）。
func (s *Store) ExportLogs(filter LogFilter, maxID, beforeID int64, limit int) ([]model.LogEntry, error) {
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	where, whereArgs := logFilterWhere(filter, s.likeOperator(), s.likeEscapeClause())
	clauses := []string{"id<=?"}
	args := []any{maxID}
	if beforeID > 0 {
		clauses = append(clauses, "id<?")
		args = append(args, beforeID)
	}
	if where != "" {
		clauses = append(clauses, strings.TrimPrefix(where, " WHERE "))
		args = append(args, whereArgs...)
	}
	sql := `SELECT id,category,source,ref_type,ref_id,level,message,fields,request_id,created_at
		FROM logs WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0, limit)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, entry)
	}
	return list, rows.Err()
}

// scanLogEntry 按统一列顺序读取一行日志。
func scanLogEntry(scanner interface{ Scan(...any) error }) (model.LogEntry, error) {
	var entry model.LogEntry
	err := scanner.Scan(
		&entry.ID,
		&entry.Category,
		&entry.Source,
		&entry.RefType,
		&entry.RefID,
		&entry.Level,
		&entry.Message,
		&entry.Fields,
		&entry.RequestID,
		&entry.CreatedAt,
	)
	return entry, err
}

// ---------------------------- 系统日志 ----------------------------

// SystemLogFilter 是「系统日志」页面的筛选条件。
// Query 同时匹配 message 与 fields；From/To 为左闭右开区间。
type SystemLogFilter struct {
	Level     string
	Component string
	Query     string
	From      *time.Time
	To        *time.Time
}

// toLogFilter 把旧接口的筛选条件转换为统一日志筛选。
func (f SystemLogFilter) toLogFilter() LogFilter {
	level := strings.ToLower(strings.TrimSpace(f.Level))
	if level == "warning" {
		level = "warn"
	}
	return LogFilter{
		Category: model.LogCategoryRuntime,
		Source:   f.Component,
		Level:    level,
		Query:    f.Query,
		From:     f.From,
		To:       f.To,
	}
}

func systemLogEntryFromLog(entry model.LogEntry) model.SystemLogEntry {
	return model.SystemLogEntry{
		ID:        entry.ID,
		Level:     entry.Level,
		Component: entry.Source,
		Message:   entry.Message,
		Fields:    entry.Fields,
		RefType:   entry.RefType,
		RefID:     entry.RefID,
		RequestID: entry.RequestID,
		CreatedAt: entry.CreatedAt,
	}
}

// ListSystemLogs 按筛选条件分页查询进程日志。
func (s *Store) ListSystemLogs(filter SystemLogFilter, limit, offset int) ([]model.SystemLogEntry, int64, error) {
	list, total, err := s.ListLogsFiltered(filter.toLogFilter(), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]model.SystemLogEntry, 0, len(list))
	for _, entry := range list {
		entries = append(entries, systemLogEntryFromLog(entry))
	}
	return entries, total, nil
}

// MaxSystemLogID 返回当前最大系统日志 ID，用于导出时固定快照上界。
func (s *Store) MaxSystemLogID() (int64, error) {
	var id int64
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM logs WHERE category=?`, model.LogCategoryRuntime).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// ExportSystemLogs 按 ID 游标倒序读取系统日志，避免导出时长时间持有结果集。
func (s *Store) ExportSystemLogs(filter SystemLogFilter, maxID, beforeID int64, limit int) ([]model.SystemLogEntry, error) {
	list, err := s.ExportLogs(filter.toLogFilter(), maxID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]model.SystemLogEntry, 0, len(list))
	for _, entry := range list {
		entries = append(entries, systemLogEntryFromLog(entry))
	}
	return entries, nil
}

// AddSystemLogs 批量写入进程日志。单条失败会回滚整批，避免页面出现半截记录。
func (s *Store) AddSystemLogs(entries []model.SystemLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	// 与 ClearSystemLogs 串行化：清空开始后，队列中时间戳早于清空时刻的记录不再写入。
	s.systemLogMu.RLock()
	defer s.systemLogMu.RUnlock()
	clearedAt := s.systemLogClearedAt
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(
		`INSERT INTO logs(category,source,ref_type,ref_id,level,message,fields,request_id,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, entry := range entries {
		level := strings.ToLower(strings.TrimSpace(entry.Level))
		if level == "" {
			level = "info"
		}
		component := strings.TrimSpace(entry.Component)
		if component == "" {
			component = "app"
		}
		createdAt := entry.CreatedAt
		if createdAt.IsZero() {
			createdAt = nowUTC()
		}
		if !clearedAt.IsZero() && !createdAt.After(clearedAt) {
			continue
		}
		if _, err := stmt.Exec(
			model.LogCategoryRuntime,
			truncateLogText(component, 64),
			truncateLogText(strings.TrimSpace(entry.RefType), 32),
			entry.RefID,
			truncateLogText(level, 16),
			truncateLogText(entry.Message, 2000),
			truncateLogText(entry.Fields, 64000),
			truncateLogText(strings.TrimSpace(entry.RequestID), 64),
			createdAt.UTC(),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SystemLogComponents 返回页面筛选下拉框使用的组件列表。
func (s *Store) SystemLogComponents() ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT source FROM logs WHERE category=? ORDER BY source`, model.LogCategoryRuntime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]string, 0)
	for rows.Next() {
		var component string
		if err := rows.Scan(&component); err != nil {
			return nil, err
		}
		if component != "" {
			list = append(list, component)
		}
	}
	return list, rows.Err()
}

// ClearSystemLogs 清空全部系统日志，返回删除行数。
func (s *Store) ClearSystemLogs() (int64, error) {
	deleted, _, err := s.ClearSystemLogsWithArchive("manual_clear", 0, "")
	return deleted, err
}

// ClearSystemLogsWithArchive 归档并清空系统日志。
// 归档未启用时退回直接删除，并返回 nil 归档结果。
func (s *Store) ClearSystemLogsWithArchive(reason string, operatorID int64, operatorEmail string) (int64, *LogArchiveResult, error) {
	if s.Archiver == nil {
		deleted, err := s.clearSystemLogsDirect()
		return deleted, nil, err
	}
	result, deleted, err := s.Archiver.ClearSystemLogs(reason, operatorID, operatorEmail)
	return deleted, result, err
}

func (s *Store) clearSystemLogsDirect() (int64, error) {
	s.systemLogMu.Lock()
	defer s.systemLogMu.Unlock()
	s.systemLogClearedAt = nowUTC()
	result, err := s.DB.Exec(`DELETE FROM logs WHERE category=?`, model.LogCategoryRuntime)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ClearSystemLogsThrough 删除快照上界内的系统日志，并更新清空标记。
// 归档必须先完成再调用该方法；maxID 为 0 时仍会更新标记，保持“清空空日志”
// 也能丢弃队列中旧日志的原有语义。
func (s *Store) ClearSystemLogsThrough(maxID int64) (int64, error) {
	s.systemLogMu.Lock()
	defer s.systemLogMu.Unlock()
	s.systemLogClearedAt = nowUTC()
	if maxID <= 0 {
		return 0, nil
	}
	result, err := s.DB.Exec(`DELETE FROM logs WHERE category=? AND id<=?`, model.LogCategoryRuntime, maxID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CleanupSystemLogs 按保留天数与最大行数清理系统日志。
// 启用归档时先归档再删除；自动清理产生的归档会记录操作日志。
func (s *Store) CleanupSystemLogs() error {
	if s.Archiver != nil {
		cutoff := time.Time{}
		if s.SystemLogRetentionDays > 0 {
			cutoff = nowUTC().AddDate(0, 0, -s.SystemLogRetentionDays)
		}
		results, err := s.Archiver.PruneSystemLogs(cutoff, s.SystemLogMaxRows)
		for _, result := range results {
			s.recordLogArchive(result)
		}
		return err
	}
	var firstErr error
	if s.SystemLogRetentionDays > 0 {
		if _, err := s.DB.Exec(`DELETE FROM logs WHERE category=? AND created_at < ?`,
			model.LogCategoryRuntime, nowUTC().AddDate(0, 0, -s.SystemLogRetentionDays)); err != nil {
			firstErr = err
		}
	}
	if s.SystemLogMaxRows > 0 {
		boundary, err := s.LogBoundaryID(model.LogCategoryRuntime, s.SystemLogMaxRows)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if boundary > 0 {
			if _, err := s.DB.Exec(`DELETE FROM logs WHERE category=? AND id<?`,
				model.LogCategoryRuntime, boundary); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// escapeLike 转义 LIKE 通配符，避免用户输入被当作模式。
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	value = strings.ReplaceAll(value, `_`, `!_`)
	return value
}

// truncateLogText 按 rune 截断，避免把多字节字符切坏。
func truncateLogText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// ---------------------------- 仪表盘 ----------------------------

// Dashboard 汇总首页统计信息。
func (s *Store) Dashboard() (*model.Dashboard, error) {
	d := &model.Dashboard{}
	now := time.Now().UTC()
	in30 := now.AddDate(0, 0, 30)

	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates`).Scan(&d.CertTotal)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after>? AND not_after<=?`, now, in30).Scan(&d.CertExpiring)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after<=?`, now).Scan(&d.CertExpired)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&d.TaskTotal)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE enabled=1`).Scan(&d.TaskEnabled)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM monitors`).Scan(&d.MonitorTotal)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM monitors WHERE status IN ('expiring','expired','error')`).Scan(&d.MonitorIssue)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM deploy_records`).Scan(&d.DeployTotal)

	var success, total int64
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM deploy_records WHERE status='success'`).Scan(&success)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM deploy_records`).Scan(&total)
	if total > 0 {
		d.SuccessRate = float64(success) / float64(total) * 100
	} else {
		d.SuccessRate = 100
	}

	// 到期分布
	buckets := []model.Bucket{
		{Label: "已过期"},
		{Label: "7 天内"},
		{Label: "30 天内"},
		{Label: "90 天内"},
		{Label: "90 天以上"},
	}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after<=?`, now).Scan(&buckets[0].Count)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after>? AND not_after<=?`, now, now.AddDate(0, 0, 7)).Scan(&buckets[1].Count)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after>? AND not_after<=?`, now.AddDate(0, 0, 7), in30).Scan(&buckets[2].Count)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after>? AND not_after<=?`, in30, now.AddDate(0, 0, 90)).Scan(&buckets[3].Count)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE not_after>?`, now.AddDate(0, 0, 90)).Scan(&buckets[4].Count)
	d.ExpiryBuckets = buckets

	// 最近 14 天签发趋势
	trendMap := map[string]int64{}
	rows, err := s.DB.Query(`SELECT ` + s.DB.Dialect().DateKeyExpr("created_at") + ` AS d, COUNT(*) FROM certificates GROUP BY d`)
	if err == nil {
		for rows.Next() {
			var day string
			var count int64
			if err := rows.Scan(&day, &count); err == nil {
				trendMap[day] = count
			}
		}
		_ = rows.Close()
	}
	for i := 13; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		d.Trend = append(d.Trend, model.TrendPoint{Date: day, Count: trendMap[day]})
	}

	tasks, _ := s.ListTasks()
	if len(tasks) > 5 {
		tasks = tasks[:5]
	}
	d.RecentTasks = tasks

	certs, _ := s.ListCertificates()
	if len(certs) > 5 {
		certs = certs[:5]
	}
	d.RecentCerts = certs

	logs, _, _ := s.ListLogs("", 8, 0)
	d.RecentLogs = logs

	return d, nil
}

// Ensure 用于早期开发阶段确认 Store 实现完整。
