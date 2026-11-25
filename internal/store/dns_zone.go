package store

import (
	"database/sql"
	"strings"

	"lactssl/internal/model"
)

const dnsZoneBindingColumns = `id,dns_account_id,zone,match_mode,priority,source,enabled,created_at,updated_at`

// ListDNSZoneBindings 返回全部绑定，按 zone 长度与优先级排序。
// 服务端解析时再按最长后缀匹配，这里只保证结果稳定。
func (s *Store) ListDNSZoneBindings() ([]model.DNSZoneBinding, error) {
	return queryRows(s.DB, `SELECT `+dnsZoneBindingColumns+` FROM dns_zone_bindings ORDER BY zone ASC, priority DESC, id ASC`, scanDNSZoneBinding)
}

// ListEnabledDNSZoneBindings 返回参与自动解析的绑定。
func (s *Store) ListEnabledDNSZoneBindings() ([]model.DNSZoneBinding, error) {
	return queryRows(s.DB, `SELECT `+dnsZoneBindingColumns+` FROM dns_zone_bindings WHERE enabled=1 ORDER BY zone ASC, priority DESC, id ASC`, scanDNSZoneBinding)
}

// ListDNSZoneBindingsByAccount 返回指定账户的全部绑定。
func (s *Store) ListDNSZoneBindingsByAccount(accountID int64) ([]model.DNSZoneBinding, error) {
	return queryRows(s.DB, `SELECT `+dnsZoneBindingColumns+` FROM dns_zone_bindings WHERE dns_account_id=? ORDER BY zone ASC, priority DESC, id ASC`, scanDNSZoneBinding, accountID)
}

func scanDNSZoneBinding(row interface{ Scan(...any) error }) (*model.DNSZoneBinding, error) {
	var b model.DNSZoneBinding
	var enabled int
	if err := row.Scan(&b.ID, &b.DNSAccountID, &b.Zone, &b.MatchMode, &b.Priority, &b.Source, &enabled, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	b.Enabled = enabled == 1
	return &b, nil
}

// ReplaceDNSZoneBindings 原子替换指定账户的全部绑定。
// 同一 zone 只允许属于一个启用账户；调用方负责先做基础格式校验。
func (s *Store) ReplaceDNSZoneBindings(accountID int64, bindings []model.DNSZoneBinding) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, binding := range bindings {
		if !binding.Enabled {
			continue
		}
		var other int64
		err := tx.QueryRow(`SELECT dns_account_id FROM dns_zone_bindings WHERE zone=? AND enabled=1 AND dns_account_id<>? LIMIT 1`,
			binding.Zone, accountID).Scan(&other)
		if err == nil {
			return &DNSZoneConflictError{Zone: binding.Zone, AccountID: other}
		}
		if err != sql.ErrNoRows {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM dns_zone_bindings WHERE dns_account_id=?`, accountID); err != nil {
		return err
	}
	now := nowUTC()
	for _, binding := range bindings {
		if _, err := tx.InsertID(`INSERT INTO dns_zone_bindings(dns_account_id,zone,match_mode,priority,source,enabled,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)`,
			accountID, binding.Zone, binding.MatchMode, binding.Priority, binding.Source, boolInt(binding.Enabled), now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertDNSZoneBinding 新增或更新单个绑定，用于探测回填。
// 同一账户 + zone 已存在时更新 match_mode/priority/source/enabled。
func (s *Store) UpsertDNSZoneBinding(accountID int64, binding model.DNSZoneBinding) (*model.DNSZoneBinding, error) {
	var other int64
	err := s.DB.QueryRow(`SELECT dns_account_id FROM dns_zone_bindings WHERE zone=? AND enabled=1 AND dns_account_id<>? LIMIT 1`,
		binding.Zone, accountID).Scan(&other)
	if err == nil {
		return nil, &DNSZoneConflictError{Zone: binding.Zone, AccountID: other}
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	now := nowUTC()
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM dns_zone_bindings WHERE dns_account_id=? AND zone=? LIMIT 1`, accountID, binding.Zone).Scan(&id)
	switch {
	case err == nil:
		if _, err := s.DB.Exec(`UPDATE dns_zone_bindings SET match_mode=?,priority=?,source=?,enabled=?,updated_at=? WHERE id=?`,
			binding.MatchMode, binding.Priority, binding.Source, boolInt(binding.Enabled), now, id); err != nil {
			return nil, err
		}
	case err == sql.ErrNoRows:
		id, err = s.DB.InsertID(`INSERT INTO dns_zone_bindings(dns_account_id,zone,match_mode,priority,source,enabled,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)`,
			accountID, binding.Zone, binding.MatchMode, binding.Priority, binding.Source, boolInt(binding.Enabled), now, now)
		if err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s.GetDNSZoneBinding(id)
}

// GetDNSZoneBinding 按 ID 查询绑定。
func (s *Store) GetDNSZoneBinding(id int64) (*model.DNSZoneBinding, error) {
	row := s.DB.QueryRow(`SELECT `+dnsZoneBindingColumns+` FROM dns_zone_bindings WHERE id=?`, id)
	return scanDNSZoneBinding(row)
}

// DeleteDNSZoneBindingsByAccount 删除账户的全部绑定，供删除账户时调用。
func (s *Store) DeleteDNSZoneBindingsByAccount(accountID int64) error {
	_, err := s.DB.Exec(`DELETE FROM dns_zone_bindings WHERE dns_account_id=?`, accountID)
	return err
}

// DNSZoneConflictError 表示同一 zone 已被另一个启用账户占用。
type DNSZoneConflictError struct {
	Zone      string
	AccountID int64
}

func (e *DNSZoneConflictError) Error() string {
	return "dns zone " + strings.TrimSpace(e.Zone) + " is already bound to another account"
}
