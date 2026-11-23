// Package otp 实现 RFC 6238 TOTP，用于需要二次验证的面板登录。
package otp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Code 生成默认 6 位、30 秒步长的 TOTP 验证码。
func Code(secret string, now time.Time) (string, error) {
	return CodeWith(secret, now, 6, 30*time.Second)
}

// CodeWith 生成指定位数与步长的 TOTP 验证码。
func CodeWith(secret string, now time.Time, digits int, period time.Duration) (string, error) {
	if digits < 6 || digits > 8 {
		return "", fmt.Errorf("otp: digits must be between 6 and 8")
	}
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("otp: secret is empty")
	}
	if period <= 0 {
		period = 30 * time.Second
	}
	if period < time.Second {
		return "", fmt.Errorf("otp: period must be at least 1s")
	}
	normalized := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "", "\n", "").Replace(strings.TrimSpace(secret)))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil {
		// 兼容带 = 填充的密钥。
		key, err = base32.StdEncoding.DecodeString(normalized)
		if err != nil {
			return "", fmt.Errorf("otp: invalid base32 secret: %w", err)
		}
	}
	counter := uint64(now.UTC().Unix() / int64(period/time.Second))
	var input [8]byte
	binary.BigEndian.PutUint64(input[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(input[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%mod), nil
}
