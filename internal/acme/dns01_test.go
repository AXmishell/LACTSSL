package acme

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
)

func TestDNS01ValidateSuccess(t *testing.T) {
	keyAuth := "token-123.thumbprint"
	sum := sha256.Sum256([]byte(keyAuth))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	v := NewDNS01Validator()
	v.Attempts = 1
	v.LookupTXT = func(ctx context.Context, name string) ([]string, error) {
		if name != "_acme-challenge.example.com" {
			t.Fatalf("TXT 查询名称不正确: %s", name)
		}
		return []string{"other", expected}, nil
	}
	if err := v.Validate(context.Background(), "example.com", keyAuth); err != nil {
		t.Fatalf("DNS-01 验证失败: %v", err)
	}
}

func TestDNS01ValidateWildcard(t *testing.T) {
	keyAuth := "token-123.thumbprint"
	sum := sha256.Sum256([]byte(keyAuth))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	v := NewDNS01Validator()
	v.Attempts = 1
	v.LookupTXT = func(ctx context.Context, name string) ([]string, error) {
		if name != "_acme-challenge.example.com" {
			t.Fatalf("通配符应去掉 *.: %s", name)
		}
		return []string{expected}, nil
	}
	if err := v.Validate(context.Background(), "*.example.com", keyAuth); err != nil {
		t.Fatalf("通配符 DNS-01 验证失败: %v", err)
	}
}

func TestDNS01ValidateMismatch(t *testing.T) {
	v := NewDNS01Validator()
	v.Attempts = 1
	v.LookupTXT = func(ctx context.Context, name string) ([]string, error) {
		return []string{"wrong"}, nil
	}
	if err := v.Validate(context.Background(), "example.com", "token.thumb"); err == nil {
		t.Fatal("TXT 不匹配应失败")
	}
}

func TestDNS01ValidateLookupError(t *testing.T) {
	v := NewDNS01Validator()
	v.Attempts = 1
	v.LookupTXT = func(ctx context.Context, name string) ([]string, error) {
		return nil, errors.New("no such host")
	}
	if err := v.Validate(context.Background(), "example.com", "token.thumb"); err == nil {
		t.Fatal("DNS 查询失败应返回错误")
	}
}
