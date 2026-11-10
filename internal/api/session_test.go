package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newSessionTestServer(t *testing.T) *gin.Engine {
	t.Helper()
	router, _ := newSessionTestServerWithStore(t)
	return router
}

func newSessionTestServerWithStore(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	st := store.New(conn, cipher)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>app</body></html>")}},
		nil,
	)
	return s.Router(), st
}

func sessionRequest(t *testing.T, router http.Handler, method, path, body string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.30:1234"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func findCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("响应未下发 Cookie %s", name)
	return nil
}

// TestEntryIssuesGateCookie 验证访问安全入口只写入一次性门禁 Cookie 并跳到裸登录页。
func TestEntryIssuesGateCookie(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("安全入口应跳转: %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/login" {
		t.Fatalf("跳转地址不正确: %s", location)
	}
	gate := findCookie(t, w.Result().Cookies(), entryCookieName)
	if !gate.HttpOnly || gate.Path != "/" {
		t.Fatalf("门禁 Cookie 属性不正确: %+v", gate)
	}
}

// TestLoginPageRequiresGateCookie 验证裸登录页只有携带入口门禁 Cookie 才能访问。
func TestLoginPageRequiresGateCookie(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/login", "", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("无门禁 Cookie 访问登录页应 404: %d", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("404 页面应返回 HTML: %s", contentType)
	}
	if body := w.Body.String(); !strings.Contains(body, "404") || !strings.Contains(body, "页面不存在") {
		t.Fatalf("404 页面内容不正确: %s", body)
	}
	forged := &http.Cookie{Name: entryCookieName, Value: "forged"}
	if w := sessionRequest(t, router, http.MethodGet, "/login", "", []*http.Cookie{forged}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("伪造门禁 Cookie 应 404: %d", w.Code)
	}

	entry := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	gate := findCookie(t, entry.Result().Cookies(), entryCookieName)

	login := sessionRequest(t, router, http.MethodGet, "/login", "", []*http.Cookie{gate}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("携带门禁 Cookie 应返回登录页: %d", login.Code)
	}
	if !strings.Contains(login.Body.String(), `__LACTSSL_API_BASE__="/secret-entry/api"`) {
		t.Fatal("登录页应注入接口地址")
	}
}

// TestSessionCookieGatesPages 验证控制台页面只有携带会话 Cookie 才返回。
func TestSessionCookieGatesPages(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/dashboard", "", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未登录访问 /dashboard 应直接 404: %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "" {
		t.Fatalf("未登录访问页面不应重定向: %s", location)
	}
	if body := w.Body.String(); !strings.Contains(body, "/dashboard") {
		t.Fatal("404 页面应展示原始请求路径")
	}

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password123"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	if !session.HttpOnly {
		t.Fatal("会话 Cookie 必须是 HttpOnly")
	}
	if session.Path != "/" {
		t.Fatalf("会话 Cookie 的 Path 应为 /: %s", session.Path)
	}

	if w := sessionRequest(t, router, http.MethodGet, "/dashboard", "", []*http.Cookie{session}, nil); w.Code != http.StatusOK {
		t.Fatalf("登录后访问 /dashboard 应返回 200: %d", w.Code)
	}
}

// TestOldEntryLinksRedirectToBarePaths 验证旧入口链接会保留查询串并跳到裸路径。
func TestOldEntryLinksRedirectToBarePaths(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/reset?token=abc", "", nil, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("旧入口链接应跳转: %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/reset?token=abc" {
		t.Fatalf("旧入口链接跳转不正确: %s", location)
	}
}

// TestAuthMiddlewareAbortsRequest 验证认证失败后不会继续执行目标接口。
func TestAuthMiddlewareAbortsRequest(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/private-cas", "", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问私有 CA 接口应返回 401: %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `"success":true`) || strings.Contains(body, `"data"`) {
		t.Fatalf("认证失败后不应继续执行业务接口: %s", body)
	}
	if !strings.Contains(body, "api.error.unauthorized") {
		t.Fatalf("应返回未登录错误: %s", body)
	}
}

// TestEntryMiddlewareAbortsRequest 验证安全入口错误后不会继续执行目标接口。
func TestEntryMiddlewareAbortsRequest(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/wrong-entry/api/private-cas", "", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("错误安全入口应返回 404: %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `"success":true`) || strings.Contains(body, `"data"`) {
		t.Fatalf("安全入口校验失败后不应继续执行业务接口: %s", body)
	}
}

// TestSessionCookieCSRF 验证 Cookie 认证的状态变更请求必须带自定义头。
func TestSessionCookieCSRF(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password123"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	if w := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/logout",
		"", []*http.Cookie{session}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 头应返回 403: %d", w.Code)
	}
	w := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/logout",
		"", []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK {
		t.Fatalf("带 CSRF 头的退出请求应成功: %d %s", w.Code, w.Body.String())
	}
	if cookie := findCookie(t, w.Result().Cookies(), sessionCookieName); cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatal("退出登录应清除会话 Cookie")
	}
}

// TestSessionManagement 验证会话列表标记当前会话，并能撤销其他会话。
func TestSessionManagement(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password123"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	first := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	login := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"password123"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Code, login.Body.String())
	}
	second := findCookie(t, login.Result().Cookies(), sessionCookieName)

	list := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/sessions", "",
		[]*http.Cookie{second}, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("获取会话列表失败: %d %s", list.Code, list.Body.String())
	}
	var payload struct {
		Data []model.Session `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析会话列表失败: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("应有 2 个活跃会话，实际 %d 个", len(payload.Data))
	}
	var otherID string
	current := 0
	for _, session := range payload.Data {
		if session.Current {
			current++
		} else {
			otherID = session.ID
		}
	}
	if current != 1 || otherID == "" {
		t.Fatalf("当前会话标记不正确: %+v", payload.Data)
	}

	revoke := sessionRequest(t, router, http.MethodDelete, "/secret-entry/api/sessions/"+otherID, "",
		[]*http.Cookie{second}, map[string]string{csrfHeaderName: "1"})
	if revoke.Code != http.StatusOK {
		t.Fatalf("撤销会话失败: %d %s", revoke.Code, revoke.Body.String())
	}

	me := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/auth/me", "",
		[]*http.Cookie{first}, nil)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("被撤销的会话不应继续访问接口: %d", me.Code)
	}

	after := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/sessions", "",
		[]*http.Cookie{second}, nil)
	if err := json.Unmarshal(after.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析会话列表失败: %v", err)
	}
	if len(payload.Data) != 1 || !payload.Data[0].Current {
		t.Fatalf("撤销后应只剩当前会话: %+v", payload.Data)
	}
}
