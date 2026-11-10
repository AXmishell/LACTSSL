package service

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"math/big"
	"time"

	"golang.org/x/crypto/ocsp"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// RevokedCertificateEntry 是 CRL 中的一条撤销记录。
type RevokedCertificateEntry struct {
	Serial    *big.Int
	RevokedAt time.Time
	Reason    int
}

// CARevocationCapabilities 返回 CA 是否具备 CRL/OCSP 签名能力。
func CARevocationCapabilities(ca *model.PrivateCA) (crl bool, ocspResponse bool, err error) {
	_, cert, err := loadIssuingCA(ca)
	if err != nil {
		return false, false, err
	}
	crl = cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageCRLSign != 0
	ocspResponse = cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageDigitalSignature != 0
	return crl, ocspResponse, nil
}

// BuildCRL 使用私有 CA 签发 DER 编码的 CRL。
func BuildCRL(ca *model.PrivateCA, entries []RevokedCertificateEntry, number int64, now time.Time) ([]byte, error) {
	key, cert, err := loadIssuingCA(ca)
	if err != nil {
		return nil, err
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCRLSign == 0 {
		return nil, i18n.E("service.acme_server.ca_crl_unsupported")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, i18n.E("service.acme_server.ca_crl_unsupported")
	}
	revoked := make([]x509.RevocationListEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Serial == nil {
			continue
		}
		revoked = append(revoked, x509.RevocationListEntry{
			SerialNumber:   entry.Serial,
			RevocationTime: entry.RevokedAt.UTC(),
			ReasonCode:     entry.Reason,
		})
	}
	template := &x509.RevocationList{
		Number:                    big.NewInt(number),
		ThisUpdate:                now.UTC().Add(-5 * time.Minute),
		NextUpdate:                now.UTC().Add(7 * 24 * time.Hour),
		RevokedCertificateEntries: revoked,
	}
	return x509.CreateRevocationList(rand.Reader, template, cert, signer)
}

// BuildOCSPResponse 使用私有 CA 签发 OCSP 响应。
func BuildOCSPResponse(ca *model.PrivateCA, serial *big.Int, status int, revokedAt time.Time, reason int, now time.Time) ([]byte, error) {
	key, cert, err := loadIssuingCA(ca)
	if err != nil {
		return nil, err
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, i18n.E("service.acme_server.ca_ocsp_unsupported")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, i18n.E("service.acme_server.ca_ocsp_unsupported")
	}
	template := ocsp.Response{
		Status:       status,
		SerialNumber: serial,
		ThisUpdate:   now.UTC().Add(-5 * time.Minute),
		NextUpdate:   now.UTC().Add(24 * time.Hour),
		ProducedAt:   now.UTC(),
	}
	if status == ocsp.Revoked {
		template.RevokedAt = revokedAt.UTC()
		template.RevocationReason = reason
	}
	return ocsp.CreateResponse(cert, cert, template, signer)
}
