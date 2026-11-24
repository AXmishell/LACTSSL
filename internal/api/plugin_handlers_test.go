package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/plugin/host/deploy"
	"lactssl/internal/plugin/host/dns"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newPluginTestServer(t *testing.T) (*Server, *gin.Engine, string) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"test helper","version":"1.0.0","providers":[{"id":"alphadns","name":"Alpha DNS","fields":[{"key":"token","label":"Token","type":"password","secret":true}]},{"id":"betadns","name":"Beta DNS"}]}
JSON
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:               st,
		PluginsEnabled:      true,
		PluginDir:           dir,
		PluginMemoryLimitMB: 128,
		PluginTimeout:       5 * time.Second,
		PluginMaxOutputKB:   64,
	}
	s.entry.Store("entry")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/entry/api/plugins/providers", s.setPluginProvidersEnabled)
	r.PUT("/entry/api/plugins/providers/:id", s.setPluginProviderEnabled)
	r.PUT("/entry/api/plugins/files/:kind/:file", s.setPluginFileEnabled)
	r.GET("/entry/api/plugins", s.listPlugins)
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
	return s, r, dir
}

func putPluginToggle(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestPluginProviderToggle(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	// 默认全部停用：helper 已发现，但不注册任何服务商。
	result := s.DiscoverPlugins()
	if result.Helpers != 1 || result.Providers != 0 || len(result.Disabled) != 2 {
		t.Fatalf("discover result: %+v", result)
	}
	if dnsplugin.Has("alphadns") || dnsplugin.Has("betadns") {
		t.Fatal("plugin providers should be disabled by default")
	}

	// 启用单个插件服务商。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/betadns", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !dnsplugin.Has("betadns") {
		t.Fatal("enabled provider should be registered")
	}
	if dnsplugin.Has("alphadns") {
		t.Fatal("other providers should stay disabled")
	}
	meta, ok := dnsplugin.ExternalProviderInfo("betadns")
	if !ok || !meta.Enabled {
		t.Fatalf("enabled metadata = %+v, %v", meta, ok)
	}
	raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting)
	if !strings.Contains(raw, "betadns") {
		t.Fatalf("enabled setting = %q", raw)
	}

	// 重新停用。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/betadns", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d: %s", rec.Code, rec.Body.String())
	}
	if dnsplugin.Has("betadns") {
		t.Fatal("disabled provider should not be registered")
	}

	// 全部启用后再全部停用。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("bulk enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !dnsplugin.Has("alphadns") || !dnsplugin.Has("betadns") {
		t.Fatal("bulk enable should register all providers")
	}
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("bulk disable status = %d: %s", rec.Code, rec.Body.String())
	}
	if dnsplugin.Has("alphadns") || dnsplugin.Has("betadns") {
		t.Fatal("bulk disable should unregister all providers")
	}
	raw, _ = s.Store.GetSetting(dnsPluginEnabledSetting)
	// 批量停用只作用于当前发现的服务商；默认白名单为空，不再保留历史 35 个 ID。
	if strings.Contains(raw, "alphadns") || strings.Contains(raw, "betadns") {
		t.Fatalf("discovered providers should be removed after bulk disable: %q", raw)
	}
	if strings.Contains(raw, "cloudflare") {
		t.Fatalf("no DNS provider should be enabled by default: %q", raw)
	}
}

func TestPluginProvidersDisabledByDefault(t *testing.T) {
	if dnsplugin.Has("cloudflare") {
		t.Skip("cloudflare is compiled in this build")
	}
	s, r, dir := newPluginTestServer(t)
	script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"builtin helper","version":"1.0.0","providers":[{"id":"cloudflare","name":"Cloudflare"},{"id":"external-only","name":"External Only"}]}
JSON
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result := s.DiscoverPlugins()
	// 新安装默认不启用任何 DNS 服务商。
	if result.Providers != 0 || len(result.Disabled) != 2 {
		t.Fatalf("discover result: %+v", result)
	}
	if dnsplugin.Has("cloudflare") || dnsplugin.Has("external-only") {
		t.Fatal("all DNS providers should be disabled by default")
	}
	if raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting); strings.TrimSpace(raw) != "" {
		t.Fatalf("enabled setting should stay empty before user changes: %q", raw)
	}

	// 管理员可以显式启用服务商并持久化。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/cloudflare", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !dnsplugin.Has("cloudflare") {
		t.Fatal("explicitly enabled provider should be registered")
	}
	raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting)
	if !strings.Contains(raw, "cloudflare") || strings.Contains(raw, "external-only") {
		t.Fatalf("enabled setting = %q", raw)
	}
}

func TestPluginProviderToggleRejectsBuiltinAndUnknown(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	s.DiscoverPlugins()

	// 编译进主程序的内置服务商不可停用；默认构建已把服务商迁移到插件，
	// 此时未安装对应 helper 的 ID 按未知服务商处理。
	if plugin, registered := dnsplugin.Lookup("cloudflare"); registered && plugin.Source() == dnsplugin.SourceBuiltin {
		if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/cloudflare", `{"enabled":false}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("builtin status = %d: %s", rec.Code, rec.Body.String())
		}
	} else if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/cloudflare", `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unavailable builtin status = %d: %s", rec.Code, rec.Body.String())
	}
	// 未发现的服务商返回 404。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/missingdns", `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d: %s", rec.Code, rec.Body.String())
	}
	// 目录中存在但当前构建未编译、也没有插件提供的服务商同样返回 404。
	if !dnsplugin.Has("ovh") {
		if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/ovh", `{"enabled":false}`); rec.Code != http.StatusNotFound {
			t.Fatalf("uncompiled status = %d: %s", rec.Code, rec.Body.String())
		}
	}
}

// newPluginUploadTestServer 创建一个带上传路由的测试服务。
// strongAuth=true 时模拟通行密钥会话，直接跳过二次验证；否则验证接口应拒绝请求。
func newPluginUploadTestServer(t *testing.T, strongAuth bool) (*Server, *gin.Engine, string, string) {
	t.Helper()
	s, _, dnsDir := newPluginTestServer(t)
	deployDir := t.TempDir()
	s.DeployPluginsEnabled = true
	s.DeployPluginDir = deployDir
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 复用真实路由的大小限制中间件，确认插件上传路径被放宽。
	r.Use(s.bodyLimit())
	r.Use(func(c *gin.Context) {
		c.Set("user", &model.User{ID: 1})
		c.Set("session_id", "test-session")
		if strongAuth {
			c.Set("session_strong_auth", true)
		}
		c.Next()
	})
	r.POST("/entry/api/plugins/upload", s.uploadPlugin)
	t.Cleanup(func() { deployplugin.Discover(deployplugin.DiscoverConfig{}) })
	return s, r, dnsDir, deployDir
}

func pluginUploadRequest(t *testing.T, kind, filename, savedName string, content []byte, overwrite bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	if savedName != "" {
		if err := writer.WriteField("name", savedName); err != nil {
			t.Fatal(err)
		}
	}
	if overwrite {
		if err := writer.WriteField("overwrite", "true"); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/entry/api/plugins/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

const dnsUploadHelper = `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"uploaded helper","version":"2.0.0","providers":[{"id":"gammadns","name":"Gamma DNS"}]}
JSON
  ;;
esac
`

func TestUploadDNSPlugin(t *testing.T) {
	s, r, dir, _ := newPluginUploadTestServer(t, true)
	// 插件服务商默认全部停用，但 helper 应已被发现。
	if result := s.DiscoverPlugins(); result.Helpers != 1 || result.Providers != 0 || len(result.Disabled) != 2 {
		t.Fatalf("initial discover result: %+v", result)
	}

	req := pluginUploadRequest(t, "dns", "uploaded-helper", "", []byte(dnsUploadHelper), false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gammadns") {
		t.Fatalf("response missing provider id: %s", rec.Body.String())
	}
	path := filepath.Join(dir, "uploaded-helper")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("uploaded plugin not found: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("uploaded plugin is not executable: %v", info.Mode())
	}
	// 新上传的 helper 默认停用文件级加载：不执行 manifest，条目只来自安装缓存。
	if s.pluginFileEnabled(pluginKindDNS, "uploaded-helper") {
		t.Fatal("uploaded helper should be disabled by default")
	}
	var uploaded *pluginFileView
	files := s.pluginFiles(pluginKindDNS)
	for i := range files {
		if files[i].File == "uploaded-helper" {
			uploaded = &files[i]
		}
	}
	if uploaded == nil || uploaded.Enabled || len(uploaded.Items) != 1 || uploaded.Items[0] != "gammadns" {
		t.Fatalf("uploaded file view = %+v", uploaded)
	}
	// 启用文件后条目仍然默认停用；显式启用条目后才会注册到运行时。
	if err := s.updatePluginFileEnabled(pluginKindDNS, "uploaded-helper", true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()
	if meta, ok := dnsplugin.ExternalProviderInfo("gammadns"); !ok || meta.Enabled {
		t.Fatalf("uploaded provider metadata = %+v, %v", meta, ok)
	}
	if _, _, err := s.updateDNSPluginProviders([]string{"gammadns"}, true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()
	if !dnsplugin.Has("gammadns") {
		t.Fatal("enabled uploaded provider should be registered")
	}

	// 同名上传默认拒绝，避免误覆盖正在使用的 helper。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "uploaded-helper", "", []byte(dnsUploadHelper), false))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate upload status = %d: %s", rec.Code, rec.Body.String())
	}

	// 显式允许覆盖后可以替换文件，并保留原有文件级启用状态。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "uploaded-helper", "", []byte(dnsUploadHelper), true))
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite status = %d: %s", rec.Code, rec.Body.String())
	}
	if !s.pluginFileEnabled(pluginKindDNS, "uploaded-helper") {
		t.Fatal("overwriting an existing helper must preserve its file state")
	}
}

func TestUploadPluginRejectsInvalidManifestAndName(t *testing.T) {
	s, r, dir, _ := newPluginUploadTestServer(t, true)
	s.DiscoverPlugins()

	invalid := []byte("#!/bin/sh\ncase \"$1\" in manifest) echo '{\"protocol\":\"v1\",\"providers\":[]}' ;; esac\n")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "invalid-helper", "", invalid, false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid manifest status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "invalid-helper")); !os.IsNotExist(err) {
		t.Fatalf("invalid helper should not be installed: %v", err)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "bad name", "", []byte(dnsUploadHelper), false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid name status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "other", "helper", "", []byte(dnsUploadHelper), false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind status = %d: %s", rec.Code, rec.Body.String())
	}
}

const deployUploadHelper = `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"uploaded deploy helper","version":"1.1.0","deployers":[{"id":"examplepanel","name":"Example Panel"}]}
JSON
  ;;
esac
`

func newDeployPluginTestServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	s, _, _ := newPluginTestServer(t)
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"test deploy helper","version":"1.0.0","deployers":[{"id":"ftp","name":"FTP"},{"id":"custompanel","name":"Custom Panel"}]}
JSON
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "deploy-helper"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.DeployPluginsEnabled = true
	s.DeployPluginDir = dir
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/entry/api/plugins/deployers", s.setDeployPluginDeployersEnabled)
	r.PUT("/entry/api/plugins/deployers/:id", s.setDeployPluginDeployerEnabled)
	t.Cleanup(func() {
		deployplugin.Discover(deployplugin.DiscoverConfig{})
	})
	return s, r
}

func TestDeployPluginDeployerToggle(t *testing.T) {
	s, r := newDeployPluginTestServer(t)
	result := s.DiscoverDeployPlugins()
	// 迁移前内置的 ftp 默认启用；自定义目标 custompanel 默认停用。
	if result.Deployers != 1 || len(result.Disabled) != 1 || result.Disabled[0] != "custompanel" {
		t.Fatalf("discover result: %+v", result)
	}
	if !deployplugin.Has("ftp") {
		t.Fatal("migrated built-in deployer should be enabled by default")
	}
	if deployplugin.Has("custompanel") {
		t.Fatal("custom deployer should stay disabled by default")
	}

	// 启用自定义目标后注册到运行时。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers/custompanel", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !deployplugin.Has("custompanel") {
		t.Fatal("enabled deployer should be registered")
	}
	meta, ok := deployplugin.ExternalDeployerInfo("custompanel")
	if !ok || !meta.Enabled {
		t.Fatalf("enabled metadata = %+v, %v", meta, ok)
	}

	// 停用迁移前内置的目标，状态会持久化。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers/ftp", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d: %s", rec.Code, rec.Body.String())
	}
	if deployplugin.Has("ftp") {
		t.Fatal("disabled deployer should be unregistered")
	}
	raw, _ := s.Store.GetSetting(deployPluginEnabledSetting)
	if strings.Contains(raw, "ftp") || !strings.Contains(raw, "custompanel") {
		t.Fatalf("enabled setting = %q", raw)
	}

	// 批量启用后再批量停用。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("bulk enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !deployplugin.Has("ftp") || !deployplugin.Has("custompanel") {
		t.Fatal("bulk enable should register all deployers")
	}
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("bulk disable status = %d: %s", rec.Code, rec.Body.String())
	}
	if deployplugin.Has("ftp") || deployplugin.Has("custompanel") {
		t.Fatal("bulk disable should unregister all deployers")
	}
}

func TestDeployPluginDeployerToggleRejectsBuiltinAndUnknown(t *testing.T) {
	s, r := newDeployPluginTestServer(t)
	s.DiscoverDeployPlugins()

	// 本地文件与 SSH 始终可用，不能停用。
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers/local", `{"enabled":false}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("builtin status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := putPluginToggle(t, r, "/entry/api/plugins/deployers/missingtarget", `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadDeployPlugin(t *testing.T) {
	s, r, _, dir := newPluginUploadTestServer(t, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "deploy", "deploy-helper", "", []byte(deployUploadHelper), false))
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy upload status = %d: %s", rec.Code, rec.Body.String())
	}
	// 新上传的 helper 默认停用文件级加载。
	if s.pluginFileEnabled(pluginKindDeploy, "deploy-helper") {
		t.Fatal("uploaded deploy helper should be disabled by default")
	}
	if err := s.updatePluginFileEnabled(pluginKindDeploy, "deploy-helper", true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverDeployPlugins()
	// 文件启用后条目仍默认停用，保留元数据供管理员在插件页启用。
	if _, ok := deployplugin.ExternalDeployerInfo("examplepanel"); !ok {
		t.Fatal("uploaded deployer metadata should be discovered")
	}
	if deployplugin.Has("examplepanel") {
		t.Fatal("uploaded deployer should stay disabled by default")
	}
	if _, _, err := s.updateDeployPluginDeployers([]string{"examplepanel"}, true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverDeployPlugins()
	if !deployplugin.Has("examplepanel") {
		t.Fatal("enabled uploaded deployer should be registered")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy-helper")); err != nil {
		t.Fatalf("uploaded deploy helper not found: %v", err)
	}
}

func TestUploadPluginAllowsLargeFile(t *testing.T) {
	_, r, dir, _ := newPluginUploadTestServer(t, true)
	// 普通 JSON 接口限制为 2 MB，插件上传路径必须单独放宽。
	large := []byte("#!/bin/sh\n" + strings.Repeat("# padding\n", 400_000) + `
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","providers":[{"id":"largedns","name":"Large DNS"}]}
JSON
  ;;
esac
`)
	if len(large) <= int(maxJSONBodySize) {
		t.Fatalf("test file should exceed JSON limit, got %d bytes", len(large))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "large-helper", "", large, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("large upload status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "large-helper")); err != nil {
		t.Fatalf("large uploaded helper not found: %v", err)
	}
}

func TestPromoteMergedDeployTypes(t *testing.T) {
	set := map[string]bool{
		"qiniu_cdn":       true,
		"baota_docker":    true,
		"1panel_site":     true,
		"acepanel_panel":  true,
		"safeline_portal": true,
		"tencent_cos":     true,
		"aliyun_oss":      true,
		"volcengine_dcdn": true,
		"ftp":             true,
	}
	promoteMergedDeployTypes(set)
	for _, id := range []string{"qiniu", "baota", "1panel", "acepanel", "safeline", "tencent", "aliyun", "volcengine", "ftp"} {
		if !set[id] {
			t.Errorf("merged deploy type %s should be promoted", id)
		}
	}
}

func TestListAndDeletePluginFile(t *testing.T) {
	s, _, dir := newPluginTestServer(t)
	if _, err := os.Stat(filepath.Join(dir, "helper")); err != nil {
		t.Fatalf("test helper missing: %v", err)
	}
	s.DiscoverPlugins()
	if _, _, err := s.updateDNSPluginProviders([]string{"alphadns"}, true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()

	files := s.pluginFiles(pluginKindDNS)
	if len(files) != 1 {
		t.Fatalf("plugin files = %+v", files)
	}
	file := files[0]
	if file.File != "helper" || file.Source != "manual" || file.Version != "1.0.0" {
		t.Fatalf("unexpected plugin file view: %+v", file)
	}
	if len(file.Items) != 2 || file.Items[0] != "alphadns" || file.Items[1] != "betadns" {
		t.Fatalf("plugin file items = %v", file.Items)
	}
	if raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting); !strings.Contains(raw, "alphadns") {
		t.Fatalf("provider should be enabled before deletion: %s", raw)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user", &model.User{ID: 1})
		c.Set("session_id", "test-session")
		c.Set("session_strong_auth", true)
		c.Next()
	})
	r.DELETE("/entry/api/plugins/files/:kind/:file", s.deletePluginFile)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/entry/api/plugins/files/dns/helper", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alphadns") {
		t.Fatalf("delete response should list removed items: %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "helper")); !os.IsNotExist(err) {
		t.Fatalf("plugin file still exists: %v", err)
	}
	if dnsplugin.Has("alphadns") {
		t.Fatal("deleted provider should no longer be registered")
	}
	if raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting); strings.Contains(raw, "alphadns") {
		t.Fatalf("deleted provider should be removed from whitelist: %s", raw)
	}

	// 重复删除返回 404，非法文件名返回 400。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/entry/api/plugins/files/dns/helper", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/entry/api/plugins/files/dns/bad%20name", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid name status = %d: %s", rec.Code, rec.Body.String())
	}

	// 符号链接不能被当作插件文件删除。
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked-helper")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/entry/api/plugins/files/dns/linked-helper", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("symlink delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target should be untouched: %v", err)
	}
}

func TestPluginFileTogglePreservesItemState(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	s.DiscoverPlugins()
	if _, _, err := s.updateDNSPluginProviders([]string{"alphadns"}, true); err != nil {
		t.Fatal(err)
	}
	result := s.DiscoverPlugins()
	if !dnsplugin.Has("alphadns") || result.Helpers != 1 {
		t.Fatalf("discover result: %+v", result)
	}
	meta, found, err := s.Store.GetPluginFileMeta(pluginKindDNS, "helper")
	if err != nil || !found || len(meta.Entries) != 2 {
		t.Fatalf("file meta = %+v, %v, %v", meta, found, err)
	}

	// 停用文件不需要二次验证；文件不再被探测，条目也不再注册。
	rec := putPluginToggle(t, r, "/entry/api/plugins/files/dns/helper", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable file status = %d: %s", rec.Code, rec.Body.String())
	}
	result = s.DiscoverPlugins()
	if len(result.DisabledFiles) != 1 || result.DisabledFiles[0] != "helper" {
		t.Fatalf("disabled files = %v", result.DisabledFiles)
	}
	if len(result.Errors) != 0 || dnsplugin.Has("alphadns") {
		t.Fatalf("disabled file should not be loaded: %+v", result)
	}

	// 停用后条目仍以影子条目的形式展示，并带有 file_enabled=false。
	shadows := s.shadowDNSProviders()
	if len(shadows) != 2 {
		t.Fatalf("shadow providers = %+v", shadows)
	}
	for _, item := range shadows {
		if item.Enabled || item.FileEnabled {
			t.Fatalf("shadow provider should be disabled: %+v", item)
		}
	}
	files := s.pluginFiles(pluginKindDNS)
	if len(files) != 1 || files[0].Enabled || len(files[0].Items) != 2 {
		t.Fatalf("file view = %+v", files)
	}

	// 列表接口应同时返回影子条目和文件级启停状态。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/entry/api/plugins", nil))
	var payload struct {
		Data struct {
			Providers []struct {
				ID          string `json:"id"`
				Enabled     bool   `json:"enabled"`
				FileEnabled bool   `json:"file_enabled"`
			} `json:"providers"`
			Files []struct {
				File    string   `json:"file"`
				Enabled bool     `json:"enabled"`
				Items   []string `json:"items"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode plugins response: %v: %s", err, rec.Body.String())
	}
	if len(payload.Data.Providers) != 2 {
		t.Fatalf("providers = %+v", payload.Data.Providers)
	}
	for _, provider := range payload.Data.Providers {
		if provider.Enabled || provider.FileEnabled {
			t.Fatalf("shadow provider = %+v", provider)
		}
	}
	if len(payload.Data.Files) != 1 || payload.Data.Files[0].Enabled || len(payload.Data.Files[0].Items) != 2 {
		t.Fatalf("files = %+v", payload.Data.Files)
	}

	// 保存白名单时不能因为文件停用而丢掉条目勾选。
	if err := s.saveDNSPluginEnabledSet(s.dnsPluginEnabledSet()); err != nil {
		t.Fatal(err)
	}
	raw, _ := s.Store.GetSetting(dnsPluginEnabledSetting)
	if !strings.Contains(raw, "alphadns") {
		t.Fatalf("enabled state should be preserved, got %q", raw)
	}

	// 启用文件只需要登录会话。
	rec = putPluginToggle(t, r, "/entry/api/plugins/files/dns/helper", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable file should succeed = %d: %s", rec.Code, rec.Body.String())
	}
	s.DiscoverPlugins()
	if !dnsplugin.Has("alphadns") {
		t.Fatal("re-enabled file should restore the enabled provider state")
	}
}

// TestUploadOverwritesRepoInstallationRecord 验证同名上传覆盖仓库 helper 后，
// 旧安装记录的摘要不会在发现阶段拒绝新文件。
func TestUploadOverwritesRepoInstallationRecord(t *testing.T) {
	s, r, dir, _ := newPluginUploadTestServer(t, true)
	if err := s.Store.UpsertPluginInstallation(model.PluginInstallation{
		Kind: "dns", ID: "gammadns", Version: "1.0.0",
		Filename: "uploaded-helper", SHA256: strings.Repeat("a", 64), Source: "repo",
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, pluginUploadRequest(t, "dns", "uploaded-helper", "", []byte(dnsUploadHelper), true))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, found, err := s.Store.GetPluginInstallation("dns", "gammadns"); err != nil || found {
		t.Fatalf("stale installation record should be removed: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploaded-helper")); err != nil {
		t.Fatalf("uploaded helper not found: %v", err)
	}
}
