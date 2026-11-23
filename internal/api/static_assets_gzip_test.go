package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
)

// TestServeAssetNegotiatesPrecompressedGzip 验证生产构建的 .gz 资源：
// 支持 gzip 的客户端直接拿到压缩表示，不支持的客户端得到流式解压后的原文。
func TestServeAssetNegotiatesPrecompressedGzip(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const appJS = "console.log('precompressed')"
	s := New(
		nil,
		auth.New([]byte("jwt-secret")),
		nil,
		"secret-entry",
		fstest.MapFS{
			"index.html":       &fstest.MapFile{Data: []byte("<html><head></head></html>")},
			"assets/app.js.gz": &fstest.MapFile{Data: gzipTestData(t, appJS)},
			"assets/raw.js":    &fstest.MapFile{Data: []byte("console.log('raw')")},
		},
		nil,
	)
	router := s.Router()

	compressed := sessionRequest(t, router, http.MethodGet, "/assets/app.js", "", nil,
		map[string]string{"Accept-Encoding": "gzip, deflate"})
	if compressed.Code != http.StatusOK {
		t.Fatalf("压缩资源应返回 200: %d", compressed.Code)
	}
	if got := compressed.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("应返回 gzip 编码: %q", got)
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
	if body := gunzipTestData(t, compressed.Body.Bytes()); string(body) != appJS {
		t.Fatalf("压缩资源解压后内容不正确: %s", body)
	}

	// Range 请求按压缩表示切分，应由 ServeContent 覆盖为区间长度。
	ranged := sessionRequest(t, router, http.MethodGet, "/assets/app.js", "", nil,
		map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	if ranged.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求应返回 206: %d", ranged.Code)
	}
	if got := ranged.Header().Get("Content-Length"); got != "10" {
		t.Fatalf("Range 请求长度不正确: %q", got)
	}
	if ranged.Body.Len() != 10 {
		t.Fatalf("Range 请求响应体长度不正确: %d", ranged.Body.Len())
	}

	identity := sessionRequest(t, router, http.MethodGet, "/assets/app.js", "", nil, nil)
	if identity.Code != http.StatusOK {
		t.Fatalf("不支持 gzip 的客户端应返回 200: %d", identity.Code)
	}
	if got := identity.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("不支持 gzip 时不应返回 Content-Encoding: %q", got)
	}
	if got := identity.Body.String(); got != appJS {
		t.Fatalf("不支持 gzip 时应流式解压原文: %s", got)
	}

	// q=0 明确拒绝 gzip，同样走解压回退。
	rejected := sessionRequest(t, router, http.MethodGet, "/assets/app.js", "", nil,
		map[string]string{"Accept-Encoding": "gzip;q=0"})
	if got := rejected.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("gzip;q=0 不应返回压缩表示: %q", got)
	}
	if got := rejected.Body.String(); got != appJS {
		t.Fatalf("gzip;q=0 应返回解压原文: %s", got)
	}

	// 只有原始文件（开发模式）时保持原有行为。
	raw := sessionRequest(t, router, http.MethodGet, "/assets/raw.js", "", nil,
		map[string]string{"Accept-Encoding": "gzip"})
	if raw.Code != http.StatusOK {
		t.Fatalf("原始资源应返回 200: %d", raw.Code)
	}
	if got := raw.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("原始资源不应声明 Content-Encoding: %q", got)
	}
	if got := raw.Body.String(); got != "console.log('raw')" {
		t.Fatalf("原始资源内容不正确: %s", got)
	}

	// .gz 是内部表示，直接请求应 404。
	direct := sessionRequest(t, router, http.MethodGet, "/assets/app.js.gz", "", nil, nil)
	if direct.Code != http.StatusNotFound {
		t.Fatalf("直接请求 .gz 应返回 404: %d", direct.Code)
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
			"assets/app.css.gz": &fstest.MapFile{Data: gzipTestData(t, "body{color:#000}")},
		},
		nil,
	)
	router := s.Router()

	resp := sessionRequest(t, router, http.MethodHead, "/assets/app.css", "", nil,
		map[string]string{"Accept-Encoding": "gzip"})
	if resp.Code != http.StatusOK {
		t.Fatalf("HEAD 应返回 200: %d", resp.Code)
	}
	if got := resp.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("HEAD 应返回与 GET 一致的编码: %q", got)
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("HEAD 不应返回响应体: %q", resp.Body.String())
	}
}

func TestAcceptsGzipEncoding(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"GZIP, deflate", true},
		{"deflate", false},
		{"br, gzip;q=0.5", true},
		{"gzip;q=0", false},
		{"gzip; q=0.0", false},
		{"*", true},
		{"*;q=0", false},
		{"identity, *;q=0.8", true},
		{"br", false},
	}
	for _, tc := range cases {
		if got := acceptsGzipEncoding(tc.header); got != tc.want {
			t.Fatalf("Accept-Encoding %q: got %v, want %v", tc.header, got, tc.want)
		}
	}
}

func gzipTestData(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(text)); err != nil {
		t.Fatalf("写入 gzip 测试数据失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 gzip 测试数据失败: %v", err)
	}
	return buf.Bytes()
}

func gunzipTestData(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解析 gzip 响应失败: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("读取 gzip 响应失败: %v", err)
	}
	return body
}
