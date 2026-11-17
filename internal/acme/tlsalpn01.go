package acme

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/asn1"
	"fmt"
	"net"
	"strings"
	"time"
)

const acmeTLSALPNProtocol = "acme-tls/1"

var oidACMEIdentifier = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}

// TLSALPN01Validator 执行 RFC 8737 TLS-ALPN-01 验证。
type TLSALPN01Validator struct {
	Resolver     *net.Resolver
	Port         int
	AllowPrivate bool
	AllowedCIDRs []string
	Timeout      time.Duration
}

// NewTLSALPN01Validator 返回带安全默认值的验证器。
func NewTLSALPN01Validator() *TLSALPN01Validator {
	return &TLSALPN01Validator{
		Resolver: net.DefaultResolver,
		Port:     443,
		Timeout:  10 * time.Second,
	}
}

// Validate 连接目标 443 端口并校验 acme-tls/1 挑战证书扩展。
func (v *TLSALPN01Validator) Validate(ctx context.Context, identifier, keyAuthorization string) error {
	host, sni, ip, err := normalizeTLSALPNIdentifier(identifier)
	if err != nil {
		return err
	}
	if strings.TrimSpace(keyAuthorization) == "" {
		return newProblemError(ProblemMalformed, "empty key authorization", 400)
	}
	port := v.Port
	if port <= 0 {
		port = 443
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	network := &HTTP01Validator{
		Resolver:     v.Resolver,
		AllowPrivate: v.AllowPrivate,
		AllowedCIDRs: v.AllowedCIDRs,
	}
	ips, err := network.resolveAllowedIPs(ctx, host)
	if err != nil {
		return err
	}
	expected := sha256.Sum256([]byte(keyAuthorization))
	var lastErr error
	for _, target := range ips {
		if ip != nil && !target.Equal(ip) {
			continue
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.String(), fmt.Sprintf("%d", port)))
		if err != nil {
			lastErr = newProblemError(ProblemConnection, "TLS-ALPN-01 connection failed: "+err.Error(), 400)
			continue
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			NextProtos:         []string{acmeTLSALPNProtocol},
			InsecureSkipVerify: true, // 挑战证书是自签名证书，不能按公共信任链校验。
			VerifyConnection: func(state tls.ConnectionState) error {
				return verifyTLSALPNState(state, host, ip, expected[:])
			},
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = tlsConn.Close()
			lastErr = newProblemError(ProblemIncorrectResponse, "TLS-ALPN-01 handshake failed: "+err.Error(), 400)
			continue
		}
		_ = tlsConn.Close()
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return newProblemError(ProblemConnection, "TLS-ALPN-01 target has no allowed address", 400)
}

func verifyTLSALPNState(state tls.ConnectionState, host string, ip net.IP, expected []byte) error {
	if state.NegotiatedProtocol != acmeTLSALPNProtocol {
		return fmt.Errorf("server did not negotiate acme-tls/1")
	}
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("server returned no certificate")
	}
	leaf := state.PeerCertificates[0]
	if ip != nil {
		found := false
		for _, candidate := range leaf.IPAddresses {
			if candidate.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("challenge certificate does not contain the IP identifier")
		}
	} else if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("challenge certificate does not contain the DNS identifier")
	}
	for _, ext := range leaf.Extensions {
		if !oidACMEIdentifier.Equal(ext.Id) {
			continue
		}
		if !ext.Critical {
			return fmt.Errorf("acmeIdentifier extension is not critical")
		}
		var digest []byte
		if _, err := asn1.Unmarshal(ext.Value, &digest); err != nil {
			return fmt.Errorf("invalid acmeIdentifier extension")
		}
		if subtle.ConstantTimeCompare(digest, expected) != 1 {
			return fmt.Errorf("acmeIdentifier digest does not match key authorization")
		}
		return nil
	}
	return fmt.Errorf("challenge certificate is missing acmeIdentifier extension")
}

func normalizeTLSALPNIdentifier(value string) (host, sni string, ip net.IP, err error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 {
		return "", "", nil, newProblemError(ProblemUnsupportedIdentifier, "invalid TLS-ALPN-01 identifier", 400)
	}
	if parsed := net.ParseIP(value); parsed != nil {
		return parsed.String(), reverseIPName(parsed), parsed, nil
	}
	if strings.ContainsAny(value, "/:@?#[]") {
		return "", "", nil, newProblemError(ProblemUnsupportedIdentifier, "invalid TLS-ALPN-01 identifier", 400)
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return "", "", nil, newProblemError(ProblemUnsupportedIdentifier, "invalid TLS-ALPN-01 identifier", 400)
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", "", nil, newProblemError(ProblemUnsupportedIdentifier, "invalid TLS-ALPN-01 identifier", 400)
			}
		}
	}
	return value, value, nil, nil
}

func reverseIPName(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v4[3], v4[2], v4[1], v4[0])
	}
	hex := fmt.Sprintf("%032x", ip.To16())
	parts := make([]string, 0, 32)
	for i := len(hex) - 1; i >= 0; i-- {
		parts = append(parts, string(hex[i]))
	}
	return strings.Join(parts, ".") + ".ip6.arpa"
}
