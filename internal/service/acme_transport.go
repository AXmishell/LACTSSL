package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// maxACMEJSONBody 限制兼容层读取的 JSON 响应大小，避免把证书链等大响应读入内存。
const maxACMEJSONBody = 1 << 20

// upLinkTransport 修复部分 ACME 服务端不返回 challenge 响应
// `Link: <authorization-url>;rel="up"` 的兼容性问题。
//
// RFC 8555 §7.5.1 要求 challenge 资源在提交后返回指向父 authorization 的
// up 链接，lego 依赖该响应头轮询授权状态。零信 ZoTrus 的 cnacme 服务端没有
// 返回这个头，导致 lego 报 `authorization[get]: empty URL`。这里在
// RoundTripper 层记录 authorization 与 challenge 的对应关系，并在缺失时补上
// 响应头；对符合标准的 CA 不会产生任何改动。
type upLinkTransport struct {
	base http.RoundTripper

	mu               sync.Mutex
	authzByChallenge map[string]string
}

// withACMECompatibility 返回带 ACME 上游兼容修复的 HTTP 客户端副本。
func withACMECompatibility(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &upLinkTransport{base: base}
	return &clone
}

func (t *upLinkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if req.Method != http.MethodPost || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}

	// challenge 提交响应缺少 up 链接时，用之前从 authorization 响应中
	// 记录的父子关系补全。
	if !hasUpLink(resp.Header) {
		if parent := t.authorizationFor(req.URL); parent != "" {
			resp.Header.Add("Link", "<"+parent+">;rel=\"up\"")
		}
	}

	// authorization 响应中带有 challenge URL 列表，记录映射供后续使用。
	if isJSONResponse(resp) {
		t.rememberAuthorizations(resp, req.URL)
	}
	return resp, nil
}

func (t *upLinkTransport) rememberAuthorizations(resp *http.Response, authzURL *url.URL) {
	body, err := readLimitedBody(resp)
	if err != nil || len(body) == 0 {
		return
	}
	var payload struct {
		Challenges []struct {
			URL string `json:"url"`
		} `json:"challenges"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Challenges) == 0 {
		return
	}

	authz := acmeURLString(authzURL)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.authzByChallenge == nil {
		t.authzByChallenge = make(map[string]string)
	}
	for _, challenge := range payload.Challenges {
		if strings.TrimSpace(challenge.URL) == "" {
			continue
		}
		ref, err := url.Parse(challenge.URL)
		if err != nil {
			continue
		}
		absolute := authzURL.ResolveReference(ref)
		t.authzByChallenge[normalizeACMEURL(absolute)] = authz
	}
}

func (t *upLinkTransport) authorizationFor(challengeURL *url.URL) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.authzByChallenge[normalizeACMEURL(challengeURL)]
}

func acmeURLString(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	clone.Fragment = ""
	return clone.String()
}

func normalizeACMEURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	clone.Fragment = ""
	clone.Path = strings.TrimRight(clone.Path, "/")
	return clone.String()
}

func hasUpLink(header http.Header) bool {
	for _, value := range header.Values("Link") {
		for _, part := range strings.Split(value, ",") {
			lower := strings.ToLower(strings.TrimSpace(part))
			if strings.Contains(lower, `rel="up"`) || strings.Contains(lower, "rel=up") {
				return true
			}
		}
	}
	return false
}

func isJSONResponse(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json")
}

// readLimitedBody 读取并恢复响应体，避免影响调用方继续读取。
func readLimitedBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil || resp.ContentLength > maxACMEJSONBody {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxACMEJSONBody+1))
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	if len(body) > maxACMEJSONBody {
		// 超过上限时把已读部分与剩余流拼回去，避免截断调用方的响应体。
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return nil, nil
	}
	closeErr := resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return body, closeErr
}
