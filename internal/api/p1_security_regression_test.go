package api

import (
	"errors"
	"net/http"
	"strings"
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

// TestRepoStatusRedactsSecrets 验证 plugins:read 不能读取仓库 token 或代理口令。
func TestRepoStatusRedactsSecrets(t *testing.T) {
	st := newSessionTestStore(t)
	s := &Server{
		Store:             st,
		PluginRepoEnabled: true,
		PluginRepoURL:     "https://repo-user:repo-pass@example.com/plugins/index.json?token=repo-token",
		PluginRepoProxy:   "socks5://proxy-user:proxy-pass@127.0.0.1:1080/path?token=proxy-token",
	}
	status := s.repoStatus()
	if status["url"] != "https://example.com/plugins/index.json" {
		t.Fatalf("仓库 URL 未脱敏: %v", status["url"])
	}
	if status["proxy"] != "socks5://127.0.0.1:1080" {
		t.Fatalf("代理未脱敏: %v", status["proxy"])
	}
	for _, key := range []string{"url", "proxy", "error"} {
		text, _ := status[key].(string)
		for _, secret := range []string{"repo-pass", "repo-token", "proxy-pass", "proxy-token"} {
			if strings.Contains(text, secret) {
				t.Fatalf("%s 泄露了 %s: %q", key, secret, text)
			}
		}
	}

	s.setRepoError(errors.New("request https://u:secret@example.com/index?token=abc failed"))
	status = s.repoStatus()
	errText, _ := status["error"].(string)
	if strings.Contains(errText, "secret") || strings.Contains(errText, "abc") {
		t.Fatalf("仓库错误信息未脱敏: %q", errText)
	}
}
