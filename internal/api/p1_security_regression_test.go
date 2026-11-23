package api

import (
	"net/http"
	"testing"
)

// TestSetupFailsClosedWithoutToken 验证生产路径（未设置 AllowInsecureSetup）
// 在 setup token 为空时拒绝初始化、配置读取和配置写入。
func TestSetupFailsClosedWithoutToken(t *testing.T) {
	s, _ := newSetupConfigTestServer(t)
	s.AllowInsecureSetup = false
	s.SetSetupToken("")
	router := s.Router()

	get := sessionRequest(t, router, http.MethodGet, "/api/setup/config", "", nil, nil)
	if get.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 token 时读取 setup config 应 fail-closed: %d %s", get.Code, get.Body.String())
	}
	put := sessionRequest(t, router, http.MethodPut, "/api/setup/config", `{"public_url":"https://example.com"}`, nil, nil)
	if put.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 token 时写入 setup config 应 fail-closed: %d %s", put.Code, put.Body.String())
	}
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 token 时创建管理员应 fail-closed: %d %s", setup.Code, setup.Body.String())
	}
	if count, err := s.Store.CountUsers(); err != nil || count != 0 {
		t.Fatalf("fail-closed 后不应创建用户: count=%d err=%v", count, err)
	}

	// 配置 token 后，仍要求请求显式携带正确 header。
	s.SetSetupToken("p1-setup-token")
	missing := sessionRequest(t, router, http.MethodGet, "/api/setup/config", "", nil, nil)
	if missing.Code != http.StatusForbidden {
		t.Fatalf("配置 token 后缺少 header 应拒绝: %d %s", missing.Code, missing.Body.String())
	}
	allowed := sessionRequest(t, router, http.MethodGet, "/api/setup/config", "", nil,
		map[string]string{setupTokenHeader: "p1-setup-token"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("携带正确 token 应允许读取 setup config: %d %s", allowed.Code, allowed.Body.String())
	}
}
