package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"lactssl/internal/auth"
	"lactssl/internal/db"
	"lactssl/internal/logarchive"
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
	st := store.New(conn, cipher)
	st.Archiver = logarchive.New(st, logarchive.Options{RootDir: t.TempDir(), Compress: true})
	return st
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
	// 测试服务器不经过 main 的启动流程，显式允许无 token 初始化；
	// 生产路径由 main 设置 token，且默认 fail-closed。
	s.AllowInsecureSetup = true
	if archiver, ok := st.Archiver.(*logarchive.Service); ok {
		s.LogArchives = archiver
	}
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

// TestSetupRequiresOneTimeToken 验证首次初始化必须携带启动日志中的一次性令牌。
func TestSetupRequiresOneTimeToken(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	s.SetSetupToken("setup-token-for-test")
	router := s.Router()

	body := `{"email":"admin@example.com","password":"password1234"}`
	missing := sessionRequest(t, router, http.MethodPost, "/api/setup", body, nil, nil)
	if missing.Code != http.StatusForbidden || !strings.Contains(missing.Body.String(), "api.error.setup_token_invalid") {
		t.Fatalf("缺少初始化令牌应被拒绝: %d %s", missing.Code, missing.Body.String())
	}
	wrong := sessionRequest(t, router, http.MethodPost, "/api/setup", body, nil,
		map[string]string{setupTokenHeader: "wrong-token"})
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("错误初始化令牌应被拒绝: %d %s", wrong.Code, wrong.Body.String())
	}
	success := sessionRequest(t, router, http.MethodPost, "/api/setup", body, nil,
		map[string]string{setupTokenHeader: "setup-token-for-test"})
	if success.Code != http.StatusOK {
		t.Fatalf("正确初始化令牌应成功: %d %s", success.Code, success.Body.String())
	}
	if s.SetupToken() != "" {
		t.Fatalf("初始化成功后应清空一次性令牌: %q", s.SetupToken())
	}
}

// TestEntryIssuesGateCookie 验证访问门禁入口只写入一次性门禁 Cookie 并跳到裸登录页。
func TestEntryIssuesGateCookie(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("门禁入口应跳转: %d", w.Code)
	}
	if location := w.Header().Get("Location"); location != "/login" {
		t.Fatalf("跳转地址不正确: %s", location)
	}
	gate := findCookie(t, w.Result().Cookies(), entryCookieName)
	if !gate.HttpOnly || gate.Path != "/" {
		t.Fatalf("门禁 Cookie 属性不正确: %+v", gate)
	}
}

// TestEntryIssuesDeviceCookie 验证首次访问门禁入口会下发稳定的设备标识。
func TestEntryIssuesDeviceCookie(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	device := findCookie(t, w.Result().Cookies(), deviceCookieName)
	if len(device.Value) != deviceCookieValueLength {
		t.Fatalf("设备标识长度不正确: %d", len(device.Value))
	}
	if !device.HttpOnly || device.Path != "/" {
		t.Fatalf("设备标识 Cookie 属性不正确: %+v", device)
	}
	if device.MaxAge <= 0 {
		t.Fatalf("设备标识应长期有效: %+v", device)
	}

	// 已存在设备标识时不应重新生成，保证同一浏览器身份稳定。
	again := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", []*http.Cookie{device}, nil)
	for _, cookie := range again.Result().Cookies() {
		if cookie.Name == deviceCookieName {
			t.Fatal("已有设备标识时不应重复下发")
		}
	}
}

// TestLoginCookieLengths 验证登录后的会话、CSRF Cookie 达到长度基线，并下发设备标识。
func TestLoginCookieLengths(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	csrf := findCookie(t, setup.Result().Cookies(), csrfCookieName)
	if len(session.Value) < auth.MinTokenLength {
		t.Fatalf("会话 Cookie 长度不足: %d", len(session.Value))
	}
	if len(csrf.Value) != csrfTokenLength {
		t.Fatalf("CSRF Cookie 长度不正确: %d", len(csrf.Value))
	}
	device := findCookie(t, setup.Result().Cookies(), deviceCookieName)
	if len(device.Value) != deviceCookieValueLength || !device.HttpOnly {
		t.Fatalf("设备标识不正确: %+v", device)
	}

	// 旧会话携带过短的 CSRF Cookie 时，中间件应自动补发长令牌。
	me := sessionRequest(t, router, http.MethodGet, "/api/auth/me", "", []*http.Cookie{
		session,
		{Name: csrfCookieName, Value: "short"},
	}, nil)
	upgraded := findCookie(t, me.Result().Cookies(), csrfCookieName)
	if len(upgraded.Value) != csrfTokenLength {
		t.Fatalf("旧会话的 CSRF Cookie 未收敛到目标长度: %d", len(upgraded.Value))
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
	// 没有门禁 Cookie 时只能看到精简版 404，不能暴露请求路径或找回入口的提示。
	body := w.Body.String()
	if !strings.Contains(body, "404 Not Found") || !strings.Contains(body, "LACTSSL") {
		t.Fatalf("无门禁 Cookie 应返回精简版 404: %s", body)
	}
	for _, leak := range []string{"/login", "页面不存在", "门禁入口地址"} {
		if strings.Contains(body, leak) {
			t.Fatalf("精简版 404 不应包含 %q: %s", leak, body)
		}
	}
	forged := &http.Cookie{Name: entryCookieName, Value: "forged"}
	if w := sessionRequest(t, router, http.MethodGet, "/login", "", []*http.Cookie{forged}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("伪造门禁 Cookie 应 404: %d", w.Code)
	} else if body := w.Body.String(); strings.Contains(body, "页面不存在") {
		t.Fatalf("伪造门禁 Cookie 不应拿到控制台样式 404: %s", body)
	}

	entry := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	gate := findCookie(t, entry.Result().Cookies(), entryCookieName)

	login := sessionRequest(t, router, http.MethodGet, "/login", "", []*http.Cookie{gate}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("携带门禁 Cookie 应返回登录页: %d", login.Code)
	}
	if !strings.Contains(login.Body.String(), `__LACTSSL_API_BASE__="/api"`) {
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
	// 没有门禁 Cookie 时返回精简版 404，不回显请求路径。
	if body := w.Body.String(); !strings.Contains(body, "404 Not Found") || strings.Contains(body, "/dashboard") {
		t.Fatalf("未通过门禁时应返回精简版 404: %s", body)
	}

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
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

// TestGatedUnknownPageUsesConsoleNotFound 验证已拿到入口门禁、但未登录访问未知页面时，
// 仍使用控制台样式的 404 页面展示原始路径，不退回精简版。
func TestGatedUnknownPageUsesConsoleNotFound(t *testing.T) {
	router := newSessionTestServer(t)

	entry := sessionRequest(t, router, http.MethodGet, "/secret-entry", "", nil, nil)
	gate := findCookie(t, entry.Result().Cookies(), entryCookieName)

	w := sessionRequest(t, router, http.MethodGet, "/dashboard", "", []*http.Cookie{gate}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未登录访问页面应返回 404: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "页面不存在") || !strings.Contains(body, "/dashboard") {
		t.Fatalf("门禁通过后的 404 页面内容不正确: %s", body)
	}
	if strings.Contains(body, `id="app-boot"`) || strings.Contains(body, `id="app-boot-style"`) {
		t.Fatalf("404 页面不应保留首屏加载层: %s", body)
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

	w := sessionRequest(t, router, http.MethodGet, "/api/private-cas", "", nil, nil)
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

// TestUnknownAPIPathReturns404 验证不存在的接口返回 404，而不是被当成页面处理。
func TestUnknownAPIPathReturns404(t *testing.T) {
	router := newSessionTestServer(t)

	w := sessionRequest(t, router, http.MethodGet, "/api/does-not-exist", "", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的接口应返回 404: %d", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("接口 404 应返回 JSON: %s", contentType)
	}
}

// TestSessionCookieCSRF 验证 Cookie 认证的状态变更请求必须带自定义头。
func TestSessionCookieCSRF(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	if w := sessionRequest(t, router, http.MethodPost, "/api/auth/logout",
		"", []*http.Cookie{session}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 头应返回 403: %d", w.Code)
	}
	w := sessionRequest(t, router, http.MethodPost, "/api/auth/logout",
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

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	first := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	login := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Code, login.Body.String())
	}
	second := findCookie(t, login.Result().Cookies(), sessionCookieName)

	list := sessionRequest(t, router, http.MethodGet, "/api/sessions", "",
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

	revoke := sessionRequest(t, router, http.MethodDelete, "/api/sessions/"+otherID, "",
		[]*http.Cookie{second}, map[string]string{csrfHeaderName: "1"})
	if revoke.Code != http.StatusOK {
		t.Fatalf("撤销会话失败: %d %s", revoke.Code, revoke.Body.String())
	}

	me := sessionRequest(t, router, http.MethodGet, "/api/auth/me", "",
		[]*http.Cookie{first}, nil)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("被撤销的会话不应继续访问接口: %d", me.Code)
	}

	after := sessionRequest(t, router, http.MethodGet, "/api/sessions", "",
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

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	first := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	login := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Code, login.Body.String())
	}
	second := findCookie(t, login.Result().Cookies(), sessionCookieName)

	change := sessionRequest(t, router, http.MethodPost, "/api/auth/change-password",
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
		me := sessionRequest(t, router, http.MethodGet, "/api/auth/me", "",
			[]*http.Cookie{cookie}, nil)
		if me.Code != http.StatusUnauthorized {
			t.Fatalf("%s 在改密后不应继续访问接口: %d %s", name, me.Code, me.Body.String())
		}
	}

	oldLogin := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码不应还能登录: %d %s", oldLogin.Code, oldLogin.Body.String())
	}

	newLogin := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"newpassword1234"}`, nil, nil)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("新密码应能登录: %d %s", newLogin.Code, newLogin.Body.String())
	}
}

// TestSensitiveOperationsRequireReauth 验证改密、换邮箱、关闭 TOTP 和删除通行密钥
// 都需要当前密码（启用 TOTP 时为动态验证码）。
func TestSensitiveOperationsRequireReauth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	headers := map[string]string{csrfHeaderName: "1"}

	missing := sessionRequest(t, router, http.MethodPost, "/api/auth/change-email",
		`{"email":"new@example.com"}`, []*http.Cookie{session}, headers)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少再认证信息时应拒绝换邮箱: %d %s", missing.Code, missing.Body.String())
	}

	wrong := sessionRequest(t, router, http.MethodPost, "/api/auth/change-email",
		`{"email":"new@example.com","password":"wrong-password"}`, []*http.Cookie{session}, headers)
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("错误当前密码应拒绝换邮箱: %d %s", wrong.Code, wrong.Body.String())
	}

	changed := sessionRequest(t, router, http.MethodPost, "/api/auth/change-email",
		`{"email":"new@example.com","password":"password1234"}`, []*http.Cookie{session}, headers)
	if changed.Code != http.StatusOK {
		t.Fatalf("正确当前密码应允许换邮箱: %d %s", changed.Code, changed.Body.String())
	}
	if _, err := st.GetUserByEmail("new@example.com"); err != nil {
		t.Fatalf("邮箱应已更新: %v", err)
	}
	// 换邮箱会轮换会话，后续请求必须使用新 Cookie。
	session = findCookie(t, changed.Result().Cookies(), sessionCookieName)

	// 删除通行密钥同样要求再认证；这里直接写入一条测试凭据。
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePasskey(&model.Passkey{
		UserID: user.ID, Name: "test", CredentialID: "test-credential", Credential: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	passkeyDelete := sessionRequest(t, router, http.MethodPost, "/api/auth/passkeys/1/delete",
		`{}`, []*http.Cookie{session}, headers)
	if passkeyDelete.Code != http.StatusBadRequest || !strings.Contains(passkeyDelete.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少再认证应拒绝删除通行密钥: %d %s", passkeyDelete.Code, passkeyDelete.Body.String())
	}
	passkeyDeleted := sessionRequest(t, router, http.MethodPost, "/api/auth/passkeys/1/delete",
		`{"password":"password1234"}`, []*http.Cookie{session}, headers)
	if passkeyDeleted.Code != http.StatusOK {
		t.Fatalf("正确再认证应允许删除通行密钥: %d %s", passkeyDeleted.Code, passkeyDeleted.Body.String())
	}
}

// TestTOTPIsRequiredForReauthWhenEnabled 验证启用 TOTP 后敏感操作只接受动态验证码。
func TestTOTPIsRequiredForReauthWhenEnabled(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	headers := map[string]string{csrfHeaderName: "1"}
	user, err := st.GetUser()
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, true); err != nil {
		t.Fatal(err)
	}

	// 只填密码不再被接受。
	passwordOnly := sessionRequest(t, router, http.MethodPost, "/api/auth/totp/disable",
		`{"password":"password1234"}`, []*http.Cookie{session}, headers)
	if passwordOnly.Code != http.StatusBadRequest || !strings.Contains(passwordOnly.Body.String(), "api.error.totp_invalid") {
		t.Fatalf("启用 TOTP 后应拒绝仅使用密码: %d %s", passwordOnly.Code, passwordOnly.Body.String())
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	disabled := sessionRequest(t, router, http.MethodPost, "/api/auth/totp/disable",
		`{"password":"password1234","totp":"`+code+`"}`, []*http.Cookie{session}, headers)
	if disabled.Code != http.StatusOK {
		t.Fatalf("正确的 TOTP 验证码应允许关闭 TOTP: %d %s", disabled.Code, disabled.Body.String())
	}
	updated, err := st.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TOTPEnabled {
		t.Fatal("TOTP 应已关闭")
	}
}

// TestValidCSRFCookieIsNotReissued 验证长度正确的 CSRF Cookie 不会在每次请求时重复下发。
func TestValidCSRFCookieIsNotReissued(t *testing.T) {
	router := newSessionTestServer(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	csrf := findCookie(t, setup.Result().Cookies(), csrfCookieName)

	me := sessionRequest(t, router, http.MethodGet, "/api/auth/me", "",
		[]*http.Cookie{session, csrf}, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("读取当前账户失败: %d %s", me.Code, me.Body.String())
	}
	for _, cookie := range me.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			t.Fatal("有效 CSRF Cookie 不应被重复下发")
		}
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

	setup := sessionRequest(t, firstRouter, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	oldSession := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	me := sessionRequest(t, firstRouter, http.MethodGet, "/api/auth/me", "",
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

	oldMe := sessionRequest(t, secondRouter, http.MethodGet, "/api/auth/me", "",
		[]*http.Cookie{oldSession}, nil)
	if oldMe.Code != http.StatusUnauthorized {
		t.Fatalf("重启后旧会话应失效: %d %s", oldMe.Code, oldMe.Body.String())
	}

	login := sessionRequest(t, secondRouter, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("重启后应能重新登录: %d %s", login.Code, login.Body.String())
	}
	newSession := findCookie(t, login.Result().Cookies(), sessionCookieName)

	list := sessionRequest(t, secondRouter, http.MethodGet, "/api/sessions", "",
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

// TestSessionPersistenceKeepsLoginAcrossRestart 验证开启会话保持后，
// 进程重启（使用固定 jwt_secret、不清空 sessions 表）不会让浏览器会话失效。
func TestSessionPersistenceKeepsLoginAcrossRestart(t *testing.T) {
	st := newSessionTestStore(t)

	firstRouter := newSessionTestServerWithAuth(t, st, auth.New([]byte("jwt-secret")))
	setup := sessionRequest(t, firstRouter, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	oldSession := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	// 模拟开启 session_persistence 后的重启：保留 sessions 表，使用相同 jwt_secret。
	secondRouter := newSessionTestServerWithAuth(t, st, auth.New([]byte("jwt-secret")))
	me := sessionRequest(t, secondRouter, http.MethodGet, "/api/auth/me", "",
		[]*http.Cookie{oldSession}, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("会话保持模式下重启后旧会话应可用: %d %s", me.Code, me.Body.String())
	}

	list := sessionRequest(t, secondRouter, http.MethodGet, "/api/sessions", "",
		[]*http.Cookie{oldSession}, nil)
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
		t.Fatalf("重启后应保留原会话: %+v", payload.Data)
	}
}

// TestDeletedSessionCannotBeRestoredByToken 验证服务端会话记录被删除后，
// 即使 JWT 本身仍在有效期内，也不能通过 ensureSession 自动补登记复活。
func TestDeletedSessionCannotBeRestoredByToken(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	if err := st.DeleteAllSessions(); err != nil {
		t.Fatalf("清理会话记录失败: %v", err)
	}

	me := sessionRequest(t, router, http.MethodGet, "/api/auth/me", "",
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

	req := httptest.NewRequest(http.MethodPost, "/api/setup",
		strings.NewReader(`{"email":"admin@example.com","password":"password1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.example")
	req.RemoteAddr = "192.0.2.30:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("跨站初始化请求应返回 403: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/setup",
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

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	csrf := findCookie(t, setup.Result().Cookies(), csrfCookieName)

	missingCookieReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	missingCookieReq.RemoteAddr = "192.0.2.30:1234"
	missingCookieReq.Header.Set(csrfHeaderName, "1")
	missingCookieReq.AddCookie(session)
	missingCookieResp := httptest.NewRecorder()
	router.ServeHTTP(missingCookieResp, missingCookieReq)
	if missingCookieResp.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF Cookie 时应返回 403: %d", missingCookieResp.Code)
	}

	wrong := sessionRequest(t, router, http.MethodPost, "/api/auth/logout", "",
		[]*http.Cookie{session, csrf}, map[string]string{csrfHeaderName: "wrong"})
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("CSRF 值不匹配应返回 403: %d", wrong.Code)
	}

	ok := sessionRequest(t, router, http.MethodPost, "/api/auth/logout", "",
		[]*http.Cookie{session, csrf}, map[string]string{csrfHeaderName: csrf.Value})
	if ok.Code != http.StatusOK {
		t.Fatalf("CSRF 值匹配应成功: %d %s", ok.Code, ok.Body.String())
	}
}
