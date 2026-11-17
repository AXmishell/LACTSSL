package acme

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"strings"
	"time"
)

// DNS01Validator 执行 RFC 8555 DNS-01 验证。
type DNS01Validator struct {
	Resolver  *net.Resolver
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	Timeout   time.Duration
	Attempts  int
	Interval  time.Duration
}

// NewDNS01Validator 返回带默认超时与重试的验证器。
func NewDNS01Validator() *DNS01Validator {
	return &DNS01Validator{
		Resolver: net.DefaultResolver,
		Timeout:  15 * time.Second,
		Attempts: 3,
		Interval: 2 * time.Second,
	}
}

// Validate 查询 _acme-challenge.<identifier> 的 TXT 记录。
func (v *DNS01Validator) Validate(ctx context.Context, identifier, keyAuthorization string) error {
	host, err := normalizeDNS01Host(identifier)
	if err != nil {
		return err
	}
	if strings.TrimSpace(keyAuthorization) == "" {
		return newProblemError(ProblemMalformed, "empty key authorization", 400)
	}
	sum := sha256.Sum256([]byte(keyAuthorization))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])

	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	attempts := v.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	interval := v.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	lookup := v.LookupTXT
	if lookup == nil {
		resolver := v.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		lookup = resolver.LookupTXT
	}
	name := "_acme-challenge." + host
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		values, err := lookup(ctx, name)
		if err == nil {
			for _, value := range values {
				if strings.TrimSpace(value) == expected {
					return nil
				}
			}
			lastErr = newProblemError(ProblemIncorrectResponse, "DNS-01 TXT record does not match key authorization", 400)
		} else {
			lastErr = newProblemError(ProblemDNS, "failed to query DNS-01 TXT record: "+err.Error(), 400)
		}
		if attempt+1 >= attempts {
			break
		}
		select {
		case <-ctx.Done():
			return newProblemError(ProblemDNS, "DNS-01 validation timed out", 400)
		case <-time.After(interval):
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return newProblemError(ProblemIncorrectResponse, "DNS-01 TXT record not found", 400)
}

func normalizeDNS01Host(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	value = strings.TrimPrefix(value, "*.")
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/:@?#[]") {
		return "", newProblemError(ProblemUnsupportedIdentifier, "invalid DNS-01 identifier", 400)
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return "", newProblemError(ProblemUnsupportedIdentifier, "invalid DNS-01 identifier", 400)
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", newProblemError(ProblemUnsupportedIdentifier, "invalid DNS-01 identifier", 400)
			}
		}
	}
	return value, nil
}
