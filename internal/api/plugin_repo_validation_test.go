package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/i18n"
	"lactssl/internal/plugin/repo"
)

// TestCachedRepoIndexIsolatesBadEntry 验证缓存加载也走统一 ParseIndex，
// 单条坏数据不会让整份缓存失效，且状态接口能报告被隔离条目。
func TestCachedRepoIndexIsolatesBadEntry(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-cache-isolation-key"))
	raw := `{"schema":1,"channel":"stable","protocol":"v1","plugins":[
		{"kind":"dns","id":"good","version":"1.0.0","protocol":"v1","assets":{"linux/amd64":{"url":"https://example.invalid/good.tgz","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":1}}},
		{"kind":"dns","id":"bad","version":"1.0.0","protocol":"v1","assets":{"linux/amd64":{"url":"https://example.invalid/bad.tgz","size":1}}}
	]}`
	// 缓存 HMAC 绑定仓库身份，准备缓存时使用与加载方相同的 URL/渠道。
	marker := &Server{
		Auth:              manager,
		PluginRepoURL:     "https://example.invalid/index.json",
		PluginRepoChannel: "stable",
	}
	if err := st.SetSetting(repoIndexSetting, raw); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(raw)); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:             st,
		Auth:              manager,
		PluginRepoEnabled: true,
		PluginRepoURL:     "https://example.invalid/index.json",
		PluginRepoChannel: "stable",
	}
	result, ok := s.loadCachedRepoIndex()
	if !ok {
		t.Fatal("cache should load with one valid entry")
	}
	if len(result.Index.Plugins) != 1 || result.Index.Plugins[0].ID != "good" {
		t.Fatalf("valid entry should survive: %+v", result.Index.Plugins)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Plugin.ID != "bad" {
		t.Fatalf("bad entry should be quarantined: %+v", result.Rejected)
	}

	status := s.repoStatus()
	if status["plugins"] != 1 || status["plugins_rejected"] != 1 {
		t.Fatalf("status counts = %+v", status)
	}
	issues, ok := status["issues"].([]gin.H)
	if !ok || len(issues) != 1 {
		t.Fatalf("status issues = %+v", status["issues"])
	}
	if issues[0]["severity"] != pluginrepo.SeverityEntry {
		t.Fatalf("issue severity = %v", issues[0]["severity"])
	}
}

// TestCachedRepoIndexRejectedWhenSourceChanged 验证缓存 HMAC 绑定了仓库
// 地址与渠道：切换仓库后旧缓存即使仍在数据库中也不能被加载。
func TestCachedRepoIndexRejectedWhenSourceChanged(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-cache-identity-key"))
	raw := `{"schema":1,"channel":"stable","protocol":"v1","plugins":[]}`
	marker := &Server{Auth: manager, PluginRepoURL: "https://old.example/index.json", PluginRepoChannel: "stable"}
	if err := st.SetSetting(repoIndexSetting, raw); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoETagSetting, `"old"`); err != nil {
		t.Fatal(err)
	}

	for name, s := range map[string]*Server{
		"url":     {Store: st, Auth: manager, PluginRepoURL: "https://new.example/index.json", PluginRepoChannel: "stable"},
		"channel": {Store: st, Auth: manager, PluginRepoURL: "https://old.example/index.json", PluginRepoChannel: "beta"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := s.loadCachedRepoIndex(); ok {
				t.Fatal("cache from another repository identity must be rejected")
			}
			if etag, _ := st.GetSetting(repoETagSetting); etag != "" {
				t.Fatalf("rejected cache should clear ETag: %q", etag)
			}
		})
	}
}

// TestRepoExpiredError 验证安装/自动更新路径会拒绝过期索引。
func TestRepoExpiredError(t *testing.T) {
	if err := repoExpiredError(pluginrepo.ValidatedIndex{}, time.Now()); err != nil {
		t.Fatalf("index without expiry should be allowed: %v", err)
	}
	expired := pluginrepo.ValidatedIndex{ExpiresAt: time.Now().Add(-time.Minute)}
	if err := repoExpiredError(expired, time.Now()); err == nil {
		t.Fatal("expired index should be rejected for install")
	}
}

// TestFailErrRedactsSecretParams 验证可翻译错误参数中的仓库 URL token
// 不会通过 message 或结构化 params 回显。
func TestFailErrRedactsSecretParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	failErr(c, http.StatusBadRequest, i18n.E("plugin.repo.signature_read_failed",
		"error", `Get "https://example.invalid/index.json?token=secret": dial failed`))
	body := rec.Body.String()
	if strings.Contains(body, "secret") {
		t.Fatalf("response leaked repository token: %s", body)
	}
	if !strings.Contains(body, "token") && !strings.Contains(body, "***") {
		t.Fatalf("response should keep a redacted hint: %s", body)
	}
}

// TestRepoStatusReportsRevisionAndGeneratedAt 验证信任元数据会出现在状态接口。
func TestRepoStatusReportsRevisionAndGeneratedAt(t *testing.T) {
	st := newSessionTestStore(t)
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:               st,
		PluginRepoEnabled:   true,
		PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pub),
	}
	s.pluginRepoIndexRaw = []byte(`{"schema":1,"channel":"stable","protocol":"v1","plugins":[]}`)
	s.pluginRepoIndex = pluginrepo.Index{Schema: 1, Channel: "stable", Protocol: "v1"}
	s.pluginRepoVerified = true
	s.pluginRepoRevision = 7
	s.pluginRepoIndexDigest = "index-digest"
	s.pluginRepoKeyID = "SHA256:test-key"
	status := s.repoStatus()
	if status["index_revision"] != uint64(7) {
		t.Fatalf("revision = %v", status["index_revision"])
	}
	if status["index_schema"] != 1 {
		t.Fatalf("schema = %v", status["index_schema"])
	}
	if status["index_digest"] != "index-digest" || status["signature_key_id"] != "SHA256:test-key" {
		t.Fatalf("audit status = %+v", status)
	}
}
