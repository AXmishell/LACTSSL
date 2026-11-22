package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLoopbackOriginRequiresDevFlag 验证生产默认不再放行 Vite 回环跨端口来源。
func TestLoopbackOriginRequiresDevFlag(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/settings", nil)
	req.RemoteAddr = "127.0.0.1:34567"
	req.Header.Set("Origin", "http://localhost:5173")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	s := &Server{}
	if s.sameOrigin(c) {
		t.Fatal("未开启 AllowLoopbackOrigin 时不应放行回环跨端口 Origin")
	}
	s.AllowLoopbackOrigin = true
	if !s.sameOrigin(c) {
		t.Fatal("显式开启开发模式后应允许本机回环跨端口 Origin")
	}
}

// TestChangeEntryRequiresReauthAndDoesNotEcho 验证入口修改需要密码/TOTP，
// 且响应不再回显新入口。
func TestChangeEntryRequiresReauthAndDoesNotEcho(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	missing := sessionRequest(t, router, http.MethodPost, "/api/security/entry",
		`{"entry":"p2entry123"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("入口修改缺少再认证应拒绝: %d %s", missing.Code, missing.Body.String())
	}

	allowed := sessionRequest(t, router, http.MethodPost, "/api/security/entry",
		`{"entry":"p2entry123","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("正确再认证应允许修改入口: %d %s", allowed.Code, allowed.Body.String())
	}
	if strings.Contains(allowed.Body.String(), "p2entry123") {
		t.Fatalf("入口修改响应不应回显新入口: %s", allowed.Body.String())
	}
}
