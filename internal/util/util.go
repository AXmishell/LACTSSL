// Package util 提供加密、域名处理等通用工具。
package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encPrefix = "enc:v1:"

// Cipher 使用 AES-GCM 加密数据库中的敏感配置。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 从任意长度的密钥派生 AES-256 密钥。
func NewCipher(secret []byte) (*Cipher, error) {
	sum := sha256.Sum256(secret)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文并返回带前缀的字符串。
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt 解密字符串；无前缀时按明文返回，便于兼容旧数据。
func (c *Cipher) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, encPrefix) {
		return value, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文长度不正确")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// RandomHex 生成指定字节数的随机十六进制串。
func RandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashToken 对重置令牌等短期凭证做不可逆摘要。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NormalizeDomains 清洗域名列表，去重并去除协议、路径和空白。
func NormalizeDomains(input []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, item := range input {
		for _, part := range strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ';'
		}) {
			d := strings.ToLower(strings.TrimSpace(part))
			d = strings.TrimPrefix(d, "https://")
			d = strings.TrimPrefix(d, "http://")
			if idx := strings.IndexAny(d, "/:"); idx >= 0 {
				d = d[:idx]
			}
			d = strings.TrimSuffix(d, ".")
			if d == "" {
				continue
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}

// ValidDomain 做基础域名格式校验，支持通配符。
func ValidDomain(domain string) bool {
	d := strings.TrimPrefix(domain, "*.")
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") || strings.Contains(d, "..") {
		return false
	}
	for _, part := range strings.Split(d, ".") {
		if part == "" || len(part) > 63 {
			return false
		}
		if strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
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

// MaskSecret 用于日志或提示信息中隐藏敏感值。
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

// JoinURL 拼接基础地址与路径，避免出现双斜杠。
func JoinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		return base
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

// FormatDuration 返回中文可读时长。
func FormatDuration(d interface{ Hours() float64 }) string {
	h := d.Hours()
	if h < 1 {
		return fmt.Sprintf("%.0f 分钟", h*60)
	}
	if h < 24 {
		return fmt.Sprintf("%.1f 小时", h)
	}
	return fmt.Sprintf("%.1f 天", h/24)
}
