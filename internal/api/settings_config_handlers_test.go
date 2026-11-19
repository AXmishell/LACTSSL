package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newSettingsConfigTestServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	s, configPath := newSetupConfigTestServer(t)
	s.DataDir = filepath.Dir(configPath)
	return s, s.Router()
}

func settingsConfigGet(t *testing.T, router http.Handler, session *http.Cookie) (revision string, config map[string]any) {
	t.Helper()
	resp := sessionRequest(t, router, http.MethodGet, "/api/settings/config", "", []*http.Cookie{session}, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取配置文件失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Revision string         `json:"revision"`
			Config   map[string]any `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析配置响应失败: %v", err)
	}
	if !payload.Success || payload.Data.Revision == "" {
		t.Fatalf("配置响应不完整: %s", resp.Body.String())
	}
	return payload.Data.Revision, payload.Data.Config
}

func TestSettingsConfigRequiresAuthAndReauth(t *testing.T) {
	_, router := newSettingsConfigTestServer(t)

	anonymous := sessionRequest(t, router, http.MethodGet, "/api/settings/config", "", nil, nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取配置应返回 401: %d %s", anonymous.Code, anonymous.Body.String())
	}

	session := loginSessionTest(t, router)
	revision, configData := settingsConfigGet(t, router, session)
	if configData["public_url"] != "" {
		t.Fatalf("初始 public_url 应为空: %v", configData["public_url"])
	}

	noReauth := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","config":{"public_url":"https://ssl.example.com"}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if noReauth.Code != http.StatusBadRequest {
		t.Fatalf("缺少再认证应被拒绝: %d %s", noReauth.Code, noReauth.Body.String())
	}

	saved := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"public_url":"https://ssl.example.com","trusted_proxies":["127.0.0.1","10.0.0.1/8"]}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存配置失败: %d %s", saved.Code, saved.Body.String())
	}
	for _, want := range []string{`"restart_required":true`, `"changed_fields"`, `"backup_id"`} {
		if !strings.Contains(saved.Body.String(), want) {
			t.Fatalf("保存响应缺少 %q: %s", want, saved.Body.String())
		}
	}
}

func TestSettingsConfigRevisionConflict(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	file, err := os.OpenFile(s.ConfigFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n# external edit\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()

	conflict := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"public_url":"https://ssl.example.com"}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "api.error.config_conflict") {
		t.Fatalf("旧 revision 应返回 409: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestSettingsConfigProxyHintsIgnoresForwardedHeadersForPeer(t *testing.T) {
	_, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)

	resp := sessionRequest(t, router, http.MethodGet, "/api/settings/config/proxy-hints", "",
		[]*http.Cookie{session},
		map[string]string{
			"X-Forwarded-For": "203.0.113.9, 198.51.100.7",
			"X-Real-IP":       "203.0.113.9",
		})
	if resp.Code != http.StatusOK {
		t.Fatalf("代理检测失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			PeerIP      string `json:"peer_ip"`
			HasForward  bool   `json:"has_forwarded"`
			Suggestions []struct {
				Value string `json:"value"`
			} `json:"suggestions"`
			Forwarded struct {
				XForwardedFor []string `json:"x_forwarded_for"`
			} `json:"forwarded"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	// sessionRequest 固定 RemoteAddr 为 192.0.2.30:1234。
	if payload.Data.PeerIP != "192.0.2.30" {
		t.Fatalf("peer_ip 应来自 RemoteAddr: %s", payload.Data.PeerIP)
	}
	if !payload.Data.HasForward {
		t.Fatal("应识别到转发头")
	}
	for _, item := range payload.Data.Suggestions {
		if item.Value == "203.0.113.9" || item.Value == "198.51.100.7" {
			t.Fatalf("不应把转发头当作代理候选: %+v", item)
		}
	}
	if len(payload.Data.Forwarded.XForwardedFor) != 2 {
		t.Fatalf("转发头应仅用于展示: %+v", payload.Data.Forwarded.XForwardedFor)
	}
}

func TestSettingsConfigBackupAndRestore(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	created := sessionRequest(t, router, http.MethodPost, "/api/settings/config/backups",
		`{"password":"password1234","note":"manual test"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if created.Code != http.StatusOK {
		t.Fatalf("创建配置备份失败: %d %s", created.Code, created.Body.String())
	}
	var createdPayload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil || createdPayload.Data.ID == "" {
		t.Fatalf("解析备份响应失败: %v %s", err, created.Body.String())
	}

	updated := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"public_url":"https://changed.example.com"}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if updated.Code != http.StatusOK {
		t.Fatalf("保存配置失败: %d %s", updated.Code, updated.Body.String())
	}

	restored := sessionRequest(t, router, http.MethodPost, "/api/settings/config/backups/"+createdPayload.Data.ID+"/restore",
		`{"password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if restored.Code != http.StatusOK {
		t.Fatalf("恢复配置备份失败: %d %s", restored.Code, restored.Body.String())
	}
	_, configData := settingsConfigGet(t, router, session)
	if configData["public_url"] != "" {
		t.Fatalf("恢复后 public_url 应回到空值: %v", configData["public_url"])
	}

	raw, err := os.ReadFile(s.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj", "ssssssssssssssssssssssssssssssss"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("恢复不应修改密钥:\n%s", raw)
		}
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "config-backups", createdPayload.Data.ID+".yaml")); err != nil {
		t.Fatalf("配置快照文件不存在: %v", err)
	}
}

func TestSettingsConfigAPIKeyCanReadButNotWrite(t *testing.T) {
	_, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	token, _ := createAPIKeyForTest(t, router, session, "config-reader", []string{apiScopeSettingsRead})

	read := apiKeyRequest(t, router, token, http.MethodGet, "/api/settings/config", "", apiKeyHeader)
	if read.Code != http.StatusOK {
		t.Fatalf("settings:read 密钥应允许读取配置: %d %s", read.Code, read.Body)
	}
	write := apiKeyRequest(t, router, token, http.MethodPut, "/api/settings/config",
		`{"config":{"public_url":"https://ssl.example.com"}}`, apiKeyHeader)
	if write.Code != http.StatusForbidden {
		t.Fatalf("配置写入必须仅限会话: %d %s", write.Code, write.Body)
	}
}

func TestSettingsConfigPortPreflightRejectsOccupiedPort(t *testing.T) {
	_, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	resp := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		fmt.Sprintf(`{"revision":%q,"password":"password1234","config":{"server":{"port":%d}}}`, revision, port),
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "api.error.config_invalid_reason") {
		t.Fatalf("占用端口应被预检拒绝: %d %s", resp.Code, resp.Body.String())
	}
}

func TestSettingsConfigRestartUsesInjectedHook(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	saved := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"public_url":"https://ssl.example.com"}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存配置失败: %d %s", saved.Code, saved.Body.String())
	}
	var savedPayload struct {
		Data struct {
			Revision string `json:"revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &savedPayload); err != nil || savedPayload.Data.Revision == "" {
		t.Fatalf("解析保存响应失败: %v %s", err, saved.Body.String())
	}

	restarted := make(chan struct{}, 1)
	s.RestartConfig = func() {
		select {
		case restarted <- struct{}{}:
		default:
		}
	}
	restart := sessionRequest(t, router, http.MethodPost, "/api/settings/config/restart",
		`{"revision":"`+savedPayload.Data.Revision+`","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if restart.Code != http.StatusOK {
		t.Fatalf("重启请求失败: %d %s", restart.Code, restart.Body.String())
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("重启钩子未被调用")
	}
}
