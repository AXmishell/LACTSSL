// Package tencentcloud 实现腾讯云 API 3.0（TC3-HMAC-SHA256）请求签名。
//
// 只覆盖部署插件需要的 JSON POST 子集：固定服务端点、可选的 region、
// 临时凭据 SessionToken，以及统一解析 {"Response":{...}} 响应包。
package tencentcloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/validate"
)

const (
	algorithm    = "TC3-HMAC-SHA256"
	dateFormat   = "20060102"
	timeFormat   = "20060102T150405Z"
	defaultLimit = 8 << 20
)

// Credentials 是腾讯云静态凭据。
type Credentials struct {
	SecretID  string
	SecretKey string
	Token     string
}

// Client 是带签名的腾讯云 JSON API 客户端。
type Client struct {
	HTTP        *http.Client
	Credentials Credentials
	Endpoint    string // 留空时按 <service>.<region>.tencentcloudapi.com 生成
	Scheme      string // https 或 http，默认 https
	Now         func() time.Time
	MaxBody     int64
}

// APIError 是腾讯云返回的结构化错误。
type APIError struct {
	Action    string
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Action + " 失败: " + e.Message
	}
	return e.Action + " 失败: " + e.Code + ": " + e.Message
}

// envelope 是腾讯云统一的响应包装。
type envelope struct {
	Response struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		RequestID string `json:"RequestId"`
	} `json:"Response"`
}

// Do 调用一个 JSON API，并把响应中的 Response 字段解码到 out。
// out 为 nil 时只校验响应是否成功。
func (c *Client) Do(ctx context.Context, service, region, action, version string, payload any, out any) (string, error) {
	if c.Credentials.SecretID == "" || c.Credentials.SecretKey == "" {
		return "", fmt.Errorf("tencentcloud: secret id and secret key are required")
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return "", err
		}
	} else {
		body = []byte("{}")
	}

	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		scheme := strings.TrimSpace(c.Scheme)
		if scheme == "" {
			scheme = "https"
		}
		host := service + ".tencentcloudapi.com"
		if strings.TrimSpace(region) != "" {
			if err := validate.Region(region); err != nil {
				return "", err
			}
			host = service + "." + strings.TrimSpace(region) + ".tencentcloudapi.com"
		}
		endpoint = scheme + "://" + host
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}
	if err := c.sign(req, service, body); err != nil {
		return "", err
	}

	client := c.HTTP
	if client == nil {
		client = outbound.NewClient(30*time.Second, false)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	limit := c.MaxBody
	if limit <= 0 {
		limit = defaultLimit
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > limit {
		return "", fmt.Errorf("tencentcloud: response body exceeds %d bytes", limit)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return "", fmt.Errorf("tencentcloud: empty or invalid JSON response")
	}
	requestID := env.Response.RequestID
	if requestID == "" {
		return "", fmt.Errorf("tencentcloud: %s response is missing RequestId", action)
	}
	if env.Response.Error != nil {
		return requestID, &APIError{
			Action: action, Code: env.Response.Error.Code,
			Message: env.Response.Error.Message, RequestID: requestID,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return requestID, fmt.Errorf("tencentcloud: %s HTTP %d: %s", action, resp.StatusCode, truncate(string(raw), 400))
	}
	if out != nil {
		// 优先解码 Response 内部字段；调用方传入的都是业务结构。
		var inner struct {
			Response json.RawMessage `json:"Response"`
		}
		if err := json.Unmarshal(raw, &inner); err == nil && len(inner.Response) > 0 {
			if err := json.Unmarshal(inner.Response, out); err != nil {
				return requestID, err
			}
		} else if err := json.Unmarshal(raw, out); err != nil {
			return requestID, err
		}
	}
	return requestID, nil
}

func (c *Client) sign(req *http.Request, service string, payload []byte) error {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().UTC()
	timestamp := t.Unix()
	date := t.Format(dateFormat)

	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	if c.Credentials.Token != "" {
		req.Header.Set("X-TC-Token", c.Credentials.Token)
	}
	req.Header.Set("Host", req.URL.Host)

	canonicalHeaders := "content-type:" + strings.TrimSpace(req.Header.Get("Content-Type")) + "\n" +
		"host:" + req.URL.Host + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQuery(req.URL.RawQuery),
		canonicalHeaders,
		signedHeaders,
		sha256Hex(payload),
	}, "\n")

	scope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		algorithm,
		fmt.Sprintf("%d", timestamp),
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+c.Credentials.SecretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, c.Credentials.SecretID, scope, signedHeaders, signature,
	))
	return nil
}

func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
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
	var b strings.Builder
	for i, key := range keys {
		for j, value := range values[key] {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(queryEscape(key))
			b.WriteByte('=')
			b.WriteString(queryEscape(value))
		}
	}
	return b.String()
}

func queryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
