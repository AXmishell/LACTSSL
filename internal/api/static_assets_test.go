package api

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
)

// TestServeSPAServesAssetsDirectlyFromWebFS 验证页面与静态资源直接读取 WebFS，
// 不依赖 Vite 开发服务器（5173），也不存在到它的代理。
func TestServeSPAServesAssetsDirectlyFromWebFS(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const indexHTML = "<html><head></head><body>direct-from-binary</body></html>"
	s := New(
		nil,
		auth.New([]byte("jwt-secret")),
		nil,
		"secret-entry",
		fstest.MapFS{
			"index.html":             &fstest.MapFile{Data: []byte(indexHTML)},
			"assets/app-abc12345.js": &fstest.MapFile{Data: []byte("console.log('direct')")},
			"assets/legacy.js":       &fstest.MapFile{Data: []byte("console.log('legacy')")},
			"favicon.svg":            &fstest.MapFile{Data: []byte("<svg></svg>")},
		},
		nil,
	)
	router := s.Router()

	asset := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil, nil)
	if asset.Code != http.StatusOK {
		t.Fatalf("静态资源应由 WebFS 直接返回: %d", asset.Code)
	}
	if body := asset.Body.String(); body != "console.log('direct')" {
		t.Fatalf("静态资源内容不正确: %s", body)
	}
	if cache := asset.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("带哈希资源应允许长期缓存: %s", cache)
	}
	etag := asset.Header().Get("ETag")
	if etag == "" {
		t.Fatal("静态资源应返回 ETag")
	}
	notModified := sessionRequest(t, router, http.MethodGet, "/assets/app-abc12345.js", "", nil,
		map[string]string{"If-None-Match": etag})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match 命中时应返回 304: %d", notModified.Code)
	}
	if notModified.Body.Len() != 0 {
		t.Fatalf("304 不应返回响应体: %q", notModified.Body.String())
	}

	// 未带指纹的文件不能使用 immutable，否则升级后无法失效。
	legacy := sessionRequest(t, router, http.MethodGet, "/assets/legacy.js", "", nil, nil)
	if cache := legacy.Header().Get("Cache-Control"); !strings.Contains(cache, "max-age=86400") || strings.Contains(cache, "immutable") {
		t.Fatalf("未带指纹的资源应使用短 TTL: %q", cache)
	}

	// 公共图标不再继承页面/接口的 no-store。
	icon := sessionRequest(t, router, http.MethodGet, "/favicon.svg", "", nil, nil)
	if cache := icon.Header().Get("Cache-Control"); !strings.Contains(cache, "max-age=86400") {
		t.Fatalf("公共图标应允许短期缓存: %q", cache)
	}
	if icon.Header().Get("ETag") == "" {
		t.Fatal("公共图标应返回 ETag")
	}

	// 通过门禁入口拿到门禁 Cookie 后，登录页同样直接读取 WebFS 中的 index.html。
	entry := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	gate := findCookie(t, entry.Result().Cookies(), entryCookieName)
	page := sessionRequest(t, router, http.MethodGet, "/login", "", []*http.Cookie{gate}, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("登录页应由 WebFS 直接返回: %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "direct-from-binary") {
		t.Fatalf("登录页内容不是 WebFS 中的 index.html: %s", body)
	}
	if !strings.Contains(body, `window.__LACTSSL_API_BASE__="/api"`) {
		t.Fatalf("登录页未注入接口地址: %s", body)
	}

	missing := sessionRequest(t, router, http.MethodGet, "/assets/missing-abc12345.js", "", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("WebFS 中不存在的资源应返回 404: %d", missing.Code)
	}
	if cache := missing.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("不存在的资源不能携带长期缓存头: %q", cache)
	}
}

func TestServeAssetRejectsTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := New(
		nil,
		auth.New([]byte("jwt-secret")),
		nil,
		"secret-entry",
		fstest.MapFS{
			"index.html":             &fstest.MapFile{Data: []byte("index")},
			"assets/app-abc12345.js": &fstest.MapFile{Data: []byte("app")},
		},
		nil,
	)
	router := s.Router()

	for _, path := range []string{
		"/assets/../index.html",
		"/assets/../../internal/webui/dist/index.html",
		"/assets/%2e%2e/index.html",
	} {
		resp := sessionRequest(t, router, http.MethodGet, path, "", nil, nil)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("%s 应返回 404: %d %s", path, resp.Code, resp.Body.String())
		}
	}
}
