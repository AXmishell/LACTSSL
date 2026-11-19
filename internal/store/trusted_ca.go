package store

import (
	"lactssl/internal/model"
)

// ---------------------------- TLS 信任链 ----------------------------

// ListTrustedCAs 返回全部导入的 CA 证书，新导入的排在前面。
func (s *Store) ListTrustedCAs() ([]model.TrustedCA, error) {
	rows, err := s.DB.Query(`SELECT id,name,subject,issuer,serial,fingerprint,cert_pem,not_before,not_after,created_at
		FROM trusted_cas ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.TrustedCA, 0)
	for rows.Next() {
		var ca model.TrustedCA
		if err := rows.Scan(&ca.ID, &ca.Name, &ca.Subject, &ca.Issuer, &ca.Serial, &ca.Fingerprint,
			&ca.CertPEM, &ca.NotBefore, &ca.NotAfter, &ca.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, ca)
	}
	return list, rows.Err()
}

// GetTrustedCA 查询单张导入的 CA 证书。
func (s *Store) GetTrustedCA(id int64) (*model.TrustedCA, error) {
	var ca model.TrustedCA
	err := s.DB.QueryRow(`SELECT id,name,subject,issuer,serial,fingerprint,cert_pem,not_before,not_after,created_at
		FROM trusted_cas WHERE id=?`, id).
		Scan(&ca.ID, &ca.Name, &ca.Subject, &ca.Issuer, &ca.Serial, &ca.Fingerprint,
			&ca.CertPEM, &ca.NotBefore, &ca.NotAfter, &ca.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &ca, nil
}

// CreateTrustedCAs 在同一事务中写入一批 CA 证书，避免部分导入。
func (s *Store) CreateTrustedCAs(list []*model.TrustedCA) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUTC()
	for _, ca := range list {
		id, err := tx.InsertID(`INSERT INTO trusted_cas(name,subject,issuer,serial,fingerprint,cert_pem,not_before,not_after,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			ca.Name, ca.Subject, ca.Issuer, ca.Serial, ca.Fingerprint, ca.CertPEM, ca.NotBefore, ca.NotAfter, now)
		if err != nil {
			return err
		}
		ca.ID = id
		ca.CreatedAt = now
	}
	return tx.Commit()
}

// DeleteTrustedCA 删除一张导入的 CA 证书。
func (s *Store) DeleteTrustedCA(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM trusted_cas WHERE id=?`, id)
	return err
}
