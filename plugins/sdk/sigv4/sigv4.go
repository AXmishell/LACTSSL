// Package sigv4 实现 AWS Signature Version 4。
//
// 只覆盖 DNS 插件需要的子集：固定 region/service、host/x-amz-* 头、
// 可选 session token，以及 GET/POST 的 query 与 payload 规范化。
package sigv4

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
	algorithm     = "AWS4-HMAC-SHA256"
	emptyPayload  = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	amzDateFormat = "20060102T150405Z"
	shortDate     = "20060102"
)

// Credentials 是 AWS 静态凭据。
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Signer 给请求添加 SigV4 头。
type Signer struct {
	Region      string
	Service     string
	Credentials Credentials
	Now         func() time.Time
}

// Sign 计算签名并设置 Authorization / X-Amz-Date / X-Amz-Content-Sha256。
func (s Signer) Sign(req *http.Request, payload []byte) error {
	if s.Region == "" || s.Service == "" {
		return fmt.Errorf("sigv4: region and service are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	t := now().UTC()
	amzDate := t.Format(amzDateFormat)
	date := t.Format(shortDate)

	payloadHash := sha256Hex(payload)
	if payload == nil {
		payloadHash = emptyPayload
	}
	req.Header.Set("X-Amz-Date", amzDate)
	if s.Credentials.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.Credentials.SessionToken)
	}
	if payload != nil {
		req.ContentLength = int64(len(payload))
	}

	signedHeaders := collectSignedHeaders(req, payload != nil)
	canonicalHeaders := canonicalHeaders(req, signedHeaders)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{date, s.Region, s.Service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	key := signingKey(s.Credentials.SecretAccessKey, date, s.Region, s.Service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, s.Credentials.AccessKeyID, scope, strings.Join(signedHeaders, ";"), signature,
	))
	return nil
}

func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
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

func collectSignedHeaders(req *http.Request, hasBody bool) []string {
	set := map[string]bool{
		"host":       true,
		"x-amz-date": true,
	}
	if hasBody {
		set["content-length"] = true
	}
	if req.Header.Get("X-Amz-Security-Token") != "" {
		set["x-amz-security-token"] = true
	}
	if req.Header.Get("Content-Type") != "" {
		set["content-type"] = true
	}
	for key := range req.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-amz-") {
			set[lower] = true
		}
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func canonicalHeaders(req *http.Request, signed []string) string {
	values := make(map[string]string, len(signed))
	values["host"] = req.URL.Host
	values["content-length"] = strconv.FormatInt(req.ContentLength, 10)
	for key, list := range req.Header {
		lower := strings.ToLower(key)
		values[lower] = strings.Join(list, ",")
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
			pairs = append(pairs, pair{awsEscape(key), awsEscape(value)})
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

func awsEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
