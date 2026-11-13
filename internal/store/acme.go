package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// ---------------------------- 通用 ACME 上游 ----------------------------

func scanACMECA(row interface{ Scan(...any) error }, withHMAC bool) (*model.ACMECA, error) {
	var ca model.ACMECA
	var eabRequired int
	var hmac string
	var err error
	if withHMAC {
		err = row.Scan(&ca.ID, &ca.PublicID, &ca.Name, &ca.DirectoryURL, &ca.Protocol, &eabRequired, &ca.EABKid, &hmac, &ca.CreatedAt, &ca.UpdatedAt)
	} else {
		err = row.Scan(&ca.ID, &ca.PublicID, &ca.Name, &ca.DirectoryURL, &ca.Protocol, &eabRequired, &ca.EABKid, &ca.CreatedAt, &ca.UpdatedAt)
	}
	if err != nil {
		return nil, err
	}
	ca.EABRequired = eabRequired == 1
	ca.EABHMAC = hmac
	ca.EABHMACSet = hmac != ""
	return &ca, nil
}

// CreateACMECA 保存通用 ACME 上游，HMAC 会先加密。
func (s *Store) CreateACMECA(ca *model.ACMECA) error {
	encrypted, err := s.Cipher.Encrypt(ca.EABHMAC)
	if err != nil {
		return err
	}
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO acme_cas(public_id,name,directory_url,protocol,eab_required,eab_kid,eab_hmac,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		ca.PublicID, ca.Name, ca.DirectoryURL, ca.Protocol, boolInt(ca.EABRequired), ca.EABKid, encrypted, now, now)
	if err != nil {
		return err
	}
	ca.ID, _ = res.LastInsertId()
	ca.CreatedAt, ca.UpdatedAt = now, now
	ca.EABHMACSet = ca.EABHMAC != ""
	return nil
}

// UpdateACMECA 更新通用 ACME 上游。hmac 为空表示保留原值。
func (s *Store) UpdateACMECA(ca *model.ACMECA, hmac string) error {
	now := nowUTC()
	if hmac != "" {
		encrypted, err := s.Cipher.Encrypt(hmac)
		if err != nil {
			return err
		}
		_, err = s.DB.Exec(`UPDATE acme_cas SET name=?,directory_url=?,protocol=?,eab_required=?,eab_kid=?,eab_hmac=?,updated_at=? WHERE id=?`,
			ca.Name, ca.DirectoryURL, ca.Protocol, boolInt(ca.EABRequired), ca.EABKid, encrypted, now, ca.ID)
		return err
	}
	_, err := s.DB.Exec(`UPDATE acme_cas SET name=?,directory_url=?,protocol=?,eab_required=?,eab_kid=?,updated_at=? WHERE id=?`,
		ca.Name, ca.DirectoryURL, ca.Protocol, boolInt(ca.EABRequired), ca.EABKid, now, ca.ID)
	return err
}

// ListACMECAs 返回全部通用 ACME 上游，不包含 HMAC 明文。
func (s *Store) ListACMECAs() ([]model.ACMECA, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,name,directory_url,protocol,eab_required,eab_kid,eab_hmac,created_at,updated_at FROM acme_cas ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.ACMECA, 0)
	for rows.Next() {
		ca, err := scanACMECA(rows, true)
		if err != nil {
			return nil, err
		}
		ca.EABHMAC = ""
		list = append(list, *ca)
	}
	return list, rows.Err()
}

// GetACMECA 按 ID 查询通用 ACME 上游，并解密 HMAC 供签发使用。
func (s *Store) GetACMECA(id int64) (*model.ACMECA, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,name,directory_url,protocol,eab_required,eab_kid,eab_hmac,created_at,updated_at FROM acme_cas WHERE id=?`, id)
	ca, err := scanACMECA(row, true)
	if err != nil {
		return nil, err
	}
	ca.EABHMAC, err = s.Cipher.Decrypt(ca.EABHMAC)
	if err != nil {
		return nil, err
	}
	ca.EABHMACSet = ca.EABHMAC != ""
	return ca, nil
}

// GetACMECAByName 按名称查询通用 ACME 上游，用于校验名称唯一性。
func (s *Store) GetACMECAByName(name string) (*model.ACMECA, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,name,directory_url,protocol,eab_required,eab_kid,created_at,updated_at FROM acme_cas WHERE name=?`, name)
	return scanACMECA(row, false)
}

// DeleteACMECA 删除通用 ACME 上游；仍被任务引用时拒绝删除。
func (s *Store) DeleteACMECA(id int64) error {
	value := "acme-ca:" + strconv.FormatInt(id, 10)
	var tasks int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE ca=?`, value).Scan(&tasks); err != nil {
		return err
	}
	if tasks > 0 {
		return i18n.E("api.error.acme_ca_has_tasks", "count", tasks)
	}
	_, err := s.DB.Exec(`DELETE FROM acme_cas WHERE id=?`, id)
	return err
}

// ---------------------------- ACME 端点 ----------------------------

func scanACMEEndpoint(row interface{ Scan(...any) error }) (*model.ACMEEndpoint, error) {
	var e model.ACMEEndpoint
	var enabled, eabRequired, allowWildcard, allowIP, http01, dns01, tlsALPN, allowPrivate, allowPrivateTLS int
	var allowedDomains, allowedCIDRs, tlsALPNCIDRs, allowedIPCIDRs string
	err := row.Scan(
		&e.ID, &e.PublicID, &e.Name, &e.Slug, &e.CAID,
		&enabled, &eabRequired, &allowedDomains, &allowWildcard, &allowIP, &allowedIPCIDRs,
		&http01, &dns01, &tlsALPN, &allowPrivate, &allowPrivateTLS, &allowedCIDRs, &tlsALPNCIDRs,
		&e.ValidityDays, &e.MaxOrdersPerHour, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(allowedDomains), &e.AllowedDomains)
	_ = json.Unmarshal([]byte(allowedCIDRs), &e.HTTP01AllowedCIDRs)
	_ = json.Unmarshal([]byte(tlsALPNCIDRs), &e.TLSALPN01AllowedCIDRs)
	_ = json.Unmarshal([]byte(allowedIPCIDRs), &e.AllowedIPCIDRs)
	if e.AllowedDomains == nil {
		e.AllowedDomains = []string{}
	}
	if e.HTTP01AllowedCIDRs == nil {
		e.HTTP01AllowedCIDRs = []string{}
	}
	if e.TLSALPN01AllowedCIDRs == nil {
		e.TLSALPN01AllowedCIDRs = []string{}
	}
	if e.AllowedIPCIDRs == nil {
		e.AllowedIPCIDRs = []string{}
	}
	e.Enabled = enabled == 1
	e.EABRequired = eabRequired == 1
	e.AllowWildcard = allowWildcard == 1
	e.AllowIP = allowIP == 1
	e.HTTP01Enabled = http01 == 1
	e.DNS01Enabled = dns01 == 1
	e.TLSALPN01Enabled = tlsALPN == 1
	e.AllowPrivateHTTP01 = allowPrivate == 1
	e.AllowPrivateTLSALPN01 = allowPrivateTLS == 1
	return &e, nil
}

// CreateACMEEndpoint 创建 ACME 端点。
func (s *Store) CreateACMEEndpoint(e *model.ACMEEndpoint) error {
	allowedDomains, _ := json.Marshal(e.AllowedDomains)
	allowedCIDRs, _ := json.Marshal(e.HTTP01AllowedCIDRs)
	tlsALPNCIDRs, _ := json.Marshal(e.TLSALPN01AllowedCIDRs)
	allowedIPCIDRs, _ := json.Marshal(e.AllowedIPCIDRs)
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO acme_endpoints(public_id,name,slug,ca_id,enabled,eab_required,allowed_domains,allow_wildcard,allow_ip,allowed_ip_cidrs,http01_enabled,dns01_enabled,tls_alpn_01_enabled,allow_private_http01,allow_private_tls_alpn_01,http01_allowed_cidrs,tls_alpn_01_allowed_cidrs,validity_days,max_orders_per_hour,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.PublicID, e.Name, e.Slug, e.CAID, boolInt(e.Enabled), boolInt(e.EABRequired),
		string(allowedDomains), boolInt(e.AllowWildcard), boolInt(e.AllowIP), string(allowedIPCIDRs),
		boolInt(e.HTTP01Enabled), boolInt(e.DNS01Enabled),
		boolInt(e.TLSALPN01Enabled), boolInt(e.AllowPrivateHTTP01), boolInt(e.AllowPrivateTLSALPN01),
		string(allowedCIDRs), string(tlsALPNCIDRs), e.ValidityDays, e.MaxOrdersPerHour, now, now)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt, e.UpdatedAt = now, now
	return nil
}

// UpdateACMEEndpoint 更新 ACME 端点配置。
func (s *Store) UpdateACMEEndpoint(e *model.ACMEEndpoint) error {
	allowedDomains, _ := json.Marshal(e.AllowedDomains)
	allowedCIDRs, _ := json.Marshal(e.HTTP01AllowedCIDRs)
	tlsALPNCIDRs, _ := json.Marshal(e.TLSALPN01AllowedCIDRs)
	allowedIPCIDRs, _ := json.Marshal(e.AllowedIPCIDRs)
	_, err := s.DB.Exec(`UPDATE acme_endpoints SET name=?,slug=?,ca_id=?,enabled=?,eab_required=?,allowed_domains=?,allow_wildcard=?,allow_ip=?,allowed_ip_cidrs=?,http01_enabled=?,dns01_enabled=?,tls_alpn_01_enabled=?,allow_private_http01=?,allow_private_tls_alpn_01=?,http01_allowed_cidrs=?,tls_alpn_01_allowed_cidrs=?,validity_days=?,max_orders_per_hour=?,updated_at=? WHERE id=?`,
		e.Name, e.Slug, e.CAID, boolInt(e.Enabled), boolInt(e.EABRequired), string(allowedDomains),
		boolInt(e.AllowWildcard), boolInt(e.AllowIP), string(allowedIPCIDRs),
		boolInt(e.HTTP01Enabled), boolInt(e.DNS01Enabled), boolInt(e.TLSALPN01Enabled),
		boolInt(e.AllowPrivateHTTP01), boolInt(e.AllowPrivateTLSALPN01), string(allowedCIDRs), string(tlsALPNCIDRs),
		e.ValidityDays, e.MaxOrdersPerHour, nowUTC(), e.ID)
	return err
}

// ListACMEEndpoints 返回全部 ACME 端点。
func (s *Store) ListACMEEndpoints() ([]model.ACMEEndpoint, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,name,slug,ca_id,enabled,eab_required,allowed_domains,allow_wildcard,allow_ip,allowed_ip_cidrs,http01_enabled,dns01_enabled,tls_alpn_01_enabled,allow_private_http01,allow_private_tls_alpn_01,http01_allowed_cidrs,tls_alpn_01_allowed_cidrs,validity_days,max_orders_per_hour,created_at,updated_at FROM acme_endpoints ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.ACMEEndpoint, 0)
	for rows.Next() {
		e, err := scanACMEEndpoint(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *e)
	}
	return list, rows.Err()
}

// GetACMEEndpoint 按 ID 查询 ACME 端点。
func (s *Store) GetACMEEndpoint(id int64) (*model.ACMEEndpoint, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,name,slug,ca_id,enabled,eab_required,allowed_domains,allow_wildcard,allow_ip,allowed_ip_cidrs,http01_enabled,dns01_enabled,tls_alpn_01_enabled,allow_private_http01,allow_private_tls_alpn_01,http01_allowed_cidrs,tls_alpn_01_allowed_cidrs,validity_days,max_orders_per_hour,created_at,updated_at FROM acme_endpoints WHERE id=?`, id)
	return scanACMEEndpoint(row)
}

// GetACMEEndpointBySlug 按 URL slug 查询 ACME 端点。
func (s *Store) GetACMEEndpointBySlug(slug string) (*model.ACMEEndpoint, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,name,slug,ca_id,enabled,eab_required,allowed_domains,allow_wildcard,allow_ip,allowed_ip_cidrs,http01_enabled,dns01_enabled,tls_alpn_01_enabled,allow_private_http01,allow_private_tls_alpn_01,http01_allowed_cidrs,tls_alpn_01_allowed_cidrs,validity_days,max_orders_per_hour,created_at,updated_at FROM acme_endpoints WHERE slug=?`, slug)
	return scanACMEEndpoint(row)
}

// DeleteACMEEndpoint 删除 ACME 端点；已有账户时拒绝删除，避免遗留不可管理的账户。
func (s *Store) DeleteACMEEndpoint(id int64) error {
	var accounts int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_accounts WHERE endpoint_id=?`, id).Scan(&accounts); err != nil {
		return err
	}
	if accounts > 0 {
		return i18n.E("api.error.acme_endpoint_has_accounts", "count", accounts)
	}
	var orders int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_orders WHERE endpoint_id=?`, id).Scan(&orders); err != nil {
		return err
	}
	if orders > 0 {
		return i18n.E("api.error.acme_endpoint_has_orders", "count", orders)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM acme_eab_credentials WHERE endpoint_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM acme_endpoints WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------- ACME 账户 ----------------------------

func scanACMEAccount(row interface{ Scan(...any) error }) (*model.ACMEAccount, error) {
	var a model.ACMEAccount
	var contact string
	var lastUsed sql.NullTime
	err := row.Scan(&a.ID, &a.PublicID, &a.EndpointID, &a.Kid, &a.PublicJWK, &a.Thumbprint, &contact, &a.Status, &a.EABID, &lastUsed, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(contact), &a.Contact)
	if a.Contact == nil {
		a.Contact = []string{}
	}
	if lastUsed.Valid {
		a.LastUsedAt = &lastUsed.Time
	}
	return &a, nil
}

// CreateACMEAccount 创建账户。eabID 大于 0 时会在同一事务内消费 EAB 凭据。
func (s *Store) CreateACMEAccount(a *model.ACMEAccount, eabID int64) error {
	contact, _ := json.Marshal(a.Contact)
	now := nowUTC()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`INSERT INTO acme_accounts(public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,NULL,?,?)`,
		a.PublicID, a.EndpointID, a.Kid, a.PublicJWK, a.Thumbprint, string(contact), a.Status, eabID, now, now)
	if err != nil {
		return err
	}
	if eabID > 0 {
		result, err := tx.Exec(`UPDATE acme_eab_credentials SET used_at=? WHERE id=? AND used_at IS NULL`, now, eabID)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return i18n.E("api.error.acme_eab_used")
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	a.CreatedAt, a.UpdatedAt = now, now
	return nil
}

// GetACMEAccountByThumbprint 按端点与 JWK thumbprint 查询账户。
func (s *Store) GetACMEAccountByThumbprint(endpointID int64, thumbprint string) (*model.ACMEAccount, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at FROM acme_accounts WHERE endpoint_id=? AND thumbprint=?`, endpointID, thumbprint)
	return scanACMEAccount(row)
}

// GetACMEAccountByKid 按账户 URL 查询账户。
func (s *Store) GetACMEAccountByKid(kid string) (*model.ACMEAccount, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at FROM acme_accounts WHERE kid=?`, kid)
	return scanACMEAccount(row)
}

// GetACMEAccountByPublicID 按端点和公开 ID 查询账户。
func (s *Store) GetACMEAccountByPublicID(endpointID int64, publicID string) (*model.ACMEAccount, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at FROM acme_accounts WHERE endpoint_id=? AND public_id=?`, endpointID, publicID)
	return scanACMEAccount(row)
}

// TouchACMEAccount 更新账户最近使用时间。
func (s *Store) TouchACMEAccount(id int64) error {
	_, err := s.DB.Exec(`UPDATE acme_accounts SET last_used_at=?,updated_at=? WHERE id=?`, nowUTC(), nowUTC(), id)
	return err
}

// UpdateACMEAccountStatus 更新账户状态。
func (s *Store) UpdateACMEAccountStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE acme_accounts SET status=?,updated_at=? WHERE id=?`, status, nowUTC(), id)
	return err
}

// UpdateACMEAccountContact 更新账户联系地址。
func (s *Store) UpdateACMEAccountContact(id int64, contacts []string) error {
	contact, _ := json.Marshal(contacts)
	_, err := s.DB.Exec(`UPDATE acme_accounts SET contact=?,updated_at=? WHERE id=?`, string(contact), nowUTC(), id)
	return err
}

// ---------------------------- EAB 凭据 ----------------------------

func scanACMEEABCredential(row interface{ Scan(...any) error }, withHMAC bool) (*model.ACMEEABCredential, error) {
	var e model.ACMEEABCredential
	var hmac string
	var usedAt, expiresAt sql.NullTime
	var err error
	if withHMAC {
		err = row.Scan(&e.ID, &e.PublicID, &e.EndpointID, &e.Kid, &e.Label, &hmac, &usedAt, &expiresAt, &e.CreatedAt)
	} else {
		err = row.Scan(&e.ID, &e.PublicID, &e.EndpointID, &e.Kid, &e.Label, &usedAt, &expiresAt, &e.CreatedAt)
	}
	if err != nil {
		return nil, err
	}
	e.HMAC = hmac
	e.HMACSet = hmac != ""
	if usedAt.Valid {
		e.UsedAt = &usedAt.Time
	}
	if expiresAt.Valid {
		e.ExpiresAt = &expiresAt.Time
	}
	return &e, nil
}

// CreateACMEEABCredential 保存 EAB 凭据，HMAC 明文会先加密。
func (s *Store) CreateACMEEABCredential(e *model.ACMEEABCredential) error {
	encrypted, err := s.Cipher.Encrypt(e.HMAC)
	if err != nil {
		return err
	}
	now := nowUTC()
	res, err := s.DB.Exec(`INSERT INTO acme_eab_credentials(public_id,endpoint_id,kid,label,hmac_key,used_at,expires_at,created_at)
		VALUES(?,?,?,?,?,NULL,?,?)`,
		e.PublicID, e.EndpointID, e.Kid, e.Label, encrypted, e.ExpiresAt, now)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt = now
	e.HMACSet = true
	return nil
}

// ListACMEEABCredentials 返回端点下的 EAB 凭据，不包含 HMAC 明文。
func (s *Store) ListACMEEABCredentials(endpointID int64) ([]model.ACMEEABCredential, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,endpoint_id,kid,label,used_at,expires_at,created_at FROM acme_eab_credentials WHERE endpoint_id=? ORDER BY id DESC`, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.ACMEEABCredential, 0)
	for rows.Next() {
		e, err := scanACMEEABCredential(rows, false)
		if err != nil {
			return nil, err
		}
		e.HMACSet = true
		list = append(list, *e)
	}
	return list, rows.Err()
}

// GetACMEEABCredentialByKid 查询 EAB 凭据并解密 HMAC，供协议层校验使用。
func (s *Store) GetACMEEABCredentialByKid(kid string) (*model.ACMEEABCredential, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,label,hmac_key,used_at,expires_at,created_at FROM acme_eab_credentials WHERE kid=?`, kid)
	e, err := scanACMEEABCredential(row, true)
	if err != nil {
		return nil, err
	}
	e.HMAC, err = s.Cipher.Decrypt(e.HMAC)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// DeleteACMEEABCredential 删除 EAB 凭据。
func (s *Store) DeleteACMEEABCredential(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM acme_eab_credentials WHERE id=?`, id)
	return err
}

// GetACMEEABCredential 按 ID 查询 EAB 凭据（不返回 HMAC）。
func (s *Store) GetACMEEABCredential(id int64) (*model.ACMEEABCredential, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,label,used_at,expires_at,created_at FROM acme_eab_credentials WHERE id=?`, id)
	e, err := scanACMEEABCredential(row, false)
	if err != nil {
		return nil, err
	}
	e.HMACSet = true
	return e, nil
}

// IsNotFound 判断错误是否表示记录不存在。
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// ---------------------------- ACME 订单 / 授权 / 挑战 ----------------------------

func scanACMEOrder(row interface{ Scan(...any) error }) (*model.ACMEOrder, error) {
	var o model.ACMEOrder
	var identifiers string
	var expires sql.NullTime
	if err := row.Scan(&o.ID, &o.PublicID, &o.EndpointID, &o.AccountID, &o.Status, &identifiers, &o.Replaces, &expires, &o.CertificateID, &o.Error, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(identifiers), &o.Identifiers)
	if o.Identifiers == nil {
		o.Identifiers = []string{}
	}
	if expires.Valid {
		o.ExpiresAt = &expires.Time
	}
	return &o, nil
}

// CreateACMEOrder 在事务中创建订单、授权和挑战。
func (s *Store) CreateACMEOrder(o *model.ACMEOrder, authzs []*model.ACMEAuthorization) error {
	identifiers, _ := json.Marshal(o.Identifiers)
	now := nowUTC()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO acme_orders(public_id,endpoint_id,account_id,status,identifiers,replaces,expires_at,certificate_id,error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,0,'',?,?)`,
		o.PublicID, o.EndpointID, o.AccountID, o.Status, string(identifiers), o.Replaces, o.ExpiresAt, now, now)
	if err != nil {
		return err
	}
	o.ID, _ = res.LastInsertId()
	o.CreatedAt, o.UpdatedAt = now, now
	for _, authz := range authzs {
		res, err := tx.Exec(`INSERT INTO acme_authorizations(public_id,order_id,identifier,identifier_type,wildcard,status,expires_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			authz.PublicID, o.ID, authz.Identifier, authz.IdentifierType, boolInt(authz.Wildcard), authz.Status, authz.ExpiresAt, now, now)
		if err != nil {
			return err
		}
		authz.ID, _ = res.LastInsertId()
		authz.OrderID = o.ID
		authz.CreatedAt, authz.UpdatedAt = now, now
		for index := range authz.Challenges {
			challenge := &authz.Challenges[index]
			res, err := tx.Exec(`INSERT INTO acme_challenges(public_id,authz_id,type,token,status,validated_at,error,attempts,next_check_at,created_at,updated_at)
				VALUES(?,?,?,?,?,NULL,'',0,NULL,?,?)`,
				challenge.PublicID, authz.ID, challenge.Type, challenge.Token, challenge.Status, now, now)
			if err != nil {
				return err
			}
			challenge.ID, _ = res.LastInsertId()
			challenge.AuthzID = authz.ID
			challenge.CreatedAt, challenge.UpdatedAt = now, now
		}
	}
	return tx.Commit()
}

// GetACMEOrderByPublicID 按端点和公开 ID 查询订单。
func (s *Store) GetACMEOrderByPublicID(endpointID int64, publicID string) (*model.ACMEOrder, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,account_id,status,identifiers,replaces,expires_at,certificate_id,error,created_at,updated_at FROM acme_orders WHERE endpoint_id=? AND public_id=?`, endpointID, publicID)
	return scanACMEOrder(row)
}

// GetACMEOrderByID 按内部 ID 查询订单。
func (s *Store) GetACMEOrderByID(id int64) (*model.ACMEOrder, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,account_id,status,identifiers,replaces,expires_at,certificate_id,error,created_at,updated_at FROM acme_orders WHERE id=?`, id)
	return scanACMEOrder(row)
}

func scanACMEAuthorization(row interface{ Scan(...any) error }) (*model.ACMEAuthorization, error) {
	var a model.ACMEAuthorization
	var wildcard int
	var expires sql.NullTime
	if err := row.Scan(&a.ID, &a.PublicID, &a.OrderID, &a.Identifier, &a.IdentifierType, &wildcard, &a.Status, &expires, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Wildcard = wildcard == 1
	if expires.Valid {
		a.ExpiresAt = &expires.Time
	}
	return &a, nil
}

// GetACMEAuthorizationByPublicID 查询授权。
func (s *Store) GetACMEAuthorizationByPublicID(publicID string) (*model.ACMEAuthorization, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,order_id,identifier,identifier_type,wildcard,status,expires_at,created_at,updated_at FROM acme_authorizations WHERE public_id=?`, publicID)
	return scanACMEAuthorization(row)
}

// GetACMEAuthorizationByID 按内部 ID 查询授权。
func (s *Store) GetACMEAuthorizationByID(id int64) (*model.ACMEAuthorization, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,order_id,identifier,identifier_type,wildcard,status,expires_at,created_at,updated_at FROM acme_authorizations WHERE id=?`, id)
	return scanACMEAuthorization(row)
}

// ListACMEAuthorizations 返回订单下的授权。
func (s *Store) ListACMEAuthorizations(orderID int64) ([]model.ACMEAuthorization, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,order_id,identifier,identifier_type,wildcard,status,expires_at,created_at,updated_at FROM acme_authorizations WHERE order_id=? ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.ACMEAuthorization, 0)
	for rows.Next() {
		a, err := scanACMEAuthorization(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *a)
	}
	return list, rows.Err()
}

func scanACMEChallenge(row interface{ Scan(...any) error }) (*model.ACMEChallenge, error) {
	var c model.ACMEChallenge
	var validated, nextCheck sql.NullTime
	if err := row.Scan(&c.ID, &c.PublicID, &c.AuthzID, &c.Type, &c.Token, &c.Status, &validated, &c.Error, &c.Attempts, &nextCheck, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if validated.Valid {
		c.ValidatedAt = &validated.Time
	}
	if nextCheck.Valid {
		c.NextCheckAt = &nextCheck.Time
	}
	return &c, nil
}

// GetACMEChallengeByPublicID 查询挑战。
func (s *Store) GetACMEChallengeByPublicID(publicID string) (*model.ACMEChallenge, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,authz_id,type,token,status,validated_at,error,attempts,next_check_at,created_at,updated_at FROM acme_challenges WHERE public_id=?`, publicID)
	return scanACMEChallenge(row)
}

// ListACMEChallenges 返回授权下的挑战。
func (s *Store) ListACMEChallenges(authzID int64) ([]model.ACMEChallenge, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,authz_id,type,token,status,validated_at,error,attempts,next_check_at,created_at,updated_at FROM acme_challenges WHERE authz_id=? ORDER BY id`, authzID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]model.ACMEChallenge, 0)
	for rows.Next() {
		c, err := scanACMEChallenge(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *c)
	}
	return list, rows.Err()
}

// UpdateACMEChallengeStatus 更新挑战状态与错误信息。
func (s *Store) UpdateACMEChallengeStatus(id int64, status, errorText string, validatedAt *time.Time) error {
	_, err := s.DB.Exec(`UPDATE acme_challenges SET status=?,error=?,validated_at=?,updated_at=? WHERE id=?`, status, errorText, validatedAt, nowUTC(), id)
	return err
}

// UpdateACMEAuthorizationStatus 更新授权状态。
func (s *Store) UpdateACMEAuthorizationStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE acme_authorizations SET status=?,updated_at=? WHERE id=?`, status, nowUTC(), id)
	return err
}

// UpdateACMEOrderStatus 更新订单状态。
func (s *Store) UpdateACMEOrderStatus(id int64, status, errorText string, certificateID int64) error {
	_, err := s.DB.Exec(`UPDATE acme_orders SET status=?,error=?,certificate_id=?,updated_at=? WHERE id=?`, status, errorText, certificateID, nowUTC(), id)
	return err
}

// CountACMEOrdersSince 统计账户在指定时间之后创建的订单数。
func (s *Store) CountACMEOrdersSince(accountID int64, since time.Time) (int64, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_orders WHERE account_id=? AND created_at>=?`, accountID, since).Scan(&count)
	return count, err
}

// ResetProcessingACMEChallenges 在服务启动时把中断的挑战恢复为 pending。
func (s *Store) ResetProcessingACMEChallenges() {
	_, _ = s.DB.Exec(`UPDATE acme_challenges SET status='pending', updated_at=? WHERE status='processing'`, nowUTC())
}

// GetACMEAccountByID 按内部 ID 查询账户。
func (s *Store) GetACMEAccountByID(id int64) (*model.ACMEAccount, error) {
	row := s.DB.QueryRow(`SELECT id,public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at FROM acme_accounts WHERE id=?`, id)
	return scanACMEAccount(row)
}

// ListACMEAccounts 分页查询 ACME 账户。
func (s *Store) ListACMEAccounts(endpointID int64, limit, offset int) ([]model.ACMEAccount, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := ""
	args := []any{}
	if endpointID > 0 {
		where = " WHERE endpoint_id=?"
		args = append(args, endpointID)
	}
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_accounts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query(`SELECT id,public_id,endpoint_id,kid,public_jwk,thumbprint,contact,status,eab_id,last_used_at,created_at,updated_at FROM acme_accounts`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := make([]model.ACMEAccount, 0)
	for rows.Next() {
		account, err := scanACMEAccount(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, *account)
	}
	return list, total, rows.Err()
}

// ListACMEOrders 分页查询 ACME 订单。
func (s *Store) ListACMEOrders(endpointID int64, status string, limit, offset int) ([]model.ACMEOrder, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	conditions := make([]string, 0, 2)
	args := []any{}
	if endpointID > 0 {
		conditions = append(conditions, "endpoint_id=?")
		args = append(args, endpointID)
	}
	if status = strings.TrimSpace(status); status != "" {
		conditions = append(conditions, "status=?")
		args = append(args, status)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_orders`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query(`SELECT id,public_id,endpoint_id,account_id,status,identifiers,replaces,expires_at,certificate_id,error,created_at,updated_at FROM acme_orders`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := make([]model.ACMEOrder, 0)
	for rows.Next() {
		order, err := scanACMEOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, *order)
	}
	return list, total, rows.Err()
}

// ACMECleanupFilter 描述清理 ACME 诊断记录时使用的筛选条件。
type ACMECleanupFilter struct {
	// Orders 表示清理订单，Accounts 表示清理账户；两者都为 true 时一并清理。
	Orders   bool
	Accounts bool
	// Scope 为 stale 时只清理失效订单（invalid 或已过期）和无进行中订单的账户；
	// 为 all 时不按状态过滤。
	Scope      string
	EndpointID int64
	// OlderThanDays 仅清理创建时间早于该天数的订单，0 表示不限。
	OlderThanDays int
	// InactiveDays 仅清理最近活动时间早于该天数的账户，0 表示不限。
	InactiveDays int
}

// ACMECleanupResult 返回清理订单与账户的数量。
type ACMECleanupResult struct {
	Orders   int64 `json:"orders"`
	Accounts int64 `json:"accounts"`
}

// orderCondition 生成待删除订单的筛选条件。
func (f ACMECleanupFilter) orderCondition(now time.Time) (string, []any) {
	now = now.UTC()
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if f.Orders {
		parts := make([]string, 0, 3)
		if f.EndpointID > 0 {
			parts = append(parts, "o.endpoint_id=?")
			args = append(args, f.EndpointID)
		}
		if f.Scope != "all" {
			parts = append(parts, "(o.status='invalid' OR (o.expires_at IS NOT NULL AND o.expires_at < ?))")
			args = append(args, now)
		}
		if f.OlderThanDays > 0 {
			parts = append(parts, "o.created_at < ?")
			args = append(args, now.AddDate(0, 0, -f.OlderThanDays))
		}
		if len(parts) == 0 {
			parts = append(parts, "1=1")
		}
		conditions = append(conditions, "("+strings.Join(parts, " AND ")+")")
	}
	if f.Accounts {
		accountWhere, accountArgs := f.accountCondition(now)
		conditions = append(conditions, "o.account_id IN (SELECT a.id FROM acme_accounts a WHERE "+accountWhere+")")
		args = append(args, accountArgs...)
	}
	if len(conditions) == 0 {
		return "0=1", nil
	}
	return strings.Join(conditions, " OR "), args
}

// accountCondition 生成待删除账户的筛选条件。
func (f ACMECleanupFilter) accountCondition(now time.Time) (string, []any) {
	now = now.UTC()
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if f.EndpointID > 0 {
		conditions = append(conditions, "a.endpoint_id=?")
		args = append(args, f.EndpointID)
	}
	if f.Scope != "all" {
		// 未过期且进行中的订单可能仍需要账户完成验证，保留对应账户。
		conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM acme_orders ao WHERE ao.account_id=a.id AND ao.status IN ('pending','ready','processing') AND (ao.expires_at IS NULL OR ao.expires_at >= ?))")
		args = append(args, now)
	}
	if f.InactiveDays > 0 {
		conditions = append(conditions, "COALESCE(a.last_used_at,a.created_at) < ?")
		args = append(args, now.AddDate(0, 0, -f.InactiveDays))
	}
	if len(conditions) == 0 {
		return "1=1", nil
	}
	return strings.Join(conditions, " AND "), args
}

// CountACMECleanup 返回符合清理条件的订单与账户数量，不实际删除。
func (s *Store) CountACMECleanup(filter ACMECleanupFilter, now time.Time) (ACMECleanupResult, error) {
	var result ACMECleanupResult
	orderWhere, orderArgs := filter.orderCondition(now)
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_orders o WHERE `+orderWhere, orderArgs...).Scan(&result.Orders); err != nil {
		return result, err
	}
	if !filter.Accounts {
		return result, nil
	}
	accountWhere, accountArgs := filter.accountCondition(now)
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_accounts a WHERE `+accountWhere, accountArgs...).Scan(&result.Accounts); err != nil {
		return result, err
	}
	return result, nil
}

// CleanupACME 按条件删除 ACME 订单、授权、挑战与账户。
func (s *Store) CleanupACME(filter ACMECleanupFilter, now time.Time) (ACMECleanupResult, error) {
	var result ACMECleanupResult
	orderWhere, orderArgs := filter.orderCondition(now)
	if orderWhere != "0=1" {
		tx, err := s.DB.Begin()
		if err != nil {
			return result, err
		}
		defer func() { _ = tx.Rollback() }()
		orderSubquery := `SELECT o.id FROM acme_orders o WHERE ` + orderWhere
		if _, err := tx.Exec(`DELETE FROM acme_challenges WHERE authz_id IN (SELECT az.id FROM acme_authorizations az WHERE az.order_id IN (`+orderSubquery+`))`, orderArgs...); err != nil {
			return result, err
		}
		if _, err := tx.Exec(`DELETE FROM acme_authorizations WHERE order_id IN (`+orderSubquery+`)`, orderArgs...); err != nil {
			return result, err
		}
		res, err := tx.Exec(`DELETE FROM acme_orders WHERE id IN (`+orderSubquery+`)`, orderArgs...)
		if err != nil {
			return result, err
		}
		result.Orders, _ = res.RowsAffected()
		if filter.Accounts {
			accountWhere, accountArgs := filter.accountCondition(now)
			res, err := tx.Exec(`DELETE FROM acme_accounts WHERE id IN (SELECT a.id FROM acme_accounts a WHERE `+accountWhere+`)`, accountArgs...)
			if err != nil {
				return result, err
			}
			result.Accounts, _ = res.RowsAffected()
		}
		return result, tx.Commit()
	}
	if !filter.Accounts {
		return result, nil
	}
	accountWhere, accountArgs := filter.accountCondition(now)
	res, err := s.DB.Exec(`DELETE FROM acme_accounts WHERE id IN (SELECT a.id FROM acme_accounts a WHERE `+accountWhere+`)`, accountArgs...)
	if err != nil {
		return result, err
	}
	result.Accounts, _ = res.RowsAffected()
	return result, nil
}

// UpdateACMEAccountKey 更新账户公钥与 thumbprint。
func (s *Store) UpdateACMEAccountKey(id int64, publicJWK, thumbprint string) error {
	_, err := s.DB.Exec(`UPDATE acme_accounts SET public_jwk=?,thumbprint=?,updated_at=? WHERE id=?`, publicJWK, thumbprint, nowUTC(), id)
	return err
}

// HasActiveACMEOrderReplaces 判断某个证书是否已被同端点下未失效的订单标记为替换目标。
func (s *Store) HasActiveACMEOrderReplaces(endpointID int64, replaces string) (bool, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM acme_orders WHERE endpoint_id=? AND replaces=? AND status!='invalid'`, endpointID, replaces).Scan(&count)
	return count > 0, err
}
