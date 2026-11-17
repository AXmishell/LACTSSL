package store

import (
	"time"

	"lactssl/internal/model"
)

// GetCertificateBySerial 按私有 CA 与序列号查询证书。
func (s *Store) GetCertificateBySerial(caID int64, serial string) (*model.Certificate, error) {
	row := s.DB.QueryRow(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,cert_pem,key_pem,fullchain_pem,serial,not_before,not_after,status,revoked_at,revoke_reason,created_at
		FROM certificates WHERE private_ca_id=? AND serial=?`, caID, serial)
	return s.scanCertificate(row)
}

// ListRevokedCertificates 返回指定 CA 已撤销的证书。
func (s *Store) ListRevokedCertificates(caID int64) ([]model.Certificate, error) {
	rows, err := s.DB.Query(`SELECT id,task_id,domains,primary_domain,issuer,ca,source,private_ca_id,acme_order_id,acme_account_id,'','','',serial,not_before,not_after,status,revoked_at,revoke_reason,created_at
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

// RevokeCertificate 标记证书为已撤销。
func (s *Store) RevokeCertificate(id int64, reason int, revokedAt time.Time) error {
	_, err := s.DB.Exec(`UPDATE certificates SET status='revoked', revoked_at=?, revoke_reason=? WHERE id=?`, revokedAt, reason, id)
	return err
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
