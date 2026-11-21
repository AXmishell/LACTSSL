package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"lactssl/internal/model"
)

const csrRequestColumns = `id,public_id,csr_pem,csr_sha256,identifiers,primary_domain,ca,challenge,dns_account_id,dns_routes,dns_routes_hash,resolved_by,resolved_zone,status,error_code,error_message,retryable,attempts,certificate_id,upstream_order_id,idempotency_key,user_id,api_key_id,created_at,updated_at,expires_at,completed_at`

func scanCSRRequest(row interface{ Scan(...any) error }) (*model.CSRRequest, error) {
	var r model.CSRRequest
	var identifiers, dnsRoutes string
	var retryable int
	var expiresAt, completedAt sql.NullTime
	if err := row.Scan(
		&r.ID, &r.PublicID, &r.CSRPEM, &r.CSRHash, &identifiers, &r.PrimaryDomain,
		&r.CA, &r.Challenge, &r.DNSAccountID, &dnsRoutes, &r.DNSRoutesHash, &r.ResolvedBy, &r.ResolvedZone, &r.Status, &r.ErrorCode, &r.ErrorMessage,
		&retryable, &r.Attempts, &r.CertificateID, &r.UpstreamOrderID, &r.IdempotencyKey,
		&r.UserID, &r.APIKeyID, &r.CreatedAt, &r.UpdatedAt, &expiresAt, &completedAt,
	); err != nil {
		return nil, err
	}
	r.Retryable = retryable == 1
	if expiresAt.Valid {
		r.ExpiresAt = &expiresAt.Time
	}
	if completedAt.Valid {
		r.CompletedAt = &completedAt.Time
	}
	_ = json.Unmarshal([]byte(identifiers), &r.Identifiers)
	_ = json.Unmarshal([]byte(dnsRoutes), &r.DNSRoutes)
	if r.Identifiers == nil {
		r.Identifiers = []string{}
	}
	if r.DNSRoutes == nil {
		r.DNSRoutes = []model.DNSRoute{}
	}
	return &r, nil
}

// CreateCSRRequest 写入一条 CSR 申请。调用方负责生成 PublicID。
func (s *Store) CreateCSRRequest(r *model.CSRRequest) error {
	identifiers, err := json.Marshal(r.Identifiers)
	if err != nil {
		return err
	}
	dnsRoutes, err := json.Marshal(r.DNSRoutes)
	if err != nil {
		return err
	}
	now := nowUTC()
	id, err := s.DB.InsertID(`INSERT INTO certificate_requests(public_id,csr_pem,csr_sha256,identifiers,primary_domain,ca,challenge,dns_account_id,dns_routes,dns_routes_hash,resolved_by,resolved_zone,status,error_code,error_message,retryable,attempts,certificate_id,upstream_order_id,idempotency_key,user_id,api_key_id,created_at,updated_at,expires_at,completed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.PublicID, r.CSRPEM, r.CSRHash, string(identifiers), r.PrimaryDomain, r.CA, r.Challenge,
		r.DNSAccountID, string(dnsRoutes), r.DNSRoutesHash, r.ResolvedBy, r.ResolvedZone,
		r.Status, r.ErrorCode, r.ErrorMessage, boolInt(r.Retryable), r.Attempts, r.CertificateID,
		r.UpstreamOrderID, r.IdempotencyKey, r.UserID, r.APIKeyID, now, now, r.ExpiresAt, r.CompletedAt)
	if err != nil {
		return err
	}
	r.ID = id
	r.CreatedAt, r.UpdatedAt = now, now
	return nil
}

// GetCSRRequest 按内部 ID 查询申请。
func (s *Store) GetCSRRequest(id int64) (*model.CSRRequest, error) {
	row := s.DB.QueryRow(`SELECT `+csrRequestColumns+` FROM certificate_requests WHERE id=?`, id)
	return scanCSRRequest(row)
}

// GetCSRRequestByPublicID 按对外随机 ID 查询申请。
func (s *Store) GetCSRRequestByPublicID(publicID string) (*model.CSRRequest, error) {
	row := s.DB.QueryRow(`SELECT `+csrRequestColumns+` FROM certificate_requests WHERE public_id=?`, publicID)
	return scanCSRRequest(row)
}

// GetCSRRequestByIdempotency 查询同一 API 密钥下指定幂等键的申请。
func (s *Store) GetCSRRequestByIdempotency(apiKeyID int64, key string) (*model.CSRRequest, error) {
	row := s.DB.QueryRow(`SELECT `+csrRequestColumns+` FROM certificate_requests WHERE api_key_id=? AND idempotency_key=? ORDER BY id DESC LIMIT 1`, apiKeyID, key)
	return scanCSRRequest(row)
}

// GetActiveCSRRequestByHash 查询同一调用方、CSR、CA 与 DNS 路由下仍在途的申请。
// 已签发记录不参与去重，允许客户端使用同一公钥在证书到期前重新申请续期；
// DNS 路由摘要也参与去重，避免同一证书换账户后被错误复用。
func (s *Store) GetActiveCSRRequestByHash(hash, ca string, apiKeyID, dnsAccountID int64, dnsRoutesHash string) (*model.CSRRequest, error) {
	row := s.DB.QueryRow(`SELECT `+csrRequestColumns+` FROM certificate_requests
		WHERE csr_sha256=? AND ca=? AND api_key_id=? AND dns_account_id=? AND dns_routes_hash=? AND status IN ('pending','running') ORDER BY id DESC LIMIT 1`,
		hash, ca, apiKeyID, dnsAccountID, dnsRoutesHash)
	return scanCSRRequest(row)
}

// ListCSRRequests 返回申请列表。apiKeyID > 0 时只返回该密钥创建的申请。
func (s *Store) ListCSRRequests(apiKeyID int64, limit int) ([]model.CSRRequest, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	query := `SELECT ` + csrRequestColumns + ` FROM certificate_requests`
	args := []any{}
	if apiKeyID > 0 {
		query += ` WHERE api_key_id=?`
		args = append(args, apiKeyID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.CSRRequest, 0)
	for rows.Next() {
		item, err := scanCSRRequest(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *item)
	}
	return list, rows.Err()
}

// ListPendingCSRRequests 返回等待处理的申请，按创建时间先后排序。
func (s *Store) ListPendingCSRRequests(limit int) ([]model.CSRRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.Query(`SELECT `+csrRequestColumns+` FROM certificate_requests
		WHERE status='pending' AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY id LIMIT ?`, nowUTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.CSRRequest, 0)
	for rows.Next() {
		item, err := scanCSRRequest(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *item)
	}
	return list, rows.Err()
}

// ClaimCSRRequest 原子地把 pending 申请切换为 running。
func (s *Store) ClaimCSRRequest(id int64) (bool, error) {
	res, err := s.DB.Exec(`UPDATE certificate_requests SET status='running', updated_at=? WHERE id=? AND status='pending'`, nowUTC(), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// ResetRunningCSRRequests 在进程重启后把中断的申请重新排队。
func (s *Store) ResetRunningCSRRequests() error {
	_, err := s.DB.Exec(`UPDATE certificate_requests SET status='pending', updated_at=? WHERE status='running'`, nowUTC())
	return err
}

// ExpirePendingCSRRequests 把超过有效期仍未开始的申请标记为 expired。
func (s *Store) ExpirePendingCSRRequests(now time.Time) error {
	_, err := s.DB.Exec(`UPDATE certificate_requests SET status='expired', error_code='csr.expired', error_message='', updated_at=? WHERE status='pending' AND expires_at IS NOT NULL AND expires_at <= ?`, now.UTC(), now.UTC())
	return err
}

// SetCSRRequestFailed 标记申请失败。只允许从 running 状态迁移，避免覆盖已取消记录。
func (s *Store) SetCSRRequestFailed(id int64, code, message string, retryable bool) error {
	_, err := s.DB.Exec(`UPDATE certificate_requests SET status='failed', error_code=?, error_message=?, retryable=?, updated_at=? WHERE id=? AND status='running'`,
		code, message, boolInt(retryable), nowUTC(), id)
	return err
}

// SetCSRRequestCancelled 取消尚未开始的申请。
func (s *Store) SetCSRRequestCancelled(id int64) (bool, error) {
	res, err := s.DB.Exec(`UPDATE certificate_requests SET status='cancelled', updated_at=? WHERE id=? AND status='pending'`, nowUTC(), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// RetryCSRRequest 把可重试的失败申请重新排队。
func (s *Store) RetryCSRRequest(id int64) (bool, error) {
	// 限制自动/手动重试次数，避免持续失败时反复消耗上游 CA 配额。
	res, err := s.DB.Exec(`UPDATE certificate_requests SET status='pending', error_code='', error_message='', retryable=0, attempts=attempts+1, updated_at=? WHERE id=? AND status='failed' AND retryable=1 AND attempts < 5`, nowUTC(), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// CompleteCSRRequest 在同一事务内写入证书并把申请标记为 issued。
// 避免证书已保存但申请状态更新失败时，客户端重试产生重复签发。
func (s *Store) CompleteCSRRequest(id int64, c *model.Certificate) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c.CSRRequestID = id
	if err := s.insertCertificate(tx, c); err != nil {
		return err
	}
	now := nowUTC()
	res, err := tx.Exec(`UPDATE certificate_requests SET status='issued', error_code='', error_message='', retryable=0, certificate_id=?, completed_at=?, updated_at=? WHERE id=? AND status='running'`,
		c.ID, now, now, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// CountActiveCSRRequestsForCA 统计引用指定 CA 的在途申请，供删除 CA 前校验。
func (s *Store) CountActiveCSRRequestsForCA(ca string) (int64, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM certificate_requests WHERE ca=? AND status IN ('pending','running')`, ca).Scan(&count)
	return count, err
}

// CountActiveCSRRequestsForDNSAccount 统计引用指定 DNS 账户的在途申请。
// 多账户 CSR 的主账户字段可能不是被删除的账户，因此需要同时检查路由快照。
func (s *Store) CountActiveCSRRequestsForDNSAccount(id int64) (int64, error) {
	rows, err := s.DB.Query(`SELECT dns_account_id,dns_routes FROM certificate_requests WHERE status IN ('pending','running')`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		var primary int64
		var routesJSON string
		if err := rows.Scan(&primary, &routesJSON); err != nil {
			return 0, err
		}
		if primary == id {
			count++
			continue
		}
		var routes []model.DNSRoute
		if err := json.Unmarshal([]byte(routesJSON), &routes); err != nil {
			continue
		}
		for _, route := range routes {
			if route.DNSAccountID == id {
				count++
				break
			}
		}
	}
	return count, rows.Err()
}

// CleanupCSRRequests 删除超过保留期的终态申请记录。
func (s *Store) CleanupCSRRequests(before time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM certificate_requests WHERE status IN ('issued','failed','cancelled','expired') AND updated_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CSRRequestExists 判断错误是否为唯一约束冲突，便于接口复用已有申请。
func CSRRequestExists(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") || strings.Contains(text, "duplicate key") ||
		strings.Contains(text, "duplicate entry") || strings.Contains(text, "error 1062")
}
