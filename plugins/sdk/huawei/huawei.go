// Package huawei 实现华为云 SDK-HMAC-SHA256 请求签名。
package huawei

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	algorithm = "SDK-HMAC-SHA256"
	dateFmt   = "20060102T150405Z"
)

// Signer 给华为云请求添加签名头。
type Signer struct {
	AccessKey string
	SecretKey string
	Now       func() time.Time
}

// Sign 设置 X-Sdk-Date 与 Authorization。
func (s Signer) Sign(req *http.Request, payload []byte) error {
	if s.AccessKey == "" || s.SecretKey == "" {
		return fmt.Errorf("huawei: access key and secret key are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	date := now().UTC().Format(dateFmt)
	req.Header.Set("X-Sdk-Date", date)
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	signed := signedHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders(req, signed),
		strings.Join(signed, ";"),
		sha256Hex(payload),
	}, "\n")

	stringToSign := strings.Join([]string{
		algorithm,
		date,
		sha256Hex([]byte(canonical)),
	}, "\n")

	mac := hmac.New(sha256.New, []byte(s.SecretKey))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Access=%s, SignedHeaders=%s, Signature=%s",
		algorithm, s.AccessKey, strings.Join(signed, ";"), signature,
	))
	return nil
}

func signedHeaders(req *http.Request) []string {
	set := map[string]bool{"host": true, "x-sdk-date": true}
	if req.Header.Get("Content-Type") != "" {
		set["content-type"] = true
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}

func canonicalQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return u.RawQuery
	}
	type pair struct{ key, value string }
	var pairs []pair
	for key, list := range values {
		for _, value := range list {
			pairs = append(pairs, pair{escape(key), escape(value)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	out := make([]string, 0, len(pairs))
	for _, item := range pairs {
		out = append(out, item.key+"="+item.value)
	}
	return strings.Join(out, "&")
}

func canonicalHeaders(req *http.Request, signed []string) string {
	values := map[string]string{"host": req.URL.Host}
	for key, list := range req.Header {
		values[strings.ToLower(key)] = strings.Join(list, ",")
	}
	var b strings.Builder
	for _, key := range signed {
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(values[key]))
		b.WriteByte('\n')
	}
	return b.String()
}

func escape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
