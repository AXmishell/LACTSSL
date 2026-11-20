package service

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

const (
	// maxCSRSize 限制 CSR 请求体，避免超大扩展占用解析内存。
	maxCSRSize = 16 * 1024
	// maxCSRNames 与主流公共 CA 的 SAN 数量上限保持一致。
	maxCSRNames = 100
)

var oidBasicConstraints = asn1.ObjectIdentifier{2, 5, 29, 19}

// CSRInfo 是解析并校验后的 CSR。
type CSRInfo struct {
	CSR           *x509.CertificateRequest
	DER           []byte
	PEM           string
	SHA256        string
	DNSNames      []string
	PrimaryDomain string
}

// CSRACMERequest 描述一次使用客户端 CSR 向权威 ACME CA 签发的请求。
type CSRACMERequest struct {
	CSR            *x509.CertificateRequest
	CA             string
	DNSAccount     *model.DNSAccount
	Email          string
	EABKid         string
	EABHMAC        string
	PreferredChain string
	Profile        string
}

// ParseCSRInfo 解析 PEM 或 base64 DER 格式的 CSR，并执行公共 CA 签发前的
// 严格校验。服务端没有私钥，无法修正 CSR，因此不合规的 CSR 必须直接拒绝。
func ParseCSRInfo(value string) (*CSRInfo, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, i18n.E("service.csr.required")
	}
	if len(value) > maxCSRSize {
		return nil, i18n.E("service.csr.too_large")
	}
	der, err := decodeCSR(value)
	if err != nil {
		return nil, err
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, i18n.E("service.csr.parse_failed", "error", err.Error())
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, i18n.E("service.csr.signature_invalid", "error", err.Error())
	}
	if err := validateCSRPublicKey(csr.PublicKey); err != nil {
		return nil, err
	}
	if err := validateCSRSignatureAlgorithm(csr); err != nil {
		return nil, err
	}
	if err := validateCSRBasicConstraints(csr); err != nil {
		return nil, err
	}
	if len(csr.EmailAddresses) > 0 || len(csr.URIs) > 0 {
		return nil, i18n.E("service.csr.extra_identifiers_unsupported")
	}
	if len(csr.IPAddresses) > 0 {
		return nil, i18n.E("service.csr.ip_unsupported")
	}
	if len(csr.DNSNames) == 0 {
		return nil, i18n.E("service.csr.dns_required")
	}
	if len(csr.DNSNames) > maxCSRNames {
		return nil, i18n.E("service.csr.dns_too_many", "max", maxCSRNames)
	}

	dnsNames := make([]string, 0, len(csr.DNSNames))
	seen := make(map[string]bool, len(csr.DNSNames))
	for _, raw := range csr.DNSNames {
		name := strings.TrimSpace(raw)
		// CSR 已由客户端签名，服务端无法改写。这里要求域名已经是规范形式，
		// 否则 lego 会把原始值直接提交给上游 CA，导致难以解释的拒绝。
		if name != strings.ToLower(name) || strings.HasSuffix(name, ".") {
			return nil, i18n.E("service.csr.dns_not_canonical", "domain", raw)
		}
		if !validDNSName(name) {
			return nil, i18n.E("service.csr.dns_invalid", "domain", raw)
		}
		if isInternalDNSName(name) {
			return nil, i18n.E("service.csr.dns_internal", "domain", raw)
		}
		if seen[name] {
			return nil, i18n.E("service.csr.dns_duplicate", "domain", name)
		}
		seen[name] = true
		dnsNames = append(dnsNames, name)
	}

	cn := strings.TrimSpace(csr.Subject.CommonName)
	if cn != "" {
		if len(cn) > 64 {
			return nil, i18n.E("service.csr.cn_invalid", "domain", cn)
		}
		if cn != strings.ToLower(cn) || strings.HasSuffix(cn, ".") || !validDNSName(cn) {
			return nil, i18n.E("service.csr.cn_invalid", "domain", cn)
		}
		if isInternalDNSName(cn) {
			return nil, i18n.E("service.csr.dns_internal", "domain", cn)
		}
		if !seen[cn] {
			return nil, i18n.E("service.csr.cn_not_in_san", "domain", cn)
		}
	}

	primary := dnsNames[0]
	if cn != "" {
		primary = cn
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr.Raw}))
	sum := sha256.Sum256(csr.Raw)
	return &CSRInfo{
		CSR:           csr,
		DER:           csr.Raw,
		PEM:           pemText,
		SHA256:        hex.EncodeToString(sum[:]),
		DNSNames:      dnsNames,
		PrimaryDomain: primary,
	}, nil
}

func decodeCSR(value string) ([]byte, error) {
	rest := []byte(value)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		switch strings.ToUpper(strings.TrimSpace(block.Type)) {
		case "CERTIFICATE REQUEST", "NEW CERTIFICATE REQUEST":
			return block.Bytes, nil
		}
		rest = next
	}
	compact := strings.Join(strings.Fields(value), "")
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if der, err := encoding.DecodeString(compact); err == nil && len(der) > 0 {
			return der, nil
		}
	}
	return nil, i18n.E("service.csr.decode_failed")
}

func validateCSRSignatureAlgorithm(csr *x509.CertificateRequest) error {
	switch csr.SignatureAlgorithm {
	case x509.MD2WithRSA, x509.MD5WithRSA, x509.SHA1WithRSA,
		x509.DSAWithSHA1, x509.DSAWithSHA256, x509.ECDSAWithSHA1:
		return i18n.E("service.csr.signature_weak", "algorithm", csr.SignatureAlgorithm.String())
	}
	return nil
}

func validateCSRBasicConstraints(csr *x509.CertificateRequest) error {
	for _, extension := range csr.Extensions {
		if !extension.Id.Equal(oidBasicConstraints) {
			continue
		}
		var constraints struct {
			IsCA       bool `asn1:"optional"`
			MaxPathLen int  `asn1:"optional,default:-1"`
		}
		if _, err := asn1.Unmarshal(extension.Value, &constraints); err != nil {
			return i18n.E("service.csr.basic_constraints_invalid", "error", err.Error())
		}
		if constraints.IsCA {
			return i18n.E("service.csr.ca_constraint")
		}
	}
	return nil
}

func isInternalDNSName(name string) bool {
	base := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "*.")
	if base == "" || base == "localhost" {
		return true
	}
	if !strings.Contains(base, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".internal", ".localhost", ".home", ".lan", ".test", ".example"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// ValidateCSRDomains 校验 CSR 域名是否在允许列表中。
// 允许列表为空时默认拒绝（fail-closed）；只有显式开启 allowAll 才放行全部域名。
// example.com 匹配该域及其子域，*.example.com 匹配子域，也允许为该域申请通配符证书。
func ValidateCSRDomains(domains, allowed []string, allowAll bool) error {
	if len(allowed) == 0 {
		if allowAll {
			return nil
		}
		return i18n.E("service.csr.allowed_domains_required")
	}
	for _, domain := range domains {
		ok := false
		for _, pattern := range allowed {
			pattern = strings.ToLower(strings.TrimSpace(pattern))
			if pattern == "" {
				continue
			}
			if domain == pattern {
				ok = true
				break
			}
			if strings.HasPrefix(pattern, "*.") {
				base := strings.TrimPrefix(pattern, "*")
				if strings.HasSuffix(domain, base) {
					ok = true
					break
				}
			}
			// 裸域允许列表覆盖其子域，也允许申请该域的通配符证书。
			base := strings.TrimPrefix(pattern, "*.")
			if strings.HasSuffix(domain, "."+base) {
				ok = true
				break
			}
			if strings.HasPrefix(domain, "*.") && strings.TrimPrefix(domain, "*.") == base {
				ok = true
				break
			}
		}
		if !ok {
			return i18n.E("service.csr.domain_not_allowed", "domain", domain)
		}
	}
	return nil
}

// ParseAllowedDomains 解析设置中的逗号/空白分隔域名列表。
func ParseAllowedDomains(value string) []string {
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		item = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(item, ".")))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

// ObtainForCSR 使用客户端 CSR 向权威 ACME CA 申请证书。CSR 中的公钥由客户端
// 持有，服务端只完成域名验证和 finalize，不生成也不保存私钥。
func (m *Manager) ObtainForCSR(req CSRACMERequest) (*ACMEResult, error) {
	if req.CSR == nil {
		return nil, i18n.E("service.csr.required")
	}
	ca, err := m.ResolveCA(req.CA)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(ca.Protocol, "v1") {
		return nil, i18n.E("service.csr.v1_unsupported")
	}
	if req.DNSAccount == nil {
		return nil, i18n.E("service.csr.dns_account_required")
	}
	provider, undo, err := newDNSProvider(req.DNSAccount.Provider, req.DNSAccount.Config)
	if err != nil {
		return nil, err
	}
	defer undo()

	client, _, err := m.newACMEClient(ca, req.Email, certcrypto.EC256, provider, req.EABKid, req.EABHMAC)
	if err != nil {
		return nil, err
	}

	resource, err := client.Certificate.ObtainForCSR(certificate.ObtainForCSRRequest{
		CSR:            req.CSR,
		Bundle:         true,
		PreferredChain: req.PreferredChain,
		Profile:        req.Profile,
	})
	if err != nil {
		return nil, i18n.E("service.csr.issue_failed", "error", SanitizeErrorText(err.Error()))
	}

	domains := certcrypto.ExtractDomainsCSR(req.CSR)
	leaf, err := pickLeaf(resource.Certificate, domains)
	if err != nil {
		return nil, err
	}
	if err := validateCSRResult(req.CSR, leaf); err != nil {
		return nil, err
	}

	fullchain, err := BuildFullchainPEM(leaf, resource.Certificate, resource.IssuerCertificate)
	if err != nil {
		return nil, i18n.Wrap("service.acme.chain_parse_failed", err, "error", err.Error())
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	return &ACMEResult{
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})),
		KeyPEM:       "",
		FullchainPEM: fullchain,
		Issuer:       issuer,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}, nil
}

// validateCSRResult 校验上游证书与客户端 CSR 完全一致。公共 CA 理论上不会
// 返回错误公钥或额外 SAN，但代理、配置错误和 CA 故障都可能发生，必须拦截。
func validateCSRResult(csr *x509.CertificateRequest, leaf *x509.Certificate) error {
	if csr == nil || leaf == nil {
		return i18n.E("service.csr.result_invalid")
	}
	if leaf.IsCA {
		return i18n.E("service.csr.result_is_ca")
	}
	if !publicKeysEqual(leaf.PublicKey, csr.PublicKey) {
		return i18n.E("service.csr.result_key_mismatch")
	}
	requested := make(map[string]bool, len(csr.DNSNames))
	for _, name := range csr.DNSNames {
		requested[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))] = true
	}
	issued := make(map[string]bool, len(leaf.DNSNames))
	for _, name := range leaf.DNSNames {
		issued[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))] = true
	}
	if len(issued) != len(requested) {
		return i18n.E("service.csr.result_domain_mismatch")
	}
	for name := range requested {
		if !issued[name] {
			return i18n.E("service.csr.result_domain_mismatch")
		}
	}
	for _, ip := range leaf.IPAddresses {
		if len(csr.IPAddresses) == 0 {
			return i18n.E("service.csr.result_domain_mismatch")
		}
		found := false
		for _, requestedIP := range csr.IPAddresses {
			if requestedIP.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return i18n.E("service.csr.result_domain_mismatch")
		}
	}
	if time.Now().After(leaf.NotAfter) {
		return i18n.E("service.csr.result_expired")
	}
	return nil
}

// StartCSRWorker 启动 CSR 申请后台处理器。申请状态持久化在数据库中，
// HTTP 请求断开不会中断签发；进程重启后 running 记录会重新排队。
func (m *Manager) StartCSRWorker(ctx context.Context) {
	if err := m.Store.ResetRunningCSRRequests(); err != nil {
		logger().Warn("恢复 CSR 申请失败", "error", err)
	}
	go func() {
		time.Sleep(2 * time.Second)
		m.csrTick(ctx)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.csrTick(ctx)
			}
		}
	}()
}

func (m *Manager) csrTick(ctx context.Context) {
	now := time.Now().UTC()
	if err := m.Store.ExpirePendingCSRRequests(now); err != nil {
		logger().Warn("清理过期 CSR 申请失败", "error", err)
	}
	list, err := m.Store.ListPendingCSRRequests(20)
	if err != nil {
		logger().Warn("查询待处理 CSR 申请失败", "error", err)
		return
	}
	for _, item := range list {
		if m.csrSem != nil {
			select {
			case m.csrSem <- struct{}{}:
			default:
				return
			}
		}
		go func(id int64) {
			defer func() {
				if m.csrSem != nil {
					<-m.csrSem
				}
				if recovered := recover(); recovered != nil {
					logger().Error("CSR 申请处理 panic", "request_id", id, "panic", recovered)
				}
			}()
			if err := m.runCSRRequest(ctx, id); err != nil {
				logger().Warn("CSR 申请处理失败", "request_id", id, "error", SafeError(err))
			}
		}(item.ID)
	}
}

func (m *Manager) runCSRRequest(ctx context.Context, id int64) error {
	claimed, err := m.Store.ClaimCSRRequest(id)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	req, err := m.Store.GetCSRRequest(id)
	if err != nil {
		return err
	}
	if req.ExpiresAt != nil && time.Now().After(*req.ExpiresAt) {
		m.failCSR(req, i18n.E("service.csr.expired"), false)
		return nil
	}

	info, err := ParseCSRInfo(req.CSRPEM)
	if err != nil {
		m.failCSR(req, err, false)
		return nil
	}
	allowed := ParseAllowedDomains(m.mustSetting("csr.allowed_domains"))
	allowAll := strings.EqualFold(strings.TrimSpace(m.mustSetting("csr.allow_all_domains")), "true")
	if err := ValidateCSRDomains(info.DNSNames, allowed, allowAll); err != nil {
		m.failCSR(req, err, false)
		return nil
	}

	dnsAccount, err := m.Store.GetDNSAccount(req.DNSAccountID)
	if err != nil {
		m.failCSR(req, i18n.E("service.csr.dns_account_not_found"), false)
		return nil
	}
	email := strings.TrimSpace(m.mustSetting("acme.email"))
	if email == "" {
		m.failCSR(req, i18n.E("service.csr.email_required"), false)
		return nil
	}
	eabKid, eabHMAC := m.loadCAEABSettings(req.CA)

	m.Store.AddLogKey("csr", req.ID, "info", "log.csr.started", map[string]any{"domains": strings.Join(info.DNSNames, ", "), "ca": req.CA})
	unlock := m.dnsLocks.Lock(strconv.FormatInt(req.DNSAccountID, 10))
	defer unlock()

	result, err := m.ObtainForCSR(CSRACMERequest{
		CSR:        info.CSR,
		CA:         req.CA,
		DNSAccount: dnsAccount,
		Email:      email,
		EABKid:     eabKid,
		EABHMAC:    eabHMAC,
	})
	if err != nil {
		m.failCSR(req, err, csrRetryable(err))
		return nil
	}

	cert := &model.Certificate{
		TaskID:        0,
		Domains:       info.DNSNames,
		PrimaryDomain: info.PrimaryDomain,
		Issuer:        result.Issuer,
		CA:            req.CA,
		Source:        "csr",
		CSRRequestID:  req.ID,
		CertPEM:       result.CertPEM,
		KeyPEM:        "",
		FullchainPEM:  result.FullchainPEM,
		Serial:        result.Serial,
		NotBefore:     result.NotBefore,
		NotAfter:      result.NotAfter,
		Status:        "valid",
	}
	if err := m.Store.CompleteCSRRequest(req.ID, cert); err != nil {
		m.failCSR(req, i18n.E("service.csr.save_failed", "error", SanitizeErrorText(err.Error())), true)
		return nil
	}
	m.Store.AddLogKey("csr", req.ID, "success", "log.csr.issued", map[string]any{"domain": cert.PrimaryDomain, "issuer": cert.Issuer})
	logger().Info("CSR 申请签发完成", "request_id", req.ID, "cert_id", cert.ID, "domains", strings.Join(cert.Domains, ","))
	return nil
}

func (m *Manager) failCSR(req *model.CSRRequest, err error, retryable bool) {
	if req == nil {
		return
	}
	message := i18n.EncodeError(err)
	if err := m.Store.SetCSRRequestFailed(req.ID, "csr.issue_failed", message, retryable); err != nil {
		logger().Warn("更新 CSR 申请失败状态失败", "request_id", req.ID, "error", err)
	}
	m.Store.AddLogKey("csr", req.ID, "error", "log.csr.failed", map[string]any{"message": i18n.ErrorText(m.Locale(), err)})
	logger().Warn("CSR 申请失败", "request_id", req.ID, "error", SafeError(err), "retryable", retryable)
}

func (m *Manager) mustSetting(key string) string {
	value, _ := m.Store.GetSetting(key)
	return value
}

func csrRetryable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, token := range []string{
		"ratelimit", "rate limit", "too many", "timeout", "temporary", "connection",
		"no such host", "serverinternal", "badgateway", "unavailable",
		"dns problem", "dns error", "urn:ietf:params:acme:error:dns",
		"500", "502", "503", "504",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// keyedLocks 为不同 DNS 账户提供独立互斥锁，避免全局串行化。
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (k *keyedLocks) Lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*sync.Mutex)
	}
	lock := k.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		k.locks[key] = lock
	}
	k.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}
