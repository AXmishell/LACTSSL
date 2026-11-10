package service

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// PrivateCertRequest 描述使用私有 CA 签发终端证书所需的参数。
type PrivateCertRequest struct {
	Domains   []string
	KeyType   string
	ValidDays int
}

// IssuePrivateCertificate 使用私有 CA 为域名签发一张 TLS 服务器证书。
func IssuePrivateCertificate(ca *model.PrivateCA, req PrivateCertRequest) (*ACMEResult, error) {
	if ca == nil {
		return nil, i18n.E("task.error.private_ca_not_found")
	}
	if len(req.Domains) == 0 {
		return nil, i18n.E("service.acme.domains_empty")
	}
	keyType := normalizePrivateCAKeyType(req.KeyType)
	if keyType == "" {
		return nil, i18n.E("task.error.key_type_unsupported")
	}

	caKey, err := parsePrivateKey(ca.KeyPEM)
	if err != nil {
		return nil, err
	}
	caCert, err := parseFirstCertificate(ca.CertPEM)
	if err != nil {
		return nil, err
	}
	if !caCert.IsCA || (caCert.KeyUsage != 0 && caCert.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, i18n.E("service.private_ca.cert_not_ca")
	}
	if !publicKeysEqual(caCert.PublicKey, publicKey(caKey)) {
		return nil, i18n.E("service.private_ca.key_mismatch")
	}
	now := time.Now().UTC()
	if now.After(caCert.NotAfter) {
		return nil, i18n.E("service.private_ca.cert_expired")
	}
	if len(caCert.ExtKeyUsage) > 0 {
		allowed := false
		for _, usage := range caCert.ExtKeyUsage {
			if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, i18n.E("service.private_ca.ca_eku_no_server_auth")
		}
	}

	leafKey, err := generateCAKey(keyType)
	if err != nil {
		return nil, i18n.E("service.private_ca.generate_key_failed", "error", err.Error())
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, i18n.E("service.private_ca.generate_serial_failed", "error", err.Error())
	}
	serial.SetBit(serial, 127, 1)

	days := req.ValidDays
	if days <= 0 {
		days = 365
	}
	notAfter := now.AddDate(0, 0, days)
	if notAfter.After(caCert.NotAfter) {
		notAfter = caCert.NotAfter
	}

	keyUsage := x509.KeyUsageDigitalSignature
	if strings.HasPrefix(keyType, "rsa") {
		keyUsage |= x509.KeyUsageKeyEncipherment
	}
	authorityKeyID := caCert.SubjectKeyId
	if len(authorityKeyID) == 0 {
		authorityKeyID = subjectKeyID(caKey)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: req.Domains[0]},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		SubjectKeyId:          subjectKeyID(leafKey),
		AuthorityKeyId:        authorityKeyID,
		DNSNames:              append([]string(nil), req.Domains...),
		CRLDistributionPoints: append([]string(nil), caCert.CRLDistributionPoints...),
		OCSPServer:            append([]string(nil), caCert.OCSPServer...),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, publicKey(leafKey), caKey)
	if err != nil {
		return nil, i18n.E("service.private_ca.create_cert_failed", "error", err.Error())
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, i18n.E("service.private_ca.create_cert_failed", "error", err.Error())
	}
	keyPEM, err := encodePrivateKey(leafKey)
	if err != nil {
		return nil, i18n.E("service.private_ca.encode_key_failed", "error", err.Error())
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}))

	return &ACMEResult{
		CertPEM:      leafPEM,
		KeyPEM:       string(keyPEM),
		FullchainPEM: leafPEM,
		Issuer:       issuer,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}, nil
}
