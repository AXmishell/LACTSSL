package service

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// PrivateCARequest 描述生成私有 CA 所需的参数。
type PrivateCARequest struct {
	Name               string
	CommonName         string
	Organization       string
	OrganizationalUnit string
	Country            string
	State              string
	Locality           string
	Email              string
	SerialNumber       string
	KeyType            string
	ValidDays          int
	ParentID           int64
	ParentCertPEM      string
	ParentKeyPEM       string
	PathLen            *int
	KeyUsage           []string
	ExtKeyUsage        []string
	DNSNames           []string
	IPAddresses        []string
	PermittedDNS       []string
	ExcludedDNS        []string
	PermittedIPs       []string
	ExcludedIPs        []string
	CRLURLs            []string
	OCSPURLs           []string
	IssuerURL          string
	PolicyOIDs         []string
	CPSURL             string
}

var (
	oidEmailAddress        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
	oidCertificatePolicies = asn1.ObjectIdentifier{2, 5, 29, 32}
	oidPolicyQualifierCPS  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 2, 1}
)

const privateCAPrefix = "private-ca:"

// PrivateCAValue 返回私有 CA 在任务配置中的引用值。
func PrivateCAValue(id int64) string {
	return privateCAPrefix + strconv.FormatInt(id, 10)
}

// ParsePrivateCAValue 解析任务配置中的私有 CA 引用。
func ParsePrivateCAValue(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, privateCAPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, privateCAPrefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// GeneratePrivateCA 生成私有 CA，返回可落库的实体。提供上级 CA 时生成下级 CA，否则生成自签名根 CA。
func GeneratePrivateCA(req PrivateCARequest) (*model.PrivateCA, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, i18n.E("service.private_ca.name_required")
	}
	commonName := strings.TrimSpace(req.CommonName)
	if commonName == "" {
		commonName = name
	}
	if err := validateSubjectField("CN", commonName, 64); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		code  string
		value string
		max   int
	}{
		{"O", req.Organization, 64},
		{"OU", req.OrganizationalUnit, 64},
		{"ST", req.State, 64},
		{"L", req.Locality, 64},
	} {
		if err := validateSubjectField(field.code, field.value, field.max); err != nil {
			return nil, err
		}
	}
	country := strings.ToUpper(strings.TrimSpace(req.Country))
	if country != "" && (len(country) != 2 || !isAlpha(country)) {
		return nil, i18n.E("service.private_ca.country_invalid")
	}
	email := strings.TrimSpace(req.Email)
	if email != "" {
		parsed, err := mail.ParseAddress(email)
		if err != nil {
			return nil, i18n.E("service.private_ca.email_invalid")
		}
		email = parsed.Address
	}
	keyType := normalizePrivateCAKeyType(req.KeyType)
	if keyType == "" {
		return nil, i18n.E("service.private_ca.key_type_unsupported", "type", req.KeyType)
	}
	days := req.ValidDays
	if days <= 0 {
		days = 3650
	}
	if days < 1 || days > 36500 {
		return nil, i18n.E("service.private_ca.valid_days_range")
	}

	pathLen, pathLenZero, err := parsePathLen(req.PathLen)
	if err != nil {
		return nil, err
	}
	keyUsage, err := parseKeyUsage(req.KeyUsage)
	if err != nil {
		return nil, err
	}
	extKeyUsage, err := parseExtKeyUsage(req.ExtKeyUsage)
	if err != nil {
		return nil, err
	}
	dnsNames, err := parseDNSNames(req.DNSNames)
	if err != nil {
		return nil, err
	}
	ipAddresses, err := parseIPAddresses(req.IPAddresses)
	if err != nil {
		return nil, err
	}
	permittedIPs, err := parseIPNets(req.PermittedIPs)
	if err != nil {
		return nil, err
	}
	excludedIPs, err := parseIPNets(req.ExcludedIPs)
	if err != nil {
		return nil, err
	}
	if err := validateURLs(req.CRLURLs); err != nil {
		return nil, err
	}
	if err := validateURLs(req.OCSPURLs); err != nil {
		return nil, err
	}
	if err := validateOptionalURL(req.IssuerURL); err != nil {
		return nil, err
	}
	if err := validateOptionalURL(req.CPSURL); err != nil {
		return nil, err
	}
	policyExtension, err := buildCertificatePoliciesExtension(req.PolicyOIDs, strings.TrimSpace(req.CPSURL))
	if err != nil {
		return nil, err
	}
	serial, err := parseCASerial(req.SerialNumber)
	if err != nil {
		return nil, err
	}
	if serial == nil {
		serial, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, i18n.E("service.private_ca.generate_serial_failed", "error", err.Error())
		}
		// 保证序列号最高位为 1，避免出现 0 或 DER 中需要额外补零的负数编码。
		serial.SetBit(serial, 127, 1)
	}

	privateKey, err := generateCAKey(keyType)
	if err != nil {
		return nil, i18n.E("service.private_ca.generate_key_failed", "error", err.Error())
	}

	subject := pkix.Name{CommonName: commonName}
	if organization := strings.TrimSpace(req.Organization); organization != "" {
		subject.Organization = []string{organization}
	}
	if unit := strings.TrimSpace(req.OrganizationalUnit); unit != "" {
		subject.OrganizationalUnit = []string{unit}
	}
	if state := strings.TrimSpace(req.State); state != "" {
		subject.Province = []string{state}
	}
	if locality := strings.TrimSpace(req.Locality); locality != "" {
		subject.Locality = []string{locality}
	}
	if country != "" {
		subject.Country = []string{country}
	}
	if email != "" {
		subject.ExtraNames = append(subject.ExtraNames, pkix.AttributeTypeAndValue{Type: oidEmailAddress, Value: email})
	}

	now := time.Now().UTC()
	notAfter := now.AddDate(0, 0, days)

	var parentCert *x509.Certificate
	var parentKey crypto.PrivateKey
	if strings.TrimSpace(req.ParentCertPEM) != "" || strings.TrimSpace(req.ParentKeyPEM) != "" {
		if strings.TrimSpace(req.ParentCertPEM) == "" || strings.TrimSpace(req.ParentKeyPEM) == "" {
			return nil, i18n.E("service.private_ca.parent_incomplete")
		}
		parentCert, parentKey, err = loadParentCA(req.ParentCertPEM, req.ParentKeyPEM)
		if err != nil {
			return nil, err
		}
		if notAfter.After(parentCert.NotAfter) {
			notAfter = parentCert.NotAfter
		}
		// 下级 CA 的路径长度不能超过上级 CA 剩余可嵌套层数。
		if parentCert.MaxPathLen > 0 {
			maxChildPathLen := parentCert.MaxPathLen - 1
			if pathLen < 0 || pathLen > maxChildPathLen {
				pathLen = maxChildPathLen
				pathLenZero = pathLen == 0
			}
		}
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              keyUsage,
		ExtKeyUsage:           extKeyUsage,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            pathLen,
		MaxPathLenZero:        pathLenZero,
		SubjectKeyId:          subjectKeyID(privateKey),
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
		CRLDistributionPoints: trimList(req.CRLURLs),
		OCSPServer:            trimList(req.OCSPURLs),
	}
	if email != "" {
		template.EmailAddresses = []string{email}
	}
	if issuerURL := strings.TrimSpace(req.IssuerURL); issuerURL != "" {
		template.IssuingCertificateURL = []string{issuerURL}
	}
	if len(req.PermittedDNS) > 0 || len(req.ExcludedDNS) > 0 || len(permittedIPs) > 0 || len(excludedIPs) > 0 {
		template.PermittedDNSDomains = trimList(req.PermittedDNS)
		template.ExcludedDNSDomains = trimList(req.ExcludedDNS)
		template.PermittedIPRanges = permittedIPs
		template.ExcludedIPRanges = excludedIPs
		template.PermittedDNSDomainsCritical = len(req.PermittedDNS) > 0 || len(req.ExcludedDNS) > 0
	}
	if policyExtension != nil {
		template.ExtraExtensions = append(template.ExtraExtensions, *policyExtension)
	}

	issuerCert := template
	signer := privateKey
	if parentCert != nil {
		issuerCert = parentCert
		signer = parentKey
		template.AuthorityKeyId = parentCert.SubjectKeyId
		if len(template.AuthorityKeyId) == 0 {
			template.AuthorityKeyId = subjectKeyID(parentKey)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuerCert, publicKey(privateKey), signer)
	if err != nil {
		return nil, i18n.E("service.private_ca.create_cert_failed", "error", err.Error())
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, i18n.E("service.private_ca.create_cert_failed", "error", err.Error())
	}
	keyPEM, err := encodePrivateKey(privateKey)
	if err != nil {
		return nil, i18n.E("service.private_ca.encode_key_failed", "error", err.Error())
	}

	caType := "root"
	if parentCert != nil {
		caType = "intermediate"
	}

	return &model.PrivateCA{
		Name:         name,
		CommonName:   commonName,
		Organization: strings.Join(cert.Subject.Organization, ", "),
		Country:      strings.Join(cert.Subject.Country, ", "),
		KeyType:      privateCAKeyType(cert.PublicKey),
		Source:       "generated",
		Serial:       strings.ToUpper(cert.SerialNumber.Text(16)),
		ParentID:     req.ParentID,
		CAType:       caType,
		PathLen:      pathLen,
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		KeyPEM:       string(keyPEM),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
	}, nil
}

// ParseImportedPrivateCA 解析并校验导入的 CA 证书与私钥。
func ParseImportedPrivateCA(name, certPEM, keyPEM string) (*model.PrivateCA, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, i18n.E("service.private_ca.name_required")
	}
	if strings.TrimSpace(certPEM) == "" {
		return nil, i18n.E("service.private_ca.cert_required")
	}
	if strings.TrimSpace(keyPEM) == "" {
		return nil, i18n.E("service.private_ca.key_required")
	}

	privateKey, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	cert, err := parseCACertificate(certPEM, privateKey)
	if err != nil {
		return nil, err
	}
	if time.Now().After(cert.NotAfter) {
		return nil, i18n.E("service.private_ca.cert_expired")
	}
	if !publicKeysEqual(cert.PublicKey, publicKey(privateKey)) {
		return nil, i18n.E("service.private_ca.key_mismatch")
	}
	encodedKey, err := encodePrivateKey(privateKey)
	if err != nil {
		return nil, i18n.E("service.private_ca.encode_key_failed", "error", err.Error())
	}

	commonName := strings.TrimSpace(cert.Subject.CommonName)
	if commonName == "" {
		commonName = name
	}
	caType := "intermediate"
	if cert.Subject.String() == cert.Issuer.String() {
		caType = "root"
	}
	return &model.PrivateCA{
		Name:         name,
		CommonName:   commonName,
		Organization: strings.Join(cert.Subject.Organization, ", "),
		Country:      strings.Join(cert.Subject.Country, ", "),
		KeyType:      privateCAKeyType(cert.PublicKey),
		Source:       "imported",
		Serial:       strings.ToUpper(cert.SerialNumber.Text(16)),
		CAType:       caType,
		PathLen:      certificatePathLen(cert),
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		KeyPEM:       string(encodedKey),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
	}, nil
}

// ValidateChildCA 校验导入的中间 CA 确实由指定上级 CA 签发。
func ValidateChildCA(childPEM, parentPEM string) error {
	child, err := parseFirstCertificate(childPEM)
	if err != nil {
		return err
	}
	parent, err := parseFirstCertificate(parentPEM)
	if err != nil {
		return err
	}
	if !parent.IsCA || (parent.KeyUsage != 0 && parent.KeyUsage&x509.KeyUsageCertSign == 0) {
		return i18n.E("service.private_ca.cert_not_ca")
	}
	if time.Now().After(parent.NotAfter) {
		return i18n.E("service.private_ca.parent_expired")
	}
	if parent.MaxPathLenZero && parent.MaxPathLen == 0 {
		return i18n.E("service.private_ca.parent_no_path")
	}
	if err := child.CheckSignatureFrom(parent); err != nil {
		return i18n.E("service.private_ca.import_parent_mismatch")
	}
	return nil
}

func certificatePathLen(cert *x509.Certificate) int {
	if cert.MaxPathLenZero {
		return 0
	}
	if cert.MaxPathLen > 0 {
		return cert.MaxPathLen
	}
	return -1
}

func parseFirstCertificate(value string) (*x509.Certificate, error) {
	rest := []byte(value)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, i18n.E("service.private_ca.cert_parse_failed", "error", err.Error())
		}
		return cert, nil
	}
	return nil, i18n.E("service.private_ca.cert_parse_failed", "error", "no certificate block")
}

func loadParentCA(certPEM, keyPEM string) (*x509.Certificate, crypto.PrivateKey, error) {
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, nil, err
	}
	cert, err := parseCACertificate(certPEM, key)
	if err != nil {
		return nil, nil, err
	}
	if time.Now().After(cert.NotAfter) {
		return nil, nil, i18n.E("service.private_ca.parent_expired")
	}
	if !publicKeysEqual(cert.PublicKey, publicKey(key)) {
		return nil, nil, i18n.E("service.private_ca.key_mismatch")
	}
	if cert.MaxPathLenZero && cert.MaxPathLen == 0 {
		return nil, nil, i18n.E("service.private_ca.parent_no_path")
	}
	return cert, key, nil
}

func validateSubjectField(field, value string, max int) error {
	if utf8.RuneCountInString(strings.TrimSpace(value)) > max {
		return i18n.E("service.private_ca.subject_field_invalid", "field", field, "max", max)
	}
	return nil
}

func parsePathLen(value *int) (int, bool, error) {
	if value == nil {
		return 1, false, nil
	}
	pathLen := *value
	if pathLen < -1 || pathLen > 10 {
		return 0, false, i18n.E("service.private_ca.path_len_range")
	}
	if pathLen == -1 {
		return -1, false, nil
	}
	return pathLen, pathLen == 0, nil
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func parseKeyUsage(values []string) (x509.KeyUsage, error) {
	if len(values) == 0 {
		return x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature, nil
	}
	var usage x509.KeyUsage
	for _, value := range values {
		switch normalizeToken(value) {
		case "digital_signature", "digitalsignature":
			usage |= x509.KeyUsageDigitalSignature
		case "content_commitment", "contentcommitment", "non_repudiation":
			usage |= x509.KeyUsageContentCommitment
		case "key_encipherment", "keyencipherment":
			usage |= x509.KeyUsageKeyEncipherment
		case "data_encipherment", "dataencipherment":
			usage |= x509.KeyUsageDataEncipherment
		case "key_agreement", "keyagreement":
			usage |= x509.KeyUsageKeyAgreement
		case "key_cert_sign", "keycertsign", "cert_sign":
			usage |= x509.KeyUsageCertSign
		case "crl_sign", "crlsign":
			usage |= x509.KeyUsageCRLSign
		case "encipher_only", "encipheronly":
			usage |= x509.KeyUsageEncipherOnly
		case "decipher_only", "decipheronly":
			usage |= x509.KeyUsageDecipherOnly
		default:
			return 0, i18n.E("service.private_ca.key_usage_unsupported", "usage", value)
		}
	}
	if usage&x509.KeyUsageCertSign == 0 {
		return 0, i18n.E("service.private_ca.key_usage_required")
	}
	return usage, nil
}

func parseExtKeyUsage(values []string) ([]x509.ExtKeyUsage, error) {
	out := make([]x509.ExtKeyUsage, 0, len(values))
	for _, value := range values {
		switch normalizeToken(value) {
		case "server_auth", "serverauth":
			out = append(out, x509.ExtKeyUsageServerAuth)
		case "client_auth", "clientauth":
			out = append(out, x509.ExtKeyUsageClientAuth)
		case "code_signing", "codesigning":
			out = append(out, x509.ExtKeyUsageCodeSigning)
		case "email_protection", "emailprotection":
			out = append(out, x509.ExtKeyUsageEmailProtection)
		case "time_stamping", "timestamping":
			out = append(out, x509.ExtKeyUsageTimeStamping)
		case "ocsp_signing", "ocspsigning":
			out = append(out, x509.ExtKeyUsageOCSPSigning)
		case "any":
			out = append(out, x509.ExtKeyUsageAny)
		default:
			return nil, i18n.E("service.private_ca.ext_key_usage_unsupported", "usage", value)
		}
	}
	return out, nil
}

func parseDNSNames(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range trimList(values) {
		name := strings.ToLower(strings.TrimSuffix(value, "."))
		if !validDNSName(name) {
			return nil, i18n.E("service.private_ca.dns_invalid", "domain", value)
		}
		out = append(out, name)
	}
	return out, nil
}

// validDNSName 允许私有 PKI 中常见的单标签域名与通配符域名。
func validDNSName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	name := strings.TrimPrefix(value, "*.")
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func parseIPAddresses(values []string) ([]net.IP, error) {
	out := make([]net.IP, 0, len(values))
	for _, value := range trimList(values) {
		ip := net.ParseIP(value)
		if ip == nil {
			return nil, i18n.E("service.private_ca.ip_invalid", "ip", value)
		}
		out = append(out, ip)
	}
	return out, nil
}

func parseIPNets(values []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range trimList(values) {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, i18n.E("service.private_ca.cidr_invalid", "cidr", value)
		}
		out = append(out, network)
	}
	return out, nil
}

func parseCASerial(value string) (*big.Int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, ":", "")
	if value == "" {
		return nil, i18n.E("service.private_ca.serial_invalid")
	}
	serial := new(big.Int)
	if _, ok := serial.SetString(value, 16); !ok || serial.Sign() <= 0 {
		return nil, i18n.E("service.private_ca.serial_invalid")
	}
	if serial.BitLen() > 160 {
		return nil, i18n.E("service.private_ca.serial_invalid")
	}
	return serial, nil
}

func parseObjectIdentifier(value string) (asn1.ObjectIdentifier, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return nil, i18n.E("service.private_ca.policy_oid_invalid", "oid", value)
	}
	oid := make(asn1.ObjectIdentifier, len(parts))
	for index, part := range parts {
		number, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || number < 0 {
			return nil, i18n.E("service.private_ca.policy_oid_invalid", "oid", value)
		}
		oid[index] = number
	}
	return oid, nil
}

type policyQualifierInfo struct {
	PolicyQualifierID asn1.ObjectIdentifier
	Qualifier         asn1.RawValue
}

type policyInformation struct {
	Policy     asn1.ObjectIdentifier
	Qualifiers []policyQualifierInfo `asn1:"optional"`
}

func buildCertificatePoliciesExtension(oids []string, cps string) (*pkix.Extension, error) {
	oids = trimList(oids)
	if len(oids) == 0 {
		if cps != "" {
			return nil, i18n.E("service.private_ca.policy_oid_required")
		}
		return nil, nil
	}
	infos := make([]policyInformation, 0, len(oids))
	for _, value := range oids {
		oid, err := parseObjectIdentifier(value)
		if err != nil {
			return nil, err
		}
		info := policyInformation{Policy: oid}
		if cps != "" {
			info.Qualifiers = []policyQualifierInfo{{
				PolicyQualifierID: oidPolicyQualifierCPS,
				Qualifier: asn1.RawValue{
					Class: asn1.ClassUniversal,
					Tag:   asn1.TagIA5String,
					Bytes: []byte(cps),
				},
			}}
		}
		infos = append(infos, info)
	}
	der, err := asn1.Marshal(infos)
	if err != nil {
		return nil, i18n.E("service.private_ca.policy_encode_failed", "error", err.Error())
	}
	return &pkix.Extension{Id: oidCertificatePolicies, Value: der}, nil
}

func validateURLs(values []string) error {
	for _, value := range trimList(values) {
		if err := validateOptionalURL(value); err != nil {
			return err
		}
	}
	return nil
}

func validateOptionalURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return i18n.E("service.private_ca.url_invalid", "url", value)
	}
	return nil
}

func trimList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func normalizePrivateCAKeyType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ecdsa", "ecdsa256", "ecdsa-p256":
		return "ecdsa"
	case "ecdsa384", "ecdsa-p384":
		return "ecdsa384"
	case "rsa2048":
		return "rsa2048"
	case "rsa4096":
		return "rsa4096"
	default:
		return ""
	}
}

func generateCAKey(keyType string) (crypto.PrivateKey, error) {
	switch keyType {
	case "ecdsa384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa2048":
		return rsa.GenerateKey(rand.Reader, 2048)
	case "rsa4096":
		return rsa.GenerateKey(rand.Reader, 4096)
	default:
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
}

func privateCAKeyType(key crypto.PublicKey) string {
	switch value := key.(type) {
	case *ecdsa.PublicKey:
		if value.Curve == elliptic.P384() {
			return "ecdsa384"
		}
		return "ecdsa"
	case *rsa.PublicKey:
		if value.N.BitLen() > 2048 {
			return "rsa4096"
		}
		return "rsa2048"
	default:
		return "unknown"
	}
}

func publicKey(privateKey crypto.PrivateKey) crypto.PublicKey {
	switch key := privateKey.(type) {
	case *ecdsa.PrivateKey:
		return &key.PublicKey
	case *rsa.PrivateKey:
		return &key.PublicKey
	default:
		return nil
	}
}

func publicKeysEqual(first, second crypto.PublicKey) bool {
	if first == nil || second == nil {
		return false
	}
	firstDER, err := x509.MarshalPKIXPublicKey(first)
	if err != nil {
		return false
	}
	secondDER, err := x509.MarshalPKIXPublicKey(second)
	if err != nil {
		return false
	}
	return bytes.Equal(firstDER, secondDER)
}

func subjectKeyID(privateKey crypto.PrivateKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(publicKey(privateKey))
	if err != nil {
		return nil
	}
	sum := sha1.Sum(der)
	return sum[:]
}

func encodePrivateKey(privateKey crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// parseCACertificate 从 PEM 中挑选与私钥匹配的 CA 证书；没有匹配项时返回第一张 CA 证书。
func parseCACertificate(value string, privateKey crypto.PrivateKey) (*x509.Certificate, error) {
	rest := []byte(value)
	hasCertificate := false
	var fallback *x509.Certificate
	public := publicKey(privateKey)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		hasCertificate = true
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, i18n.E("service.private_ca.cert_parse_failed", "error", err.Error())
		}
		if !cert.IsCA || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
			continue
		}
		if fallback == nil {
			fallback = cert
		}
		if publicKeysEqual(cert.PublicKey, public) {
			return cert, nil
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	if !hasCertificate {
		return nil, i18n.E("service.private_ca.cert_parse_failed", "error", "no certificate block")
	}
	return nil, i18n.E("service.private_ca.cert_not_ca")
}

func parsePrivateKey(value string) (crypto.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, i18n.E("service.private_ca.key_parse_failed", "error", "no pem block")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, i18n.E("service.private_ca.key_parse_failed", "error", "unsupported private key format")
}

func isAlpha(value string) bool {
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
