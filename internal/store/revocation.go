package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"lactssl/internal/model"
)

// RevocationEntry 是一条独立保存的吊销记录。
// 它不随证书行删除，CRL/OCSP 始终以这里为准。
type RevocationEntry struct {
	Serial    string
	RevokedAt time.Time
	Reason    int
}

// GetCertificateBySerial 按私有 CA 与序列号查询证书。
func (s *Store) GetCertificateBySerial(caID int64, serial string) (*model.Certificate, error) {
	row := s.DB.QueryRow(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END
		FROM certificates WHERE private_ca_id=? AND serial=?`, caID, serial)
	return s.scanCertificate(row)
}

// ListRevokedCertificates 返回指定 CA 已撤销的证书。
// 该接口仅用于兼容旧调用；CRL 应使用 ListRevocations，避免证书行删除后丢失吊销状态。
func (s *Store) ListRevokedCertificates(caID int64) ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,csr_request_id,'','','',serial,not_before,not_after,status,revoked_at,revoke_reason,created_at,CASE WHEN key_pem<>'' THEN 1 ELSE 0 END
		FROM certificates WHERE private_ca_id=? AND revoked_at IS NOT NULL ORDER BY revoked_at`, caID)
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

// RevokeCertificate 标记证书为已撤销，并写入独立的吊销记录。
// 两步在同一事务中完成；证书后续被删除时吊销记录仍然保留。
func (s *Store) RevokeCertificate(id int64, reason int, revokedAt time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var caID int64
	var serial string
	if err := tx.QueryRow(`SELECT private_ca_id, serial FROM certificates WHERE id=?`, id).Scan(&caID, &serial); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE certificates SET status='revoked', revoked_at=?, revoke_reason=? WHERE id=?`, revokedAt, reason, id); err != nil {
		return err
	}
	if caID > 0 && strings.TrimSpace(serial) != "" {
		if _, err := tx.Exec(`INSERT INTO revocations(ca_id, serial, revoked_at, reason, created_at) VALUES(?,?,?,?,?)
			ON CONFLICT(ca_id, serial) DO NOTHING`,
			caID, strings.ToUpper(strings.TrimSpace(serial)), revokedAt, reason, nowUTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetRevocation 查询某个 CA 下序列号的吊销记录。
func (s *Store) GetRevocation(caID int64, serial string) (RevocationEntry, bool, error) {
	row := s.DB.QueryRow(`SELECT serial, revoked_at, reason FROM revocations WHERE ca_id=? AND serial=?`,
		caID, strings.ToUpper(strings.TrimSpace(serial)))
	var entry RevocationEntry
	if err := row.Scan(&entry.Serial, &entry.RevokedAt, &entry.Reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RevocationEntry{}, false, nil
		}
		return RevocationEntry{}, false, err
	}
	return entry, true, nil
}

// ListRevocations 返回指定 CA 的全部吊销记录，供 CRL 生成使用。
func (s *Store) ListRevocations(caID int64) ([]RevocationEntry, error) {
	rows, err := s.DB.Query(`SELECT serial, revoked_at, reason FROM revocations WHERE ca_id=? ORDER BY revoked_at`, caID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]RevocationEntry, 0)
	for rows.Next() {
		var entry RevocationEntry
		if err := rows.Scan(&entry.Serial, &entry.RevokedAt, &entry.Reason); err != nil {
			return nil, err
		}
		list = append(list, entry)
	}
	return list, rows.Err()
}

// NextCRLNumber 以事务方式获取指定 CA 的下一个 CRL Number。
func (s *Store) NextCRLNumber(caID int64) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE private_cas SET crl_number=crl_number+1 WHERE id=?`, caID); err != nil {
		return 0, err
	}
	var number int64
	if err := tx.QueryRow(`SELECT crl_number FROM private_cas WHERE id=?`, caID).Scan(&number); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return number, nil
}
