package service

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/store"
)

// ACMERequest 描述一次证书签发请求。
type ACMERequest struct {
	Domains    []string
	DNSAccount *model.DNSAccount
	CA         string
	KeyType    string
	Email      string
	EABKid     string
	EABHMAC    string
}

// ACMEResult 是签发结果。
type ACMEResult struct {
	CertPEM      string
	KeyPEM       string
	FullchainPEM string
	Issuer       string
	Serial       string
	NotBefore    time.Time
	NotAfter     time.Time
}

// acmeUser 实现 lego 的 registration.User 接口。
type acmeUser struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Manager 是核心业务调度器。
type Manager struct {
	Store  *store.Store
	acmeMu sync.Mutex
	// accountMu 串行化 ACME 账户密钥加载、客户端创建与注册，
	// 避免 CSR 申请与普通任务同时首次注册同一上游账户。
	accountMu sync.Mutex
	running   sync.Map
	queued    sync.Map
	// monitorRunning 对同一监控项的并发检查去重：手动点击与定时任务
	// 可能同时触发，避免重复外拨、重复写库与重复通知。
	monitorRunning sync.Map
	batchSem       chan struct{}
	// csrSem 限制 CSR 申请与上游 CA 交互的并发数。
	csrSem chan struct{}
	// dnsLocks 按 DNS 账户串行化挑战写入，避免同一服务商凭据并发冲突。
	dnsLocks keyedLocks
	// ProtectedPaths 是本地部署禁止写入的目录（数据目录、配置目录、程序目录）。
	// 避免部署参数把证书/私钥覆盖到数据库、config.yaml 或程序二进制上。
	ProtectedPaths []string
}

// New 创建业务管理器。
func New(st *store.Store) *Manager {
	return &Manager{Store: st, batchSem: make(chan struct{}, 4), csrSem: make(chan struct{}, 4)}
}

// Locale 返回系统设置中保存的语言，供后台任务与邮件使用。
func (m *Manager) Locale() i18n.Locale {
	value, _ := m.Store.GetSetting("language")
	return i18n.Normalize(value)
}

// 签发阶段全局串行，避免 DNS 服务商环境变量互相干扰。
func (m *Manager) Obtain(req ACMERequest) (*ACMEResult, error) {
	if len(req.Domains) == 0 {
		return nil, i18n.E("service.acme.domains_empty")
	}
	ca, err := m.ResolveCA(req.CA)
	if err != nil {
		return nil, err
	}

	m.acmeMu.Lock()
	defer m.acmeMu.Unlock()

	if req.DNSAccount == nil {
		return nil, i18n.E("service.acme.dns_account_required")
	}
	provider, undo, err := newDNSProvider(req.DNSAccount.Provider, req.DNSAccount.Config)
	if err != nil {
		return nil, err
	}
	defer undo()
	// 与 CSR 申请共用按 DNS 账户的锁，避免任务签发和 CSR 申请同时
	// 向同一服务商写入/清理 TXT 记录。
	unlockDNS := m.dnsLocks.Lock(strconv.FormatInt(req.DNSAccount.ID, 10))
	defer unlockDNS()

	keyType := certificateKeyType(req.KeyType)
	if ca.Protocol == "v1" {
		m.accountMu.Lock()
		user, err := m.loadACMEUser(ca.Value, req.Email)
		m.accountMu.Unlock()
		if err != nil {
			return nil, err
		}
		result, err := m.obtainV1(ca, user, req, provider, keyType)
		if err != nil {
			return nil, err
		}
		if err := validateACMEResult(req.Domains, result); err != nil {
			return nil, err
		}
		return result, nil
	}

	client, _, err := m.newACMEClient(ca, req.Email, keyType, provider, req.EABKid, req.EABHMAC)
	if err != nil {
		return nil, err
	}

	request := certificate.ObtainRequest{
		Domains: req.Domains,
		Bundle:  true,
	}
	resource, err := client.Certificate.Obtain(request)
	if err != nil {
		return nil, i18n.E("service.acme.issue_failed", "error", err.Error())
	}

	leaf, err := pickLeaf(resource.Certificate, req.Domains)
	if err != nil {
		return nil, err
	}

	fullchain := string(resource.Certificate)
	// 保证完整链包含根证书之外的中间证书。
	if len(resource.IssuerCertificate) > 0 && !strings.Contains(fullchain, string(resource.IssuerCertificate)) {
		fullchain = strings.TrimRight(fullchain, "\n") + "\n" + string(resource.IssuerCertificate)
	}

	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}

	result := &ACMEResult{
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})),
		KeyPEM:       string(resource.PrivateKey),
		FullchainPEM: fullchain,
		Issuer:       issuer,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}
	if err := validateACMEResult(req.Domains, result); err != nil {
		return nil, err
	}
	return result, nil
}

// newACMEClient 创建并注册 ACME v2 客户端。账户密钥与注册过程串行化，
// 供普通任务签发和 CSR 申请共用，避免并发首次注册产生不同账户。
func (m *Manager) newACMEClient(ca CADef, email string, keyType certcrypto.KeyType, provider challenge.Provider, eabKid, eabHMAC string) (*lego.Client, *acmeUser, error) {
	m.accountMu.Lock()
	defer m.accountMu.Unlock()

	user, err := m.loadACMEUser(ca.Value, email)
	if err != nil {
		return nil, nil, err
	}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = ca.URL
	cfg.UserAgent = "LACTSSL/1.0"
	cfg.Certificate.KeyType = keyType

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, nil, i18n.E("service.acme.client_init_failed", "error", err.Error())
	}
	if provider != nil {
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return nil, nil, i18n.E("service.acme.dns01_config_failed", "error", err.Error())
		}
	}

	var reg *registration.Resource
	if ca.EAB {
		if eabKid == "" || eabHMAC == "" {
			return nil, nil, i18n.E("service.acme.eab_required", "ca", ca.Localized(m.Locale()).Label)
		}
		reg, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
			TermsOfServiceAgreed: true,
			Kid:                  strings.TrimSpace(eabKid),
			HmacEncoded:          normalizeEABKey(eabHMAC),
		})
	} else {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		return nil, nil, i18n.E("service.acme.register_failed", "error", err.Error())
	}
	user.reg = reg
	return client, user, nil
}

// validateACMEResult 校验上游返回的证书确实覆盖请求域名，且私钥与证书匹配。
// 防止 CA/代理返回错误域名的证书或不匹配的私钥后被静默部署。
func validateACMEResult(domains []string, result *ACMEResult) error {
	if result == nil {
		return i18n.E("service.acme.chain_parse_failed", "error", "empty result")
	}
	leaf, err := parseFirstCertificate(result.CertPEM)
	if err != nil {
		return i18n.E("service.acme.chain_parse_failed", "error", err.Error())
	}
	for _, domain := range domains {
		if !certificateCoversDomain(leaf, domain) {
			return i18n.E("service.acme.result_domain_mismatch", "domain", domain)
		}
	}
	key, err := certcrypto.ParsePEMPrivateKey([]byte(result.KeyPEM))
	if err != nil {
		return i18n.E("service.acme.account_key_parse_failed", "error", err.Error())
	}
	if !publicKeysEqual(leaf.PublicKey, publicKey(key)) {
		return i18n.E("service.acme.result_key_mismatch")
	}
	return nil
}

// certificateCoversDomain 判断证书是否精确覆盖指定域名或 IP 标识符。
func certificateCoversDomain(cert *x509.Certificate, domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if ip := net.ParseIP(domain); ip != nil {
		for _, candidate := range cert.IPAddresses {
			if candidate.Equal(ip) {
				return true
			}
		}
		return false
	}
	if strings.EqualFold(cert.Subject.CommonName, domain) {
		return true
	}
	for _, name := range cert.DNSNames {
		if strings.EqualFold(name, domain) {
			return true
		}
	}
	return false
}

// certificateKeyType 把界面上的私钥类型转换为 lego 的证书密钥类型。
func certificateKeyType(value string) certcrypto.KeyType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rsa2048":
		return certcrypto.RSA2048
	case "rsa4096":
		return certcrypto.RSA4096
	case "ecdsa384":
		return certcrypto.EC384
	default:
		return certcrypto.EC256
	}
}

// normalizeEABKey 兼容用户粘贴的带 padding 或标准 Base64 字符集的 HMAC Key。
func normalizeEABKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "+", "-")
	value = strings.ReplaceAll(value, "/", "_")
	return strings.TrimRight(value, "=")
}

// loadACMEUser 加载或生成指定 CA 的 ACME 账户密钥。
func (m *Manager) loadACMEUser(ca, email string) (*acmeUser, error) {
	keyName := CAAccountKeySettingKey(ca)
	value, err := m.Store.GetSecret(keyName)
	if err != nil {
		return nil, err
	}
	var privateKey crypto.PrivateKey
	if strings.TrimSpace(value) != "" {
		privateKey, err = certcrypto.ParsePEMPrivateKey([]byte(value))
		if err != nil {
			return nil, i18n.E("service.acme.account_key_parse_failed", "error", err.Error())
		}
	} else {
		privateKey, err = certcrypto.GeneratePrivateKey(certcrypto.EC256)
		if err != nil {
			return nil, i18n.E("service.acme.account_key_generate_failed", "error", err.Error())
		}
		encoded := certcrypto.PEMEncode(privateKey)
		if err := m.Store.SetSecret(keyName, string(encoded)); err != nil {
			return nil, i18n.E("service.acme.account_key_save_failed", "error", err.Error())
		}
	}
	return &acmeUser{email: email, key: privateKey}, nil
}

// pickLeaf 从证书链中选择终端证书。
func pickLeaf(bundle []byte, domains []string) (*x509.Certificate, error) {
	certs, err := certcrypto.ParsePEMBundle(bundle)
	if err != nil || len(certs) == 0 {
		return nil, i18n.E("service.acme.chain_parse_failed", "error", err.Error())
	}
	for _, c := range certs {
		if c.IsCA {
			continue
		}
		for _, d := range domains {
			if strings.EqualFold(c.Subject.CommonName, d) {
				return c, nil
			}
			for _, name := range c.DNSNames {
				if strings.EqualFold(name, d) {
					return c, nil
				}
			}
		}
	}
	return nil, i18n.E("service.acme.result_domain_mismatch", "domain", strings.Join(domains, ","))
}

// newDNSProvider 根据服务商插件构造 lego DNS Provider。
// 所有服务商都由外部 exec helper 提供，配置直接交给 helper，
// 主进程不修改环境变量。
func newDNSProvider(name string, cfg map[string]string) (challenge.Provider, func(), error) {
	plugin, ok := dnsplugin.Lookup(name)
	if !ok {
		return nil, func() {}, i18n.E("service.acme.dns_provider_unavailable", "provider", name)
	}
	provider, err := plugin.New(cfg)
	if err != nil {
		return nil, func() {}, i18n.E("service.acme.dns_provider_init_failed", "error", err.Error())
	}
	return provider, func() {}, nil
}
