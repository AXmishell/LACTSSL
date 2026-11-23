// Package alibaba 实现阿里云 RPC 风格 API 的 HMAC-SHA1 签名。
package alibaba

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/sdk/httpapi"
)

// Signer 是阿里云 RPC 请求签名器。
type Signer struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Now             func() time.Time
}

// Do 发送一个 GET RPC 请求，返回响应体。
func (s Signer) Do(ctx context.Context, client *httpapi.Client, endpoint, action, version string, params map[string]string, out any) error {
	return s.do(ctx, client, http.MethodGet, endpoint, action, version, params, out)
}

// DoPost 发送一个 POST 表单 RPC 请求，返回响应体。
func (s Signer) DoPost(ctx context.Context, client *httpapi.Client, endpoint, action, version string, params map[string]string, out any) error {
	return s.do(ctx, client, http.MethodPost, endpoint, action, version, params, out)
}

func (s Signer) do(ctx context.Context, client *httpapi.Client, method, endpoint, action, version string, params map[string]string, out any) error {
	if s.AccessKeyID == "" || s.AccessKeySecret == "" {
		return fmt.Errorf("alibaba: access key and secret are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	values := url.Values{}
	values.Set("AccessKeyId", s.AccessKeyID)
	values.Set("Action", action)
	values.Set("Format", "JSON")
	values.Set("SignatureMethod", "HMAC-SHA1")
	values.Set("SignatureVersion", "1.0")
	values.Set("SignatureNonce", nonce())
	values.Set("Timestamp", now().UTC().Format("2006-01-02T15:04:05Z"))
	values.Set("Version", version)
	if s.SecurityToken != "" {
		values.Set("SecurityToken", s.SecurityToken)
	}
	for key, value := range params {
		if value != "" {
			values.Set(key, value)
		}
	}

	canonical := canonicalQuery(values)
	stringToSign := method + "&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(s.AccessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	values.Set("Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))

	header := make(http.Header)
	endpoint = strings.TrimRight(endpoint, "/")
	var resp *httpapi.Response
	var err error
	if method == http.MethodPost {
		form := canonicalQuery(values)
		header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err = client.Do(ctx, http.MethodPost, endpoint+"/", nil, header, []byte(form))
	} else {
		resp, err = client.Do(ctx, method, endpoint+"/?"+canonicalQuery(values), nil, header, nil)
	}
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return httpapi.JSON(resp.Body, out)
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, percentEncode(key)+"="+percentEncode(value))
		}
	}
	return strings.Join(parts, "&")
}

func percentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func nonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
