package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"lactssl/internal/auth"
	"lactssl/internal/service"
)

var routeParamPattern = regexp.MustCompile(`:[A-Za-z0-9_]+`)

func newSecurityTestServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
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
	s.AllowInsecureSetup = true
	return s, s.Router()
}

// TestAPIRoutesRequireAuthByDefault 确保除显式匿名白名单外，所有 /api 路由
// 在无凭据时都返回 401；新增路由不会因为忘记挂中间件而裸奔。
func TestAPIRoutesRequireAuthByDefault(t *testing.T) {
	router := newSessionTestServer(t)
	checked := 0
	for index, route := range router.Routes() {
		// 旧版兼容路径由 TestLegacyEntryAPIPathStillWorks 单独验证。
		if strings.HasPrefix(route.Path, "/:entry/") || !strings.HasPrefix(route.Path, "/api") {
			continue
		}
		key := route.Method + " " + route.Path
		if publicAPIRoutes[key] {
			continue
		}
		path := routeParamPattern.ReplaceAllString(route.Path, "1")
		req := httptest.NewRequest(route.Method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		// 每个路由用不同来源 IP，避免匿名接口限流干扰断言。
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", index%250+1)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s 无凭据应返回 401，实际 %d %s", key, w.Code, w.Body.String())
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("没有检查到任何受保护接口")
	}
}

// TestLegacyEntryAPIPathStillWorks 验证旧版 /<entry>/api 兼容别名：
// 正确入口可用，错误入口仍返回 404。
func TestLegacyEntryAPIPathStillWorks(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)

	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("旧版路径初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	me := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/auth/me", "", []*http.Cookie{session}, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("旧版路径访问接口失败: %d %s", me.Code, me.Body.String())
	}

	wrong := sessionRequest(t, router, http.MethodGet, "/wrong-entry/api/auth/me", "", nil, nil)
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("错误入口应返回 404: %d", wrong.Code)
	}
}

// TestLegacyEntryAPIReusesAPIKeyPolicy 验证旧版路径复用 /api 的 API 密钥权限表。
func TestLegacyEntryAPIReusesAPIKeyPolicy(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	token, _ := createAPIKeyForTest(t, router, session, "legacy", []string{apiScopeCertificatesRead})

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/certificates", "", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if w.Code != http.StatusOK {
		t.Fatalf("旧版路径的 API 密钥请求应通过权限校验: %d %s", w.Code, w.Body.String())
	}
}

// TestProtectedAPIRequiresCSRFForCookieSessions 验证受保护接口对 Cookie 会话
// 默认校验双提交 CSRF，而显式 Bearer/API 密钥不受影响。
func TestProtectedAPIRequiresCSRFForCookieSessions(t *testing.T) {
	router := newSessionTestServer(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	csrf := findCookie(t, setup.Result().Cookies(), csrfCookieName)

	missing := sessionRequest(t, router, http.MethodPost, "/api/tasks", "{}", []*http.Cookie{session}, nil)
	if missing.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 头应返回 403: %d %s", missing.Code, missing.Body.String())
	}

	valid := sessionRequest(t, router, http.MethodPost, "/api/tasks", "{}",
		[]*http.Cookie{session, csrf}, map[string]string{csrfHeaderName: csrf.Value})
	if valid.Code == http.StatusForbidden || valid.Code == http.StatusUnauthorized {
		t.Fatalf("携带正确 CSRF 头不应被拒绝: %d %s", valid.Code, valid.Body.String())
	}
}

// TestLoginRateLimitedByIP 验证登录接口在裸 /api 下仍按 IP 限流。
func TestLoginRateLimitedByIP(t *testing.T) {
	router := newSessionTestServer(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}

	body := `{"email":"admin@example.com","password":"wrong-password"}`
	for i := 0; i < 10; i++ {
		w := sessionRequest(t, router, http.MethodPost, "/api/auth/login", body, nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误登录应返回 401: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := sessionRequest(t, router, http.MethodPost, "/api/auth/login", body, nil, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过登录 IP 配额应返回 429: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应应包含 Retry-After")
	}
}

// TestAPIRateLimitCoversFakeCredentials 验证伪造 Bearer 令牌无法绕过总量限流。
func TestAPIRateLimitCoversFakeCredentials(t *testing.T) {
	s, router := newSecurityTestServer(t)
	for i := 0; i < 600; i++ {
		s.limiter.Allow("api-ip:192.0.2.31", 600, time.Minute)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/certificates", nil)
	req.RemoteAddr = "192.0.2.31:1234"
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("伪造凭据超过总量配额应返回 429: %d %s", w.Code, w.Body.String())
	}
}

// TestAnonymousAPIRateLimited 验证未携带凭据的裸 /api 请求按 IP 限流。
func TestAnonymousAPIRateLimited(t *testing.T) {
	s, router := newSecurityTestServer(t)
	for i := 0; i < 120; i++ {
		s.limiter.Allow("anon-api:192.0.2.30", 120, time.Minute)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.RemoteAddr = "192.0.2.30:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过匿名接口配额应返回 429: %d %s", w.Code, w.Body.String())
	}
}
