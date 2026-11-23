// Package volcengine 实现火山引擎 HMAC-SHA256 请求签名。
package volcengine

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

// Signer 给火山引擎请求添加签名头。
type Signer struct {
	AccessKey    string
	SecretKey    string
	Region       string
	Service      string
	SessionToken string
	Now          func() time.Time
}

// Sign 计算签名。
func (s Signer) Sign(req *http.Request, body []byte) error {
	if s.AccessKey == "" || s.SecretKey == "" {
		return fmt.Errorf("volcengine: access key and secret key are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	date := now().UTC().Format("20060102T150405Z")
	shortDate := date[:8]
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Date", date)
	req.Header.Set("X-Content-Sha256", payloadHash)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.SessionToken != "" {
		req.Header.Set("X-Security-Token", s.SessionToken)
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	signed := signedHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders(req, signed),
		strings.Join(signed, ";"),
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{shortDate, s.Region, s.Service, "request"}, "/")
	stringToSign := strings.Join([]string{
		"HMAC-SHA256",
		date,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
	key := signingKey(s.SecretKey, shortDate, s.Region, s.Service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, strings.Join(signed, ";"), signature,
	))
	return nil
}

func signedHeaders(req *http.Request) []string {
	set := map[string]bool{
		"host":             true,
		"content-type":     true,
		"x-date":           true,
		"x-content-sha256": true,
	}
	if req.Header.Get("X-Security-Token") != "" {
		set["x-security-token"] = true
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func canonicalHeaders(req *http.Request, signed []string) string {
	values := map[string]string{
		"host":             req.Host,
		"content-type":     req.Header.Get("Content-Type"),
		"x-date":           req.Header.Get("X-Date"),
		"x-content-sha256": req.Header.Get("X-Content-Sha256"),
		"x-security-token": req.Header.Get("X-Security-Token"),
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

func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func canonicalQuery(u *url.URL) string {
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

func escape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte(secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
