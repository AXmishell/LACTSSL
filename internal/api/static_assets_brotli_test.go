package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
)

// TestServeAssetNegotiatesPrecompressedBrotli 验证生产构建的 .br 资源：
// 支持 br 的客户端直接拿到压缩表示，不支持的客户端得到流式解压后的原文。
func TestServeAssetNegotiatesPrecompressedBrotli(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const appJS = "console.log('precompressed')"
	s := New(
		nil,
		auth.New([]byte("jwt-secret")),
		nil,
		"secret-entry",
		fstest.MapFS{
			"index.html":                &fstest.MapFile{Data: []byte("<html><head></head></html>")},
			"assets/app-abc12345.js.br": &fstest.MapFile{Data: brotliTestData(t, appJS)},
			"assets/raw-B7kXp2Qa.js":    &fstest.MapFile{Data: []byte("console.log('raw')")},
		},
		nil,
	)
	router := s.Router()

	compressed := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"Accept-Encoding": "br, gzip"})
	if compressed.Code != http.StatusOK {
		t.Fatalf("压缩资源应返回 200: %d", compressed.Code)
	}
	if got := compressed.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("应返回 br 编码: %q", got)
	}
	if vary := compressed.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("必须声明 Vary: Accept-Encoding: %q", vary)
	}
	if length := compressed.Header().Get("Content-Length"); length == "" {
		t.Fatal("压缩表示应声明 Content-Length")
	}
	if !strings.Contains(compressed.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("Content-Type 应基于原始文件名: %q", compressed.Header().Get("Content-Type"))
	}
	if cache := compressed.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("带哈希资源应允许长期缓存: %q", cache)
	}
	compressedETag := compressed.Header().Get("ETag")
	if compressedETag == "" {
		t.Fatal("压缩表示应返回 ETag")
	}
	if body := unbrotliTestData(t, compressed.Body.Bytes()); string(body) != appJS {
		t.Fatalf("压缩资源解压后内容不正确: %s", body)
	}

	// 同一 URL 的 br 表示命中 If-None-Match 时应返回 304。
	compressedNotModified := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"Accept-Encoding": "br", "If-None-Match": compressedETag})
	if compressedNotModified.Code != http.StatusNotModified {
		t.Fatalf("br 表示命中 If-None-Match 时应返回 304: %d", compressedNotModified.Code)
	}

	// Range 请求按压缩表示切分，应由 ServeContent 覆盖为区间长度。
	ranged := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"Accept-Encoding": "br", "Range": "bytes=0-9"})
	if ranged.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求应返回 206: %d", ranged.Code)
	}
	if got := ranged.Header().Get("Content-Length"); got != "10" {
		t.Fatalf("Range 请求长度不正确: %q", got)
	}
	if ranged.Body.Len() != 10 {
		t.Fatalf("Range 请求响应体长度不正确: %d", ranged.Body.Len())
	}

	identity := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil, nil)
	if identity.Code != http.StatusOK {
		t.Fatalf("不支持 br 的客户端应返回 200: %d", identity.Code)
	}
	if got := identity.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("不支持 br 时不应返回 Content-Encoding: %q", got)
	}
	if got := identity.Body.String(); got != appJS {
		t.Fatalf("不支持 br 时应流式解压原文: %s", got)
	}
	identityETag := identity.Header().Get("ETag")
	if identityETag == "" || identityETag == compressedETag {
		t.Fatalf("identity 与 br 必须使用不同的 ETag: %q %q", identityETag, compressedETag)
	}
	// 流式解压路径不经过 ServeContent，需要自行处理 304。
	identityNotModified := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"If-None-Match": identityETag})
	if identityNotModified.Code != http.StatusNotModified {
		t.Fatalf("解压表示命中 If-None-Match 时应返回 304: %d", identityNotModified.Code)
	}
	if identityNotModified.Body.Len() != 0 {
		t.Fatalf("304 不应返回响应体: %q", identityNotModified.Body.String())
	}

	// q=0 明确拒绝 br，同样走解压回退。
	rejected := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"Accept-Encoding": "br;q=0"})
	if got := rejected.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("br;q=0 不应返回压缩表示: %q", got)
	}
	if got := rejected.Body.String(); got != appJS {
		t.Fatalf("br;q=0 应返回解压原文: %s", got)
	}

	// 只有原始文件（开发模式）时保持原有行为。
	raw := sessionRequest(t, router, http.MethodGet, "/assets/raw-B7kXp2Qa.js", "", nil,
		map[string]string{"Accept-Encoding": "br"})
	if raw.Code != http.StatusOK {
		t.Fatalf("原始资源应返回 200: %d", raw.Code)
	}
	if got := raw.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("原始资源不应声明 Content-Encoding: %q", got)
	}
	if got := raw.Body.String(); got != "console.log('raw')" {
		t.Fatalf("原始资源内容不正确: %s", got)
	}

	// .br 是内部表示，直接请求应 404；旧版 .gz 同样不允许直接访问。
	for _, name := range []string{"/assets/app-abc12345.js.br", "/assets/app-abc12345.js.gz"} {
		direct := sessionRequest(t, router, http.MethodGet, name, "", nil, nil)
		if direct.Code != http.StatusNotFound {
			t.Fatalf("直接请求 %s 应返回 404: %d", name, direct.Code)
		}
	}
}

func TestServeAssetHeadUsesSameEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := New(
		nil,
		auth.New([]byte("jwt-secret")),
		nil,
		"secret-entry",
		fstest.MapFS{
			"index.html":        &fstest.MapFile{Data: []byte("<html></html>")},
			"assets/app.css.br": &fstest.MapFile{Data: brotliTestData(t, "body{color:#000}")},
		},
		nil,
	)
	router := s.Router()

	resp := sessionRequest(t, router, http.MethodHead, "/assets/app.css", "", nil,
		map[string]string{"Accept-Encoding": "br"})
	if resp.Code != http.StatusOK {
		t.Fatalf("HEAD 应返回 200: %d", resp.Code)
	}
	if got := resp.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("HEAD 应返回与 GET 一致的编码: %q", got)
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("HEAD 不应返回响应体: %q", resp.Body.String())
	}
}

func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"br", true},
		{"BR, gzip", true},
		{"gzip", false},
		{"gzip, br;q=0.5", true},
		{"br;q=0", false},
		{"br; q=0.0", false},
		{"*", true},
		{"*;q=0", false},
		{"identity, *;q=0.8", true},
		{"gzip", false},
	}
	for _, tc := range cases {
		if got := acceptsEncoding(tc.header, "br"); got != tc.want {
			t.Fatalf("Accept-Encoding %q: got %v, want %v", tc.header, got, tc.want)
		}
	}
}

func brotliTestData(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := brotli.NewWriter(&buf)
	if _, err := zw.Write([]byte(text)); err != nil {
		t.Fatalf("写入 brotli 测试数据失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 brotli 测试数据失败: %v", err)
	}
	return buf.Bytes()
}

func unbrotliTestData(t *testing.T, data []byte) []byte {
	t.Helper()
	body, err := io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("解析 brotli 响应失败: %v", err)
	}
	return body
}
