package service

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

// CertificateIdentifier 是 ACME 订单中的一个证书标识。
type CertificateIdentifier struct {
	Type  string
	Value string
}

// SignCSROptions 控制叶子证书签发时的扩展与有效期。
type SignCSROptions struct {
	NotBefore time.Time
	NotAfter  time.Time
	CRLURLs   []string
	OCSPURLs  []string
	// ChainPEM 是除叶子证书外的中间证书链，不包含自签名根证书。
	ChainPEM string
}

// IssuedCertificate 是使用私有 CA 签发叶子证书的结果。
type IssuedCertificate struct {
	CertPEM      string
	FullchainPEM string
	Serial       string
	Issuer       string
	NotBefore    time.Time
	NotAfter     time.Time
}

// ValidateIssuingCA 校验私有 CA 是否可用于签发叶子证书。
func ValidateIssuingCA(ca *model.PrivateCA) error {
	_, cert, err := loadIssuingCA(ca)
	if err != nil {
		return err
	}
	if !cert.IsCA || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
		return i18n.E("service.acme_server.ca_not_signing")
	}
	if time.Now().After(cert.NotAfter) {
		return i18n.E("service.acme_server.ca_expired")
	}
	return nil
}

func loadIssuingCA(ca *model.PrivateCA) (crypto.PrivateKey, *x509.Certificate, error) {
	if ca == nil {
		return nil, nil, i18n.E("service.acme_server.ca_required")
	}
	key, err := parsePrivateKey(ca.KeyPEM)
	if err != nil {
		return nil, nil, err
	}
	cert, err := parseCACertificate(ca.CertPEM, key)
	if err != nil {
		return nil, nil, err
	}
	if !publicKeysEqual(cert.PublicKey, publicKey(key)) {
		return nil, nil, i18n.E("service.private_ca.key_mismatch")
	}
	if !cert.IsCA || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, nil, i18n.E("service.acme_server.ca_not_signing")
	}
	if time.Now().After(cert.NotAfter) {
		return nil, nil, i18n.E("service.acme_server.ca_expired")
	}
	return key, cert, nil
}

// SignCSR 使用私有 CA 签发叶子证书。CSR 中的 SAN 必须与 identifiers 完全一致。
func SignCSR(ca *model.PrivateCA, csr *x509.CertificateRequest, identifiers []CertificateIdentifier, opts SignCSROptions) (*IssuedCertificate, error) {
	if csr == nil {
		return nil, i18n.E("service.acme_server.csr_required")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, i18n.E("service.acme_server.csr_signature_invalid", "error", err.Error())
	}
	if err := validateCSRPublicKey(csr.PublicKey); err != nil {
		return nil, err
	}

	key, caCert, err := loadIssuingCA(ca)
	if err != nil {
		return nil, err
	}

	normalized, dnsNames, ipAddresses, err := normalizeCertificateIdentifiers(identifiers)
	if err != nil {
		return nil, err
	}
	if err := matchCSRIdentifiers(csr, dnsNames, ipAddresses); err != nil {
		return nil, err
	}

	notBefore := opts.NotBefore
	if notBefore.IsZero() {
		notBefore = time.Now().UTC().Add(-5 * time.Minute)
	}
	notAfter := opts.NotAfter
	if notAfter.IsZero() {
		notAfter = time.Now().UTC().AddDate(0, 0, 90)
	}
	if !notAfter.After(notBefore) {
		return nil, i18n.E("service.acme_server.validity_invalid")
	}
	if notAfter.After(caCert.NotAfter) {
		notAfter = caCert.NotAfter
	}
	if !notAfter.After(notBefore) {
		return nil, i18n.E("service.acme_server.validity_invalid")
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, i18n.E("service.acme_server.serial_failed", "error", err.Error())
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	serial.SetBit(serial, 127, 1)

	subject := csr.Subject
	if strings.TrimSpace(subject.CommonName) == "" {
		subject.CommonName = normalized[0].Value
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
		SubjectKeyId:          publicKeyID(csr.PublicKey),
		AuthorityKeyId:        caCert.SubjectKeyId,
		CRLDistributionPoints: trimList(opts.CRLURLs),
		OCSPServer:            trimList(opts.OCSPURLs),
	}
	if _, ok := csr.PublicKey.(*rsa.PublicKey); ok {
		template.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	if len(template.AuthorityKeyId) == 0 {
		template.AuthorityKeyId = publicKeyID(caCert.PublicKey)
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, key)
	if err != nil {
		return nil, i18n.E("service.acme_server.issue_failed", "error", err.Error())
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, i18n.E("service.acme_server.issue_failed", "error", err.Error())
	}
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}))
	fullchain := leafPEM
	if chain := strings.TrimSpace(opts.ChainPEM); chain != "" {
		fullchain = strings.TrimRight(leafPEM, "\n") + "\n" + strings.TrimSpace(chain) + "\n"
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	return &IssuedCertificate{
		CertPEM:      leafPEM,
		FullchainPEM: fullchain,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		Issuer:       issuer,
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}, nil
}

func validateCSRPublicKey(pub crypto.PublicKey) error {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		if key.N == nil || key.N.BitLen() < 2048 {
			return i18n.E("service.acme_server.csr_key_weak")
		}
	case *ecdsa.PublicKey:
		switch key.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
		default:
			return i18n.E("service.acme_server.csr_key_unsupported")
		}
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return i18n.E("service.acme_server.csr_key_unsupported")
		}
	default:
		return i18n.E("service.acme_server.csr_key_unsupported")
	}
	return nil
}

func normalizeCertificateIdentifiers(values []CertificateIdentifier) ([]CertificateIdentifier, []string, []net.IP, error) {
	if len(values) == 0 {
		return nil, nil, nil, i18n.E("service.acme_server.identifiers_required")
	}
	normalized := make([]CertificateIdentifier, 0, len(values))
	dnsNames := make([]string, 0, len(values))
	ipAddresses := make([]net.IP, 0, len(values))
	seen := map[string]bool{}
	for _, item := range values {
		kind := strings.ToLower(strings.TrimSpace(item.Type))
		value := strings.TrimSpace(item.Value)
		switch kind {
		case "dns":
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			if net.ParseIP(value) != nil || !validDNSName(value) {
				return nil, nil, nil, i18n.E("service.acme_server.identifier_invalid", "identifier", item.Value)
			}
			key := "dns:" + value
			if seen[key] {
				return nil, nil, nil, i18n.E("service.acme_server.identifier_duplicate", "identifier", value)
			}
			seen[key] = true
			normalized = append(normalized, CertificateIdentifier{Type: "dns", Value: value})
			dnsNames = append(dnsNames, value)
		case "ip":
			ip := net.ParseIP(value)
			if ip == nil {
				return nil, nil, nil, i18n.E("service.acme_server.identifier_invalid", "identifier", item.Value)
			}
			key := "ip:" + ip.String()
			if seen[key] {
				return nil, nil, nil, i18n.E("service.acme_server.identifier_duplicate", "identifier", value)
			}
			seen[key] = true
			normalized = append(normalized, CertificateIdentifier{Type: "ip", Value: ip.String()})
			ipAddresses = append(ipAddresses, ip)
		default:
			return nil, nil, nil, i18n.E("service.acme_server.identifier_type_unsupported", "type", item.Type)
		}
	}
	return normalized, dnsNames, ipAddresses, nil
}

func matchCSRIdentifiers(csr *x509.CertificateRequest, dnsNames []string, ipAddresses []net.IP) error {
	csrDNS := make(map[string]bool, len(csr.DNSNames))
	for _, name := range csr.DNSNames {
		csrDNS[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))] = true
	}
	csrIPs := make(map[string]bool, len(csr.IPAddresses))
	for _, ip := range csr.IPAddresses {
		csrIPs[ip.String()] = true
	}
	if len(csrDNS) != len(dnsNames) || len(csrIPs) != len(ipAddresses) {
		return i18n.E("service.acme_server.csr_identifier_mismatch")
	}
	for _, name := range dnsNames {
		if !csrDNS[name] {
			return i18n.E("service.acme_server.csr_identifier_mismatch")
		}
	}
	for _, ip := range ipAddresses {
		if !csrIPs[ip.String()] {
			return i18n.E("service.acme_server.csr_identifier_mismatch")
		}
	}
	if cn := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(csr.Subject.CommonName), ".")); cn != "" {
		found := false
		for _, name := range dnsNames {
			if cn == name {
				found = true
				break
			}
		}
		for _, ip := range ipAddresses {
			if cn == ip.String() {
				found = true
				break
			}
		}
		if !found {
			return i18n.E("service.acme_server.csr_identifier_mismatch")
		}
	}
	return nil
}

func publicKeyID(pub crypto.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil
	}
	sum := sha1.Sum(der)
	return sum[:]
}

// BuildCAChainPEM 返回签发 CA 及其上级中间证书，不包含自签名根证书。
// 该链用于 ACME 证书下载，叶子证书由调用方另外拼接。
func BuildCAChainPEM(st *store.Store, ca *model.PrivateCA) (string, error) {
	if st == nil || ca == nil {
		return "", nil
	}
	parts := make([]string, 0, 4)
	current := ca
	seen := map[int64]bool{}
	for current != nil && !seen[current.ID] && len(parts) < 10 {
		seen[current.ID] = true
		cert, err := parseFirstCertificate(current.CertPEM)
		if err != nil {
			return "", err
		}
		if !isSelfSignedCertificate(cert) {
			parts = append(parts, strings.TrimSpace(current.CertPEM))
		}
		if current.ParentID <= 0 {
			break
		}
		parent, err := st.GetPrivateCA(current.ParentID)
		if err != nil {
			return "", err
		}
		current = parent
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n") + "\n", nil
}

func isSelfSignedCertificate(cert *x509.Certificate) bool {
	return cert != nil && cert.Subject.String() == cert.Issuer.String()
}
