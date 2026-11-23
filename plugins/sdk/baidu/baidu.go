// Package baidu 实现百度智能云 BCE V1 请求签名。
package baidu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	algorithm       = "bce-auth-v1"
	defaultExpire   = 1800
	defaultDateTime = "2006-01-02T15:04:05Z"
)

// Signer 给百度智能云请求添加 Authorization 与 x-bce-date。
type Signer struct {
	AccessKey     string
	SecretKey     string
	ExpireSeconds int
	Now           func() time.Time
}

// Sign 计算 BCE V1 签名。请求体需要由调用方设置 Content-Type 与 Content-Length。
func (s Signer) Sign(req *http.Request, body []byte) error {
	if s.AccessKey == "" || s.SecretKey == "" {
		return fmt.Errorf("baidu: access key and secret key are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	date := now().UTC().Format(defaultDateTime)
	if req.Header.Get("x-bce-date") == "" {
		req.Header.Set("x-bce-date", date)
	}
	date = req.Header.Get("x-bce-date")
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	contentLength := req.ContentLength
	if contentLength < 0 {
		contentLength = 0
	}

	expire := s.ExpireSeconds
	if expire <= 0 {
		expire = defaultExpire
	}
	signKeyInfo := fmt.Sprintf("%s/%s/%s/%d", algorithm, s.AccessKey, date, expire)
	signKey := hmacSha256Hex(s.SecretKey, signKeyInfo)

	canonicalHeaders, signedHeaders := canonicalHeaders(req, contentLength, body != nil)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQuery(req.URL.RawQuery),
		canonicalHeaders,
	}, "\n")
	signature := hmacSha256Hex(signKey, canonicalRequest)
	req.Header.Set("Authorization", signKeyInfo+"/"+strings.Join(signedHeaders, ";")+"/"+signature)
	return nil
}

func canonicalURI(path string) string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "/"
	}
	return "/" + uriEncode(path, false)
}

func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0)
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, uriEncode(key, true)+"="+uriEncode(value, true))
		}
	}
	return strings.Join(parts, "&")
}

func canonicalHeaders(req *http.Request, contentLength int64, hasBody bool) (string, []string) {
	values := map[string]string{
		"host":         req.URL.Host,
		"content-type": req.Header.Get("Content-Type"),
	}
	if hasBody {
		values["content-length"] = strconv.FormatInt(contentLength, 10)
	}
	for key, list := range req.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-bce-") && lower != "x-bce-request-id" {
			values[lower] = strings.Join(list, ",")
		}
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, uriEncode(key, true)+":"+uriEncode(strings.TrimSpace(values[key]), true))
	}
	return strings.Join(parts, "\n"), keys
}

func uriEncode(value string, encodeSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSha256Hex(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
