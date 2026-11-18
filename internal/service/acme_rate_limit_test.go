package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/acme"
	"lactssl/internal/i18n"
)

func TestRateLimitErrorFromDetail(t *testing.T) {
	detail := "too many certificates (5) already issued for this exact set of identifiers in the last 168h0m0s, retry after 2026-11-20 00:19:14 UTC: see https://letsencrypt.org/docs/rate-limits/"
	err := &acme.RateLimitedError{
		ProblemDetails: &acme.ProblemDetails{
			HTTPStatus: 429,
			Type:       acme.RateLimitedErr,
			Detail:     detail,
		},
	}
	limited := rateLimitErrorFrom(err)
	if limited == nil {
		t.Fatal("应识别 ACME 限流错误")
	}
	want := time.Date(2026, 11, 20, 0, 19, 14, 0, time.UTC)
	if !limited.RetryAt.Equal(want) {
		t.Fatalf("恢复时间解析不正确: want %v, got %v", want, limited.RetryAt)
	}
	if limited.Detail != detail {
		t.Fatalf("原始错误详情丢失: %s", limited.Detail)
	}
	if key, _, ok := i18n.Decode(i18n.EncodeError(limited)); !ok || key != "service.acme.rate_limited" {
		t.Fatalf("限流错误应持久化为可翻译消息: %s", i18n.EncodeError(limited))
	}
}

func TestRateLimitErrorFromRetryAfterHeader(t *testing.T) {
	err := &acme.RateLimitedError{
		ProblemDetails: &acme.ProblemDetails{HTTPStatus: 429, Type: acme.RateLimitedErr},
		RetryAfter:     "90",
	}
	limited := rateLimitErrorFrom(err)
	if limited == nil {
		t.Fatal("应识别 Retry-After 头")
	}
	delay := time.Until(limited.RetryAt)
	if delay < 80*time.Second || delay > 100*time.Second {
		t.Fatalf("Retry-After 解析不正确: %v", delay)
	}
}

func TestRateLimitErrorFromGeneric429(t *testing.T) {
	err := &acme.ProblemDetails{
		HTTPStatus: 429,
		Type:       "urn:ietf:params:acme:error:serverInternal",
		Detail:     "quota exceeded, retry after 2026-11-20 00:19:14 UTC",
	}
	limited := rateLimitErrorFrom(err)
	if limited == nil {
		t.Fatal("通用 429 也应识别为限流")
	}
	want := time.Date(2026, 11, 20, 0, 19, 14, 0, time.UTC)
	if !limited.RetryAt.Equal(want) {
		t.Fatalf("通用 429 的恢复时间解析不正确: %v", limited.RetryAt)
	}
}

func TestRateLimitErrorFromString(t *testing.T) {
	err := fmt.Errorf("acme: error: 429 :: POST :: https://ca/acme/new-order :: urn:ietf:params:acme:error:rateLimited :: too many certificates (5) already issued for this exact set of identifiers in the last 168h0m0s, retry after 2026-11-20 00:19:14 UTC")
	limited := rateLimitErrorFrom(err)
	if limited == nil {
		t.Fatal("字符串错误中的限流信息也应被识别")
	}
	want := time.Date(2026, 11, 20, 0, 19, 14, 0, time.UTC)
	if !limited.RetryAt.Equal(want) {
		t.Fatalf("字符串中的恢复时间解析不正确: %v", limited.RetryAt)
	}

	if rateLimitErrorFrom(errors.New("network unreachable")) != nil {
		t.Fatal("普通错误不应被识别为限流")
	}
}
