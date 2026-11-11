// Package store 封装 SQLite 数据访问。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// Store 是数据访问入口。
type Store struct {
	DB     *sql.DB
	Cipher *util.Cipher
}

// New 创建数据访问对象。
func New(db *sql.DB, cipher *util.Cipher) *Store {
	return &Store{DB: db, Cipher: cipher}
}

func nowUTC() time.Time { return time.Now().UTC() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	res, err := s.DB.Exec(`INSERT INTO users(email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at) VALUES(?,?,'',0,1,?,?)`, email, hash, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUserByID(id)
}

// CreateFirstUser 原子地创建首个管理员账户，避免初始化接口并发请求创建多个管理员。
func (s *Store) CreateFirstUser(email, hash string) (*model.User, error) {
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO users(email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at)
		SELECT ?,?,'',0,1,?,? WHERE NOT EXISTS (SELECT 1 FROM users)`, email, hash, now, now)
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, i18n.E("api.error.already_initialized")
	}
	return s.GetUserByEmail(email)
}

// UpdatePassword 更新密码并提升令牌版本。
func (s *Store) UpdatePassword(id int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE users SET password_hash=?, token_version=token_version+1, updated_at=? WHERE id=?`, hash, nowUTC(), id)
	return err
}

// UpdateEmail 更新邮箱并提升令牌版本。
func (s *Store) UpdateEmail(id int64, email string) error {
	_, err := s.DB.Exec(`UPDATE users SET email=?, token_version=token_version+1, updated_at=? WHERE id=?`, email, nowUTC(), id)
	return err
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
			rows.Close()
			return err
		}
		if !util.IsEncrypted(value.secret) {
			pending = append(pending, value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
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
func (s *Store) Maintenance() {
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, nowUTC())
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE revoked_at IS NOT NULL AND revoked_at < ?`, nowUTC().AddDate(0, 0, -7))
	_, _ = s.DB.Exec(`DELETE FROM password_resets WHERE expires_at < ?`, nowUTC())
	_, _ = s.DB.Exec(`DELETE FROM logs WHERE created_at < ?`, nowUTC().AddDate(0, 0, -90))
	_, _ = s.DB.Exec(`DELETE FROM deploy_records WHERE created_at < ?`, nowUTC().AddDate(0, 0, -180))
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
	var v string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
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
	_, err := s.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
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
	_, err := s.DB.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}

// AllSettings 返回全部设置键值。
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT key,value FROM settings`)
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

// ResetPassword 通过密码重置流程更新密码：提升令牌版本、清理会话并关闭 TOTP，
// 这样即使攻击者曾利用被盗会话绑定 TOTP，邮箱持有者仍能恢复账户。
func (s *Store) ResetPassword(id int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE users SET password_hash=?, token_version=token_version+1,
		totp_secret='', totp_enabled=0, updated_at=? WHERE id=?`, hash, nowUTC(), id)
	return err
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
	rows, err := s.DB.Query(`SELECT id,name,provider,config,remark,created_at,updated_at FROM dns_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.DNSAccount, 0)
	for rows.Next() {
		var a model.DNSAccount
		var cfg string
		if err := rows.Scan(&a.ID, &a.Name, &a.Provider, &cfg, &a.Remark, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	err := s.DB.QueryRow(`SELECT id,name,provider,config,remark,created_at,updated_at FROM dns_accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.Provider, &cfg, &a.Remark, &a.CreatedAt, &a.UpdatedAt)
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
	res, err := s.DB.Exec(`INSERT INTO dns_accounts(name,provider,config,remark,created_at,updated_at) VALUES(?,?,?,?,?,?)`, a.Name, a.Provider, cfg, a.Remark, now, now)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	a.CreatedAt, a.UpdatedAt = now, now
	return nil
}

// UpdateDNSAccount 更新 DNS 账户。
func (s *Store) UpdateDNSAccount(a *model.DNSAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE dns_accounts SET name=?,provider=?,config=?,remark=?,updated_at=? WHERE id=?`, a.Name, a.Provider, cfg, a.Remark, nowUTC(), a.ID)
	return err
}

// DeleteDNSAccount 删除 DNS 账户。
func (s *Store) DeleteDNSAccount(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM dns_accounts WHERE id=?`, id)
	return err
}

// ---------------------------- 目标凭据 ----------------------------

// ListDeployAccounts 返回全部目标凭据。
func (s *Store) ListDeployAccounts() ([]model.DeployAccount, error) {
	rows, err := s.DB.Query(`SELECT id,name,type,config,remark,created_at,updated_at FROM deploy_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.DeployAccount, 0)
	for rows.Next() {
		var a model.DeployAccount
		var cfg string
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &cfg, &a.Remark, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	err := s.DB.QueryRow(`SELECT id,name,type,config,remark,created_at,updated_at FROM deploy_accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.Type, &cfg, &a.Remark, &a.CreatedAt, &a.UpdatedAt)
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
	res, err := s.DB.Exec(`INSERT INTO deploy_accounts(name,type,config,remark,created_at,updated_at) VALUES(?,?,?,?,?,?)`, a.Name, a.Type, cfg, a.Remark, now, now)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	a.CreatedAt, a.UpdatedAt = now, now
	return nil
}

// UpdateDeployAccount 更新目标凭据。
func (s *Store) UpdateDeployAccount(a *model.DeployAccount) error {
	cfg, err := s.marshalConfig(a.Config)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE deploy_accounts SET name=?,type=?,config=?,remark=?,updated_at=? WHERE id=?`, a.Name, a.Type, cfg, a.Remark, nowUTC(), a.ID)
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
	res, err := s.DB.Exec(`INSERT INTO private_cas(name,common_name,organization,country,key_type,source,serial,parent_id,ca_type,path_len,cert_pem,key_pem,not_before,not_after,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Name, c.CommonName, c.Organization, c.Country, c.KeyType, c.Source, c.Serial, c.ParentID, c.CAType, c.PathLen, c.CertPEM, encryptedKey, c.NotBefore, c.NotAfter, now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
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
	rows, err := s.DB.Query(`SELECT id,name,domains,challenge,dns_account_id,ca,key_type,auto_renew,renew_days,deploy_ids,pipeline,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at FROM tasks ORDER BY id DESC`)
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
	row := s.DB.QueryRow(`SELECT id,name,domains,challenge,dns_account_id,ca,key_type,auto_renew,renew_days,deploy_ids,pipeline,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// CreateTask 新建任务。
func (s *Store) CreateTask(t *model.Task) error {
	domains, _ := json.Marshal(t.Domains)
	deployIDs, _ := json.Marshal(t.DeployIDs)
	pipeline, _ := json.Marshal(t.Pipeline)
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO tasks(name,domains,challenge,dns_account_id,ca,key_type,auto_renew,renew_days,deploy_ids,pipeline,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?,0,?,?)`,
		t.Name, string(domains), t.Challenge, t.DNSAccountID, t.CA, t.KeyType, boolInt(t.AutoRenew), t.RenewDays, string(deployIDs), string(pipeline), boolInt(t.Enabled), t.Status, t.Message, now, now, now)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	t.CreatedAt, t.UpdatedAt = now, now
	return nil
}

// UpdateTask 更新任务基础配置。
func (s *Store) UpdateTask(t *model.Task) error {
	domains, _ := json.Marshal(t.Domains)
	deployIDs, _ := json.Marshal(t.DeployIDs)
	pipeline, _ := json.Marshal(t.Pipeline)
	_, err := s.DB.Exec(`UPDATE tasks SET name=?,domains=?,challenge=?,dns_account_id=?,ca=?,key_type=?,auto_renew=?,renew_days=?,deploy_ids=?,pipeline=?,enabled=?,updated_at=? WHERE id=?`,
		t.Name, string(domains), t.Challenge, t.DNSAccountID, t.CA, t.KeyType, boolInt(t.AutoRenew), t.RenewDays, string(deployIDs), string(pipeline), boolInt(t.Enabled), nowUTC(), t.ID)
	return err
}

// DeleteTask 删除任务。
func (s *Store) DeleteTask(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
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
	rows, err := s.DB.Query(`SELECT id,name,domains,challenge,dns_account_id,ca,key_type,auto_renew,renew_days,deploy_ids,pipeline,enabled,status,message,last_run_at,next_run_at,cert_id,created_at,updated_at
		FROM tasks WHERE enabled=1 AND next_run_at IS NOT NULL AND next_run_at<=? ORDER BY next_run_at LIMIT 10`, now)
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

func scanTask(row interface{ Scan(...any) error }) (*model.Task, error) {
	var t model.Task
	var domainsJSON, deployJSON, pipelineJSON string
	var autoRenew, enabled int
	var lastRun, nextRun sql.NullTime
	err := row.Scan(&t.ID, &t.Name, &domainsJSON, &t.Challenge, &t.DNSAccountID, &t.CA, &t.KeyType, &autoRenew, &t.RenewDays, &deployJSON, &pipelineJSON, &enabled, &t.Status, &t.Message, &lastRun, &nextRun, &t.CertID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(domainsJSON), &t.Domains)
	_ = json.Unmarshal([]byte(deployJSON), &t.DeployIDs)
	_ = json.Unmarshal([]byte(pipelineJSON), &t.Pipeline)
	if t.Domains == nil {
		t.Domains = []string{}
	}
	if t.DeployIDs == nil {
		t.DeployIDs = []int64{}
	}
	if t.Pipeline == nil {
		t.Pipeline = []model.PipelineNode{}
	}
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
	res, err := s.DB.Exec(`INSERT INTO certificates(task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.TaskID, string(domains), c.PrimaryDomain, c.Issuer, c.CA, source, c.PrivateCAID, c.ACMEOrderID, c.ACMEAccountID, c.CertPEM, encryptedKey, c.FullchainPEM, c.Serial, c.NotBefore, c.NotAfter, c.Status, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = now
	return nil
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
			rows.Close()
			return err
		}
		if !util.IsEncrypted(value.key) {
			pending = append(pending, value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
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
	row := s.DB.QueryRow(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at FROM certificates WHERE id=?`, id)
	return s.scanCertificate(row)
}

// ListCertificates 返回全部证书（不包含 PEM 大字段）。
func (s *Store) ListCertificates() ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,'','','',serial,not_before,not_after,status,revoked_at,revoke_reason,created_at FROM certificates ORDER BY id DESC`)
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
func (f CertCleanupFilter) where(now time.Time) (string, []any) {
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
		conditions = append(conditions, `(instr(lower(primary_domain), lower(?)) > 0 OR instr(lower(domains), lower(?)) > 0)`)
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

// CountCertificatesForCleanup 返回符合清理条件的证书数量。
func (s *Store) CountCertificatesForCleanup(filter CertCleanupFilter, now time.Time) (int64, error) {
	where, args := filter.where(now)
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM certificates WHERE `+where, args...).Scan(&count)
	return count, err
}

// CleanupCertificates 按条件删除证书，并在同一事务中清理任务和 ACME 订单的引用。
func (s *Store) CleanupCertificates(filter CertCleanupFilter, now time.Time) (int64, error) {
	where, args := filter.where(now)
	return s.deleteCertificatesWhere(where, args...)
}

// deleteCertificatesWhere 在事务中删除符合条件的证书，避免任务仍引用已删除的证书。
func (s *Store) deleteCertificatesWhere(where string, args ...any) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE tasks SET cert_id=0 WHERE cert_id IN (SELECT id FROM certificates WHERE `+where+`)`, args...); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE acme_orders SET certificate_id=0 WHERE certificate_id IN (SELECT id FROM certificates WHERE `+where+`)`, args...); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM certificates WHERE `+where, args...)
	if err != nil {
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
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
	if err := row.Scan(&c.ID, &c.TaskID, &domainsJSON, &c.PrimaryDomain, &c.Issuer, &c.CA, &c.Source, &c.PrivateCAID, &c.ACMEOrderID, &c.ACMEAccountID, &c.CertPEM, &c.KeyPEM, &c.FullchainPEM, &c.Serial, &c.NotBefore, &c.NotAfter, &c.Status, &revokedAt, &c.RevokeReason, &c.CreatedAt); err != nil {
		return nil, err
	}
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
	res, err := s.DB.Exec(`INSERT INTO deploy_records(cert_id,deploy_account_id,account_name,task_id,status,message,created_at) VALUES(?,?,?,?,?,?,?)`,
		r.CertID, r.DeployAccountID, r.AccountName, r.TaskID, r.Status, r.Message, now)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
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

// ---------------------------- 监控 ----------------------------

// ListMonitors 返回监控项。
func (s *Store) ListMonitors() ([]model.Monitor, error) {
	rows, err := s.DB.Query(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at FROM monitors ORDER BY id DESC`)
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
	row := s.DB.QueryRow(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at FROM monitors WHERE id=?`, id)
	return scanMonitor(row)
}

// CreateMonitor 新建监控项。
func (s *Store) CreateMonitor(m *model.Monitor) error {
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO monitors(name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at) VALUES(?,?,?,?,?,?,NULL,'',NULL,?,?)`,
		m.Name, m.Target, m.IntervalHour, boolInt(m.Enabled), "pending", "", now, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.Status = "pending"
	m.CreatedAt = now
	m.NextCheckAt = &now
	return nil
}

// UpdateMonitor 更新监控配置。
func (s *Store) UpdateMonitor(m *model.Monitor) error {
	_, err := s.DB.Exec(`UPDATE monitors SET name=?,target=?,interval_hours=?,enabled=?,next_check_at=? WHERE id=?`,
		m.Name, m.Target, m.IntervalHour, boolInt(m.Enabled), nowUTC(), m.ID)
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
	rows, err := s.DB.Query(`SELECT id,name,target,interval_hours,enabled,status,message,expires_at,issuer,last_check_at,next_check_at,created_at
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
	var enabled int
	var expires, lastCheck, nextCheck sql.NullTime
	if err := row.Scan(&m.ID, &m.Name, &m.Target, &m.IntervalHour, &enabled, &m.Status, &m.Message, &expires, &m.Issuer, &lastCheck, &nextCheck, &m.CreatedAt); err != nil {
		return nil, err
	}
	m.Enabled = enabled == 1
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

// AddLog 写入一条日志。
func (s *Store) AddLog(source string, refID int64, level, message string) {
	_, _ = s.DB.Exec(`INSERT INTO logs(source,ref_id,level,message,created_at) VALUES(?,?,?,?,?)`, source, refID, level, message, nowUTC())
}

// AddLogKey 写入一条语言无关的结构化日志，读取时按请求语言渲染。
func (s *Store) AddLogKey(source string, refID int64, level, key string, params map[string]any) {
	s.AddLog(source, refID, level, i18n.Encode(key, params))
}

// ListLogsByRef 查询某个任务或对象的日志。
func (s *Store) ListLogsByRef(source string, refID int64, limit int) ([]model.LogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT id,source,ref_id,level,message,created_at FROM logs WHERE source=? AND ref_id=? ORDER BY created_at DESC, id DESC LIMIT ?`, source, refID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0)
	for rows.Next() {
		var l model.LogEntry
		if err := rows.Scan(&l.ID, &l.Source, &l.RefID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

// ListLogs 分页查询日志。
func (s *Store) ListLogs(source string, limit, offset int) ([]model.LogEntry, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := ""
	args := []any{}
	if source != "" {
		where = " WHERE source=?"
		args = append(args, source)
	}
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query(`SELECT id,source,ref_id,level,message,created_at FROM logs`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := make([]model.LogEntry, 0)
	for rows.Next() {
		var l model.LogEntry
		if err := rows.Scan(&l.ID, &l.Source, &l.RefID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		list = append(list, l)
	}
	return list, total, rows.Err()
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
	rows, err := s.DB.Query(`SELECT substr(created_at,1,10) AS d, COUNT(*) FROM certificates GROUP BY d`)
	if err == nil {
		for rows.Next() {
			var day string
			var count int64
			if err := rows.Scan(&day, &count); err == nil {
				trendMap[day] = count
			}
		}
		rows.Close()
	}
	for i := 13; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
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
