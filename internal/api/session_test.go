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

func newSessionTestStore(t *testing.T) *store.Store {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	return store.New(conn, cipher)
}

func newSessionTestServerWithAuth(t *testing.T, st *store.Store, authManager *auth.Manager) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	s := New(
		st,
		authManager,
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	return s.Router()
}

func newSessionTestServerWithStore(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	st := newSessionTestStore(t)
	return newSessionTestServerWithAuth(t, st, auth.New([]byte("jwt-secret"))), st
}

// sessionIndexHTML 模拟构建后的 index.html，包含首屏启动层和模块脚本，
// 用于验证 404 页面会清理这些只属于正常应用的节点。
const sessionIndexHTML = `<html><head><title>LACTSSL</title>` +
	`<style id="app-boot-style">#app-boot{position:fixed}</style></head><body>` +
	`<div id="app"></div>` +
	`<div id="app-boot" role="status"><span class="boot-mark"></span><span class="boot-text">正在加载</span></div>` +
	`<script type="module" src="/assets/app.js"></script></body></html>`

func sessionRequest(t *testing.T, router http.Handler, method, path, body string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.30:1234"
	// 严格双提交校验要求 CSRF Cookie 与请求头一致；测试里用请求头值模拟已下发的 Cookie。
	if value, ok := headers[csrfHeaderName]; ok {
		hasCSRF := false
		for _, cookie := range cookies {
			if cookie.Name == csrfCookieName {
				hasCSRF = true
				break
			}
		}
		if !hasCSRF {
			cookies = append(append([]*http.Cookie{}, cookies...), &http.Cookie{Name: csrfCookieName, Value: value})
		}
	}
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
	if body := w.Body.String(); strings.Contains(body, `id="app-boot"`) || strings.Contains(body, `id="app-boot-style"`) {
		t.Fatalf("404 页面不应保留首屏加载层: %s", body)
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
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
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
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
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
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	first := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	login := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
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

// TestChangePasswordInvalidatesAllSessions 验证改密后包括当前设备在内的全部会话失效，
// 且必须使用新密码重新登录。
func TestChangePasswordInvalidatesAllSessions(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	first := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	login := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Code, login.Body.String())
	}
	second := findCookie(t, login.Result().Cookies(), sessionCookieName)

	change := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/change-password",
		`{"old_password":"password1234","new_password":"newpassword1234"}`,
		[]*http.Cookie{second}, map[string]string{csrfHeaderName: "1"})
	if change.Code != http.StatusOK {
		t.Fatalf("修改密码失败: %d %s", change.Code, change.Body.String())
	}

	cleared := false
	for _, cookie := range change.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value == "" && cookie.MaxAge < 0 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatalf("改密响应应清除会话 Cookie: %v", change.Result().Cookies())
	}

	for name, cookie := range map[string]*http.Cookie{"原会话": first, "当前会话": second} {
		me := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/auth/me", "",
			[]*http.Cookie{cookie}, nil)
		if me.Code != http.StatusUnauthorized {
			t.Fatalf("%s 在改密后不应继续访问接口: %d %s", name, me.Code, me.Body.String())
		}
	}

	oldLogin := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码不应还能登录: %d %s", oldLogin.Code, oldLogin.Body.String())
	}

	newLogin := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"newpassword1234"}`, nil, nil)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("新密码应能登录: %d %s", newLogin.Code, newLogin.Body.String())
	}
}

// TestRestartInvalidatesAllSessions 验证进程重启后旧会话 Cookie 失效：
// 启动级 JWT 签名密钥发生变化，且启动时清空服务端会话记录。
func TestRestartInvalidatesAllSessions(t *testing.T) {
	st := newSessionTestStore(t)

	firstAuth, err := auth.NewBootScoped([]byte("jwt-secret"))
	if err != nil {
		t.Fatalf("初始化首次启动认证组件失败: %v", err)
	}
	firstRouter := newSessionTestServerWithAuth(t, st, firstAuth)

	setup := sessionRequest(t, firstRouter, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	oldSession := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	me := sessionRequest(t, firstRouter, http.MethodGet, "/secret-entry/api/auth/me", "",
		[]*http.Cookie{oldSession}, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("重启前会话应可用: %d %s", me.Code, me.Body.String())
	}

	// 模拟进程重启：启动时清理 sessions 表，并生成新的启动级 JWT 签名密钥。
	if err := st.DeleteAllSessions(); err != nil {
		t.Fatalf("清理会话记录失败: %v", err)
	}
	secondAuth, err := auth.NewBootScoped([]byte("jwt-secret"))
	if err != nil {
		t.Fatalf("初始化重启后认证组件失败: %v", err)
	}
	secondRouter := newSessionTestServerWithAuth(t, st, secondAuth)

	oldMe := sessionRequest(t, secondRouter, http.MethodGet, "/secret-entry/api/auth/me", "",
		[]*http.Cookie{oldSession}, nil)
	if oldMe.Code != http.StatusUnauthorized {
		t.Fatalf("重启后旧会话应失效: %d %s", oldMe.Code, oldMe.Body.String())
	}

	login := sessionRequest(t, secondRouter, http.MethodPost, "/secret-entry/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("重启后应能重新登录: %d %s", login.Code, login.Body.String())
	}
	newSession := findCookie(t, login.Result().Cookies(), sessionCookieName)

	list := sessionRequest(t, secondRouter, http.MethodGet, "/secret-entry/api/sessions", "",
		[]*http.Cookie{newSession}, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("获取会话列表失败: %d %s", list.Code, list.Body.String())
	}
	var payload struct {
		Data []model.Session `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析会话列表失败: %v", err)
	}
	if len(payload.Data) != 1 || !payload.Data[0].Current {
		t.Fatalf("重启后应只剩重新登录产生的会话: %+v", payload.Data)
	}
}

// TestDeletedSessionCannotBeRestoredByToken 验证服务端会话记录被删除后，
// 即使 JWT 本身仍在有效期内，也不能通过 ensureSession 自动补登记复活。
func TestDeletedSessionCannotBeRestoredByToken(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	if err := st.DeleteAllSessions(); err != nil {
		t.Fatalf("清理会话记录失败: %v", err)
	}

	me := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/auth/me", "",
		[]*http.Cookie{session}, nil)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("服务端会话删除后令牌不应继续访问接口: %d %s", me.Code, me.Body.String())
	}

	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("统计会话记录失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("失效令牌不应被自动补登记，实际会话记录数 %d", count)
	}
}

// TestOriginGuardRejectsCrossSite 验证未认证状态变更接口拒绝跨站 Origin。
func TestOriginGuardRejectsCrossSite(t *testing.T) {
	router := newSessionTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/secret-entry/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.example")
	req.RemoteAddr = "192.0.2.30:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("跨站初始化请求应返回 403: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/secret-entry/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.RemoteAddr = "192.0.2.30:1234"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("同源初始化请求应成功: %d %s", w.Code, w.Body.String())
	}
}

// TestCSRFTokenMustMatchCookie 验证双提交 CSRF 在拿到 Cookie 后必须值一致。
func TestCSRFTokenMustMatchCookie(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	csrf := findCookie(t, setup.Result().Cookies(), csrfCookieName)

	missingCookieReq := httptest.NewRequest(http.MethodPost, "/secret-entry/api/auth/logout", nil)
	missingCookieReq.RemoteAddr = "192.0.2.30:1234"
	missingCookieReq.Header.Set(csrfHeaderName, "1")
	missingCookieReq.AddCookie(session)
	missingCookieResp := httptest.NewRecorder()
	router.ServeHTTP(missingCookieResp, missingCookieReq)
	if missingCookieResp.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF Cookie 时应返回 403: %d", missingCookieResp.Code)
	}

	wrong := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/logout", "",
		[]*http.Cookie{session, csrf}, map[string]string{csrfHeaderName: "wrong"})
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("CSRF 值不匹配应返回 403: %d", wrong.Code)
	}

	ok := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/auth/logout", "",
		[]*http.Cookie{session, csrf}, map[string]string{csrfHeaderName: csrf.Value})
	if ok.Code != http.StatusOK {
		t.Fatalf("CSRF 值匹配应成功: %d %s", ok.Code, ok.Body.String())
	}
}
