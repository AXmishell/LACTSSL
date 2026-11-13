package service

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/digitalocean"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/providers/dns/gandi"
	"github.com/go-acme/lego/v4/providers/dns/godaddy"
	"github.com/go-acme/lego/v4/providers/dns/hetzner"
	"github.com/go-acme/lego/v4/providers/dns/linode"
	"github.com/go-acme/lego/v4/providers/dns/namecheap"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
	"github.com/go-acme/lego/v4/providers/dns/vultr"
	"github.com/go-acme/lego/v4/registration"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
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
	Store    *store.Store
	acmeMu   sync.Mutex
	running  sync.Map
	queued   sync.Map
	batchSem chan struct{}
}

// New 创建业务管理器。
func New(st *store.Store) *Manager {
	return &Manager{Store: st, batchSem: make(chan struct{}, 4)}
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

	user, err := m.loadACMEUser(ca.Value, req.Email)
	if err != nil {
		return nil, err
	}

	if req.DNSAccount == nil {
		return nil, i18n.E("service.acme.dns_account_required")
	}
	provider, undo, err := newDNSProvider(req.DNSAccount.Provider, req.DNSAccount.Config)
	if err != nil {
		return nil, err
	}
	defer undo()

	keyType := certificateKeyType(req.KeyType)
	if ca.Protocol == "v1" {
		return m.obtainV1(ca, user, req, provider, keyType)
	}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = ca.URL
	cfg.UserAgent = "LACTSSL/1.0"
	cfg.Certificate.KeyType = keyType

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, i18n.E("service.acme.client_init_failed", "error", err.Error())
	}

	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return nil, i18n.E("service.acme.dns01_config_failed", "error", err.Error())
	}

	// 注册或复用 ACME 账户。
	var reg *registration.Resource
	if ca.EAB {
		if req.EABKid == "" || req.EABHMAC == "" {
			return nil, i18n.E("service.acme.eab_required", "ca", ca.Localized(m.Locale()).Label)
		}
		reg, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
			TermsOfServiceAgreed: true,
			Kid:                  strings.TrimSpace(req.EABKid),
			HmacEncoded:          normalizeEABKey(req.EABHMAC),
		})
	} else {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		return nil, i18n.E("service.acme.register_failed", "error", err.Error())
	}
	user.reg = reg

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

	return &ACMEResult{
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})),
		KeyPEM:       string(resource.PrivateKey),
		FullchainPEM: fullchain,
		Issuer:       issuer,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}, nil
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
	for _, c := range certs {
		if !c.IsCA {
			return c, nil
		}
	}
	return certs[0], nil
}

// newDNSProvider 根据服务商构造 lego DNS Provider，并在结束后恢复环境变量。
func newDNSProvider(name string, cfg map[string]string) (challenge.Provider, func(), error) {
	if _, ok := DNSProviderInfo(name); !ok {
		return nil, func() {}, i18n.E("service.acme.dns_provider_unsupported", "provider", name)
	}

	envs := map[string]string{}
	switch name {
	case "alidns":
		envs["ALICLOUD_ACCESS_KEY"] = cfg["access_key_id"]
		envs["ALICLOUD_SECRET_KEY"] = cfg["access_key_secret"]
		envs["ALICLOUD_REGION_ID"] = cfg["region_id"]
	case "cloudflare":
		if string(cfg["api_token"]) != "" {
			envs["CLOUDFLARE_DNS_API_TOKEN"] = cfg["api_token"]
		} else {
			envs["CLOUDFLARE_EMAIL"] = cfg["email"]
			envs["CLOUDFLARE_API_KEY"] = cfg["api_key"]
		}
	case "dnspod":
		envs["DNSPOD_API_KEY"] = cfg["api_key"]
	case "tencentcloud":
		envs["TENCENTCLOUD_SECRET_ID"] = cfg["secret_id"]
		envs["TENCENTCLOUD_SECRET_KEY"] = cfg["secret_key"]
		envs["TENCENTCLOUD_REGION"] = cfg["region"]
	case "digitalocean":
		envs["DO_AUTH_TOKEN"] = cfg["token"]
	case "godaddy":
		envs["GODADDY_API_KEY"] = cfg["api_key"]
		envs["GODADDY_API_SECRET"] = cfg["api_secret"]
	case "namecheap":
		envs["NAMECHEAP_API_USER"] = cfg["api_user"]
		envs["NAMECHEAP_API_KEY"] = cfg["api_key"]
		envs["NAMECHEAP_SOURCE_IP"] = cfg["source_ip"]
	case "vultr":
		envs["VULTR_API_KEY"] = cfg["api_key"]
	case "linode":
		envs["LINODE_TOKEN"] = cfg["token"]
	case "hetzner":
		envs["HETZNER_API_KEY"] = cfg["api_key"]
	case "gandi":
		envs["GANDI_API_KEY"] = cfg["api_key"]
	}

	// 未填写的字段不覆盖系统已有环境变量。
	keys := make([]string, 0, len(envs))
	for k := range envs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	backup := map[string]*string{}
	for _, k := range keys {
		v := strings.TrimSpace(envs[k])
		if v == "" {
			continue
		}
		if old, ok := os.LookupEnv(k); ok {
			copied := old
			backup[k] = &copied
		} else {
			backup[k] = nil
		}
		_ = os.Setenv(k, v)
	}
	restore := func() {
		for k, old := range backup {
			if old == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *old)
			}
		}
	}

	var provider challenge.Provider
	var err error
	switch name {
	case "alidns":
		provider, err = alidns.NewDNSProvider()
	case "cloudflare":
		provider, err = cloudflare.NewDNSProvider()
	case "dnspod":
		provider, err = dnspod.NewDNSProvider()
	case "tencentcloud":
		provider, err = tencentcloud.NewDNSProvider()
	case "digitalocean":
		provider, err = digitalocean.NewDNSProvider()
	case "godaddy":
		provider, err = godaddy.NewDNSProvider()
	case "namecheap":
		provider, err = namecheap.NewDNSProvider()
	case "vultr":
		provider, err = vultr.NewDNSProvider()
	case "linode":
		provider, err = linode.NewDNSProvider()
	case "hetzner":
		provider, err = hetzner.NewDNSProvider()
	case "gandi":
		provider, err = gandi.NewDNSProvider()
	default:
		err = i18n.E("service.acme.dns_provider_unsupported", "provider", name)
	}
	if err != nil {
		restore()
		return nil, func() {}, i18n.E("service.acme.dns_provider_init_failed", "error", err.Error())
	}
	return provider, restore, nil
}
