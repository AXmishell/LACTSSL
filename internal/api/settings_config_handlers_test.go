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
	"lactssl/internal/config"
	"lactssl/internal/ratelimit"
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

func TestSettingsConfigSavesRateLimits(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, configData := settingsConfigGet(t, router, session)
	rateLimit, ok := configData["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("配置响应缺少 rate_limit: %v", configData["rate_limit"])
	}
	rules, ok := rateLimit["rules"].(map[string]any)
	if !ok || rules[ratelimit.LoginIP] == nil {
		t.Fatalf("rate_limit.rules 不完整: %v", rateLimit["rules"])
	}

	saved := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"rate_limit":{"max_keys":2000,"rules":{"login_ip":{"max":3,"window_seconds":60}}}}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存限流配置失败: %d %s", saved.Code, saved.Body.String())
	}
	cfg, err := config.LoadSetupConfig(s.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.MaxKeys != 2000 {
		t.Fatalf("max_keys 未保存: %d", cfg.RateLimit.MaxKeys)
	}
	if rule := cfg.RateLimit.Rules[ratelimit.LoginIP]; rule.Max != 3 || rule.WindowSeconds != 60 {
		t.Fatalf("login_ip 未保存: %+v", rule)
	}

	revision, _ = settingsConfigGet(t, router, session)
	bad := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"rate_limit":{"rules":{"not_a_rule":{"max":1,"window_seconds":60}}}}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("未知限流规则应返回 400: %d %s", bad.Code, bad.Body.String())
	}
}

func TestSettingsConfigSavesProxyAndArchiveFields(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	saved := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{`+
			`"server":{"client_ip_mode":"cloudflare","proxy_protocol":true},`+
			`"log":{"archive":{"enabled":true,"compress":false,"retention_days":90,"max_total_mb":512,"quota_policy":"prune_oldest"}},`+
			`"upgrade":{"program":{"allow_unsigned":true}}}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存代理与归档配置失败: %d %s", saved.Code, saved.Body.String())
	}
	cfg, err := config.LoadSetupConfig(s.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ClientIPMode != "cloudflare" || !cfg.Server.ProxyProtocol {
		t.Fatalf("代理识别配置未保存: %+v", cfg.Server)
	}
	if !cfg.Log.Archive.Enabled || cfg.Log.Archive.Compress ||
		cfg.Log.Archive.RetentionDays != 90 || cfg.Log.Archive.MaxTotalMB != 512 ||
		cfg.Log.Archive.QuotaPolicy != "prune_oldest" {
		t.Fatalf("日志归档配置未保存: %+v", cfg.Log.Archive)
	}
	if !cfg.Upgrade.Program.AllowUnsigned {
		t.Fatal("allow_unsigned 未保存")
	}
}

func TestSettingsConfigResetsRateLimitRule(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	session := loginSessionTest(t, router)
	revision, _ := settingsConfigGet(t, router, session)

	custom := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"rate_limit":{"rules":{"login_ip":{"max":3,"window_seconds":60}}}}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if custom.Code != http.StatusOK {
		t.Fatalf("保存自定义限流规则失败: %d %s", custom.Code, custom.Body.String())
	}

	revision, _ = settingsConfigGet(t, router, session)
	reset := sessionRequest(t, router, http.MethodPut, "/api/settings/config",
		`{"revision":"`+revision+`","password":"password1234","config":{"reset":["security.rate_limit.rules.login_ip"]}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if reset.Code != http.StatusOK {
		t.Fatalf("恢复默认限流规则失败: %d %s", reset.Code, reset.Body.String())
	}
	raw, err := os.ReadFile(s.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "login_ip:") {
		t.Fatalf("恢复默认后应从 config.yaml 删除该规则:\n%s", raw)
	}
	cfg, err := config.LoadSetupConfig(s.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	def := cfg.RateLimit.Defaults[ratelimit.LoginIP]
	rule := cfg.RateLimit.Rules[ratelimit.LoginIP]
	if rule.Max != def.Max || rule.WindowSeconds != def.WindowSeconds {
		t.Fatalf("规则未恢复默认: rule=%+v default=%+v", rule, def)
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

func TestSettingsConfigProxyHintsUsesAppliedConfig(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	// 模拟“已保存但未重启”：运行配置已生效，磁盘配置仍为空。
	s.TrustedProxies = []string{"192.0.2.30"}
	session := loginSessionTest(t, router)

	resp := sessionRequest(t, router, http.MethodGet, "/api/settings/config/proxy-hints", "",
		[]*http.Cookie{session},
		map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if resp.Code != http.StatusOK {
		t.Fatalf("代理检测失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			Effective  string   `json:"effective_client_ip"`
			Source     string   `json:"client_ip_source"`
			Applied    []string `json:"applied_trusted_proxies"`
			Configured []string `json:"configured_trusted_proxies"`
			Skipped    int      `json:"trusted_skipped"`
			Warnings   []struct {
				Code string `json:"code"`
			} `json:"warnings"`
			Suggestions []struct {
				Confidence           string `json:"confidence"`
				RequiresConfirmation bool   `json:"requires_confirmation"`
			} `json:"suggestions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Effective != "203.0.113.9" || payload.Data.Source != "x-forwarded-for" {
		t.Fatalf("应按生效配置解析客户端 IP: %s %s", payload.Data.Effective, payload.Data.Source)
	}
	if len(payload.Data.Applied) != 1 || payload.Data.Applied[0] != "192.0.2.30" {
		t.Fatalf("applied_trusted_proxies 不正确: %v", payload.Data.Applied)
	}
	if len(payload.Data.Configured) != 0 {
		t.Fatalf("configured_trusted_proxies 应来自磁盘配置: %v", payload.Data.Configured)
	}
	codes := map[string]bool{}
	for _, warning := range payload.Data.Warnings {
		codes[warning.Code] = true
	}
	for _, want := range []string{"unverified_forwarded_headers", "configured_differs_from_applied"} {
		if !codes[want] {
			t.Fatalf("缺少警告 %s: %+v", want, payload.Data.Warnings)
		}
	}
	if len(payload.Data.Suggestions) != 1 || payload.Data.Suggestions[0].Confidence != "medium" ||
		!payload.Data.Suggestions[0].RequiresConfirmation {
		t.Fatalf("非回环代理候选应为中置信并要求确认: %+v", payload.Data.Suggestions)
	}
}

func TestSettingsConfigProxyHintsReportsHeaderWarnings(t *testing.T) {
	s, router := newSettingsConfigTestServer(t)
	s.TrustedProxies = []string{"192.0.2.30"}
	session := loginSessionTest(t, router)

	resp := sessionRequest(t, router, http.MethodGet, "/api/settings/config/proxy-hints", "",
		[]*http.Cookie{session},
		map[string]string{"X-Forwarded-For": "203.0.113.9, bad"})
	if resp.Code != http.StatusOK {
		t.Fatalf("代理检测失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			Effective      string   `json:"effective_client_ip"`
			Source         string   `json:"client_ip_source"`
			HeaderWarnings []string `json:"header_warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Effective != "192.0.2.30" || payload.Data.Source != "remote" {
		t.Fatalf("非法 XFF 应回退 peer: %s %s", payload.Data.Effective, payload.Data.Source)
	}
	found := false
	for _, warning := range payload.Data.HeaderWarnings {
		if warning == "x_forwarded_for_unusable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应返回 XFF 解析告警: %+v", payload.Data.HeaderWarnings)
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
