// Package aliyunoss 实现阿里云 OSS V1 请求签名。
//
// 部署插件用它给「绑定自定义域名证书」接口签名；签名只依赖标准库。
package aliyunoss

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials 是 OSS 静态凭据。
type Credentials struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
}

// SignV1 计算 OSS V1 签名（Base64 后的 HMAC-SHA1 结果）。
//
// resource 是 CanonicalizedResource，例如 /bucket/?cname&comp=add。
// headers 中所有 x-oss-* 头会按字典序参与签名。
func SignV1(method, contentMD5, contentType, date, resource string, headers http.Header, secret string) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte('\n')
	b.WriteString(contentMD5)
	b.WriteByte('\n')
	b.WriteString(contentType)
	b.WriteByte('\n')
	b.WriteString(date)
	b.WriteByte('\n')

	keys := make([]string, 0, len(headers))
	for key := range headers {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-oss-") {
			keys = append(keys, lower)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(strings.Join(headers.Values(key), ",")))
		b.WriteByte('\n')
	}
	b.WriteString(resource)

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(b.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// SetHeaders 设置签名所需的公共头，并返回 Authorization。
func SetHeaders(req *http.Request, cred Credentials, resource string, bodyMD5 string, contentType string, now time.Time) string {
	date := now.UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", contentType)
	if bodyMD5 != "" {
		req.Header.Set("Content-MD5", bodyMD5)
	}
	if cred.SecurityToken != "" {
		req.Header.Set("x-oss-security-token", cred.SecurityToken)
	}
	signature := SignV1(req.Method, bodyMD5, contentType, date, resource, req.Header, cred.AccessKeySecret)
	return "OSS " + cred.AccessKeyID + ":" + signature
}

// CanonicalResource 生成 OSS CanonicalizedResource。
// subresources 会按字典序以 ?a&b 形式追加，用于 cname、comp 等子资源。
func CanonicalResource(bucket, path string, subresources map[string]string) string {
	path = "/" + strings.TrimLeft(path, "/")
	if path == "/" {
		path = "/"
	}
	resource := "/" + strings.Trim(bucket, "/") + path
	if len(subresources) == 0 {
		return resource
	}
	keys := make([]string, 0, len(subresources))
	for key := range subresources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if subresources[key] == "" {
			parts = append(parts, url.QueryEscape(key))
			continue
		}
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(subresources[key]))
	}
	return resource + "?" + strings.Join(parts, "&")
}
