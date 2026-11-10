package acme

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTP01Validator 执行 RFC 8555 HTTP-01 验证。
// 它先解析域名并校验目标 IP，再直接拨号该 IP，避免 DNS rebinding 和 SSRF。
type HTTP01Validator struct {
	Resolver     *net.Resolver
	Port         int
	AllowPrivate bool
	AllowedCIDRs []string
	AllowedPorts []string
	Timeout      time.Duration
	MaxRedirects int
	MaxBody      int64
}

// NewHTTP01Validator 返回带安全默认值的验证器。
func NewHTTP01Validator() *HTTP01Validator {
	return &HTTP01Validator{
		Resolver:     net.DefaultResolver,
		Port:         80,
		AllowedPorts: []string{"80", "443"},
		Timeout:      10 * time.Second,
		MaxRedirects: 5,
		MaxBody:      16 * 1024,
	}
}

// Validate 请求 http://<identifier>/.well-known/acme-challenge/<token> 并比对 keyAuthorization。
func (v *HTTP01Validator) Validate(ctx context.Context, identifier, token, keyAuthorization string) error {
	host, err := normalizeHTTP01Host(identifier)
	if err != nil {
		return err
	}
	if !validHTTP01Token(token) {
		return newProblemError(ProblemMalformed, "invalid HTTP-01 token", 400)
	}
	if strings.TrimSpace(keyAuthorization) == "" {
		return newProblemError(ProblemMalformed, "empty key authorization", 400)
	}

	port := v.Port
	if port <= 0 {
		port = 80
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/.well-known/acme-challenge/" + token,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return newProblemError(ProblemMalformed, "invalid HTTP-01 URL: "+err.Error(), 400)
	}
	if strings.Contains(host, ":") {
		req.Host = "[" + host + "]"
	} else {
		req.Host = host
	}
	req.Header.Set("User-Agent", "LACTSSL-ACME/1.0")

	client := &http.Client{
		Timeout:   timeout,
		Transport: v.transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			limit := v.MaxRedirects
			if limit <= 0 {
				limit = 5
			}
			if len(via) >= limit {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("unsupported redirect scheme")
			}
			if !v.portAllowed(req.URL.Port(), req.URL.Scheme) {
				return fmt.Errorf("redirect port is not allowed")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		var problem *ProblemError
		if errors.As(err, &problem) {
			return problem
		}
		return newProblemError(ProblemConnection, "HTTP-01 request failed: "+err.Error(), 400)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return newProblemError(ProblemIncorrectResponse, fmt.Sprintf("HTTP-01 endpoint returned status %d", resp.StatusCode), 400)
	}
	limit := v.MaxBody
	if limit <= 0 {
		limit = 16 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return newProblemError(ProblemConnection, "failed to read HTTP-01 response: "+err.Error(), 400)
	}
	if int64(len(body)) > limit {
		return newProblemError(ProblemIncorrectResponse, "HTTP-01 response is too large", 400)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(keyAuthorization) {
		return newProblemError(ProblemIncorrectResponse, "HTTP-01 response does not match key authorization", 400)
	}
	return nil
}

func (v *HTTP01Validator) transport() *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, newProblemError(ProblemConnection, "invalid HTTP-01 dial address", 400)
			}
			if !v.portAllowed(port, "http") {
				return nil, newProblemError(ProblemConnection, "HTTP-01 port is not allowed", 400)
			}
			ips, err := v.resolveAllowedIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				dialer := &net.Dialer{Timeout: 5 * time.Second}
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr != nil {
				return nil, newProblemError(ProblemConnection, "HTTP-01 connection failed: "+lastErr.Error(), 400)
			}
			return nil, newProblemError(ProblemConnection, "HTTP-01 host has no allowed address", 400)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

func (v *HTTP01Validator) resolveAllowedIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if !v.ipAllowed(ip) {
			return nil, newProblemError(ProblemUnauthorized, "HTTP-01 target IP is not allowed", 400)
		}
		return []net.IP{ip}, nil
	}
	resolver := v.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, newProblemError(ProblemDNS, "failed to resolve HTTP-01 host: "+err.Error(), 400)
	}
	out := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if v.ipAllowed(address.IP) {
			out = append(out, address.IP)
		}
	}
	if len(out) == 0 {
		return nil, newProblemError(ProblemUnauthorized, "HTTP-01 target has no allowed address", 400)
	}
	return out, nil
}

func (v *HTTP01Validator) ipAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	networks, err := parseCIDRs(v.AllowedCIDRs)
	if err != nil {
		return false
	}
	if len(networks) > 0 {
		for _, network := range networks {
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}
	if ip.IsLoopback() {
		return false
	}
	if ip.IsPrivate() {
		return v.AllowPrivate
	}
	return true
}

func (v *HTTP01Validator) portAllowed(port, scheme string) bool {
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if v.Port > 0 && port == strconv.Itoa(v.Port) {
		return true
	}
	allowed := v.AllowedPorts
	if len(allowed) == 0 {
		allowed = []string{"80", "443"}
	}
	for _, item := range allowed {
		if strings.TrimSpace(item) == port {
			return true
		}
	}
	return false
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		out = append(out, network)
	}
	return out, nil
}

func normalizeHTTP01Host(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 {
		return "", newProblemError(ProblemUnsupportedIdentifier, "invalid HTTP-01 identifier", 400)
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	if strings.ContainsAny(value, "/:@?#[]") {
		return "", newProblemError(ProblemUnsupportedIdentifier, "invalid HTTP-01 identifier", 400)
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return "", newProblemError(ProblemUnsupportedIdentifier, "invalid HTTP-01 identifier", 400)
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", newProblemError(ProblemUnsupportedIdentifier, "invalid HTTP-01 identifier", 400)
			}
		}
	}
	return value, nil
}

func validHTTP01Token(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
