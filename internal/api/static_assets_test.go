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
			"index.html":    &fstest.MapFile{Data: []byte(indexHTML)},
			"assets/app.js": &fstest.MapFile{Data: []byte("console.log('direct')")},
		},
		nil,
	)
	router := s.Router()

	asset := sessionRequest(t, router, http.MethodGet, "/assets/app.js", "", nil, nil)
	if asset.Code != http.StatusOK {
		t.Fatalf("静态资源应由 WebFS 直接返回: %d", asset.Code)
	}
	if body := asset.Body.String(); body != "console.log('direct')" {
		t.Fatalf("静态资源内容不正确: %s", body)
	}
	if cache := asset.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("带哈希资源应允许长期缓存: %s", cache)
	}

	// 通过安全入口拿到门禁 Cookie 后，登录页同样直接读取 WebFS 中的 index.html。
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
	if !strings.Contains(body, `window.__LACTSSL_API_BASE__="/secret-entry/api"`) {
		t.Fatalf("登录页未注入安全入口: %s", body)
	}

	missing := sessionRequest(t, router, http.MethodGet, "/assets/missing.js", "", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("WebFS 中不存在的资源应返回 404: %d", missing.Code)
	}
}
