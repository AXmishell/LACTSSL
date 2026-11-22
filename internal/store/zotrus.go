package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"lactssl/internal/model"
)

// CreateZotrusOrder 保存一条等待手动 DNS 验证的零信订单。
func (s *Store) CreateZotrusOrder(order *model.ZotrusOrder) error {
	if order == nil {
		return errors.New("zotrus order is nil")
	}
	if order.TaskID <= 0 {
		return errors.New("zotrus order task id is required")
	}
	domainsJSON, err := json.Marshal(order.Domains)
	if err != nil {
		return err
	}
	challengesJSON, err := json.Marshal(order.Challenges)
	if err != nil {
		return err
	}
	encryptedKey, err := s.Cipher.Encrypt(order.KeyPEM)
	if err != nil {
		return err
	}
	if order.Status == "" {
		order.Status = model.ZotrusOrderPending
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO zotrus_orders(
		task_id,domains,key_type,order_url,account_uri,finalize_url,certificate_url,key_pem,csr_der,challenges,status,error,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		order.TaskID, string(domainsJSON), order.KeyType, order.OrderURL, order.AccountURI, order.FinalizeURL,
		order.CertificateURL, encryptedKey, order.CSRDER, string(challengesJSON), order.Status,
		order.Error, now, now,
	)
	if err != nil {
		return err
	}
	order.ID = id
	order.CreatedAt = now
	order.UpdatedAt = now
	return nil
}

// GetZotrusOrderByTask 返回任务最近一条零信订单；不存在时返回 nil, nil。
func (s *Store) GetZotrusOrderByTask(taskID int64) (*model.ZotrusOrder, error) {
	if taskID <= 0 {
		return nil, nil
	}
	row := s.DB.QueryRow(`SELECT id,task_id,domains,key_type,order_url,account_uri,finalize_url,certificate_url,key_pem,csr_der,challenges,status,error,created_at,updated_at
		FROM zotrus_orders WHERE task_id=? ORDER BY id DESC LIMIT 1`, taskID)
	order, err := s.scanZotrusOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return order, err
}

// GetZotrusOrderByID 按主键返回零信订单。
func (s *Store) GetZotrusOrderByID(id int64) (*model.ZotrusOrder, error) {
	row := s.DB.QueryRow(`SELECT id,task_id,domains,key_type,order_url,account_uri,finalize_url,certificate_url,key_pem,csr_der,challenges,status,error,created_at,updated_at
		FROM zotrus_orders WHERE id=?`, id)
	return s.scanZotrusOrder(row)
}

func (s *Store) scanZotrusOrder(row interface{ Scan(...any) error }) (*model.ZotrusOrder, error) {
	var (
		order          model.ZotrusOrder
		domainsJSON    string
		challengesJSON string
		encryptedKey   string
	)
	if err := row.Scan(
		&order.ID, &order.TaskID, &domainsJSON, &order.KeyType, &order.OrderURL, &order.AccountURI,
		&order.FinalizeURL, &order.CertificateURL, &encryptedKey, &order.CSRDER,
		&challengesJSON, &order.Status, &order.Error, &order.CreatedAt, &order.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if domainsJSON != "" {
		_ = json.Unmarshal([]byte(domainsJSON), &order.Domains)
	}
	if challengesJSON != "" {
		_ = json.Unmarshal([]byte(challengesJSON), &order.Challenges)
	}
	if encryptedKey != "" {
		decrypted, err := s.Cipher.Decrypt(encryptedKey)
		if err != nil {
			return nil, err
		}
		order.KeyPEM = decrypted
	}
	return &order, nil
}

// UpdateZotrusOrderStatus 更新订单状态、证书 URL 与错误信息。
func (s *Store) UpdateZotrusOrderStatus(id int64, status, certificateURL, message string) error {
	query := `UPDATE zotrus_orders SET status=?,certificate_url=?,error=?,updated_at=? WHERE id=?`
	args := []any{status, certificateURL, message, nowUTC(), id}
	if status == model.ZotrusOrderFailed {
		// 失败订单不再重试，及时清掉私钥与 CSR，减少密钥驻留。
		query = `UPDATE zotrus_orders SET status=?,certificate_url=?,error=?,key_pem='',csr_der='',updated_at=? WHERE id=?`
	}
	res, err := s.DB.Exec(query, args...)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateZotrusOrderChallenges 更新每条验证记录的状态。
func (s *Store) UpdateZotrusOrderChallenges(id int64, challenges []model.ZotrusChallenge, status, message string) error {
	payload, err := json.Marshal(challenges)
	if err != nil {
		return err
	}
	query := `UPDATE zotrus_orders SET challenges=?,status=?,error=?,updated_at=? WHERE id=?`
	args := []any{string(payload), status, message, nowUTC(), id}
	if status == model.ZotrusOrderFailed {
		query = `UPDATE zotrus_orders SET challenges=?,status=?,error=?,key_pem='',csr_der='',updated_at=? WHERE id=?`
	}
	res, err := s.DB.Exec(query, args...)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteZotrusOrder 删除指定订单。
func (s *Store) DeleteZotrusOrder(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM zotrus_orders WHERE id=?`, id)
	return err
}

// DeleteZotrusOrderByTask 删除任务下的零信订单，用于任务删除或重新生成记录。
func (s *Store) DeleteZotrusOrderByTask(taskID int64) error {
	if taskID <= 0 {
		return nil
	}
	_, err := s.DB.Exec(`DELETE FROM zotrus_orders WHERE task_id=?`, taskID)
	return err
}
