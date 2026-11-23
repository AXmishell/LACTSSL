// Package httpapi 提供重构 DNS provider 共用的最小 HTTP 客户端。
//
// 只依赖标准库：统一超时、错误体截断、JSON/XML 解码与请求构造。
// 签名逻辑放在各自的 sdk/<vendor> 包中，通过 NewRequest/Send 组合。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/outbound"
)

const (
	defaultTimeout = 30 * time.Second
	defaultMaxBody = 4 << 20
	maxErrorBody   = 4096
)

// Client 是最小 HTTP 客户端。
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	MaxBody   int64
}

// Response 是已读取完毕的响应。
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Error 表示非 2xx 响应。
type Error struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (e *Error) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	if body == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, body)
}

// New 创建带默认超时的客户端。
// 使用 outbound 的安全拨号器（拒绝元数据/链路本地地址、不跟随重定向），
// 避免原生 DNS provider 或云 API 在遇到重定向/代理时把凭据带到别处。
func New(baseURL string) *Client {
	return &Client{
		HTTP:      outbound.NewClient(defaultTimeout, false),
		BaseURL:   strings.TrimRight(baseURL, "/"),
		UserAgent: "lactssl-native/1.0",
		MaxBody:   defaultMaxBody,
	}
}

// NewRequest 构造请求。path 可以是以 / 开头的绝对路径，也可以是完整 URL。
func (c *Client) NewRequest(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*http.Request, error) {
	rawURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		base := strings.TrimRight(c.BaseURL, "/")
		if base == "" {
			return nil, errors.New("httpapi: base URL is empty")
		}
		rawURL = base + "/" + strings.TrimLeft(path, "/")
	}
	if len(query) > 0 {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		values := parsed.Query()
		for key, list := range query {
			values[key] = append([]string(nil), list...)
		}
		parsed.RawQuery = values.Encode()
		rawURL = parsed.String()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	req.GetBody = func() (io.ReadCloser, error) {
		if body == nil {
			return http.NoBody, nil
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return req, nil
}

// Send 发送请求并读取有上限的响应体。
func (c *Client) Send(req *http.Request) (*Response, error) {
	client := c.HTTP
	if client == nil {
		client = outbound.NewClient(defaultTimeout, false)
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error 会包含完整 URL；Alibaba RPC 等把签名放在 query 中。
		return nil, errors.New(outbound.SafeError(err))
	}
	defer resp.Body.Close()
	limit := c.MaxBody
	if limit <= 0 {
		limit = defaultMaxBody
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("httpapi: response body exceeds %d bytes", limit)
	}
	out := &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &Error{
			Method: req.Method,
			URL:    outbound.RedactURL(req.URL.String()),
			Status: resp.StatusCode,
			Body:   string(body),
		}
	}
	return out, nil
}

// Do 是 NewRequest + Send 的便捷封装。
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*Response, error) {
	req, err := c.NewRequest(ctx, method, path, query, header, body)
	if err != nil {
		return nil, err
	}
	return c.Send(req)
}

// JSON 解码响应体。
func JSON(body []byte, out any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("httpapi: decode JSON: %w", err)
	}
	return nil
}

// XML 解码响应体。
func XML(body []byte, out any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := xml.Unmarshal(body, out); err != nil {
		return fmt.Errorf("httpapi: decode XML: %w", err)
	}
	return nil
}

// EncodeJSON 编码 JSON 请求体。
func EncodeJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// EncodeXML 编码 XML 请求体。
func EncodeXML(v any) ([]byte, error) {
	return xml.Marshal(v)
}

// IsNotFound 判断错误是否为 404。
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsConflict 判断错误是否为 409。
func IsConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// IsRateLimited 判断错误是否为 429 或 5xx。
func IsRateLimited(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusTooManyRequests || e.Status >= 500)
}

// CloneHeader 复制 header，避免签名过程修改调用方数据。
func CloneHeader(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}
