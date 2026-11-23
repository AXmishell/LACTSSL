package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
	dnsplugin "lactssl/internal/plugin/dns"
)

func writeConflictTestFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestDetectPluginConflicts(t *testing.T) {
	s, _, dir := newPluginTestServer(t)
	writeConflictTestFile(t, dir, "lactssl-dns-old")
	s.upsertPluginFileMeta(pluginKindDNS, "lactssl-dns-old", "old bundle", "1.0.0", "sha", []string{"cloudflare", "route53"})
	if err := s.Store.UpsertPluginMetadata(model.PluginMetadata{
		Kind: pluginKindDNS, ID: "route53", Manifest: `{"id":"route53","aliases":["aws-route53"]}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.CreateDNSAccount(&model.DNSAccount{Name: "prod", Provider: "route53"}); err != nil {
		t.Fatal(err)
	}

	conflict := s.detectPluginConflicts(pluginKindDNS,
		[]string{"cloudflare", "aws-route53"}, nil, true,
		"dns-all-native", "DNS 全量包", "lactssl-dns-all-native")
	if conflict == nil {
		t.Fatal("expected conflicts")
	}
	if len(conflict.Conflicts) != 2 {
		t.Fatalf("conflicts = %d: %+v", len(conflict.Conflicts), conflict.Conflicts)
	}
	for _, item := range conflict.Conflicts {
		if item.Existing == nil || item.Existing.File != "lactssl-dns-old" {
			t.Fatalf("unexpected conflict: %+v", item)
		}
		if item.Existing.Covered {
			t.Fatalf("partial overlap must not be covered: %+v", item)
		}
		if len(item.Existing.Uncovered) != 1 || item.Existing.Uncovered[0] != "route53" {
			t.Fatalf("unexpected uncovered ids: %+v", item.Existing.Uncovered)
		}
		if item.Existing.Usage.Accounts != 1 {
			t.Fatalf("usage = %+v", item.Existing.Usage)
		}
		if len(item.Actions) == 0 || item.Actions[0] != "uninstall_existing" {
			t.Fatalf("actions = %+v", item.Actions)
		}
	}

	// 合并包完整覆盖旧文件时交给安装后的自动清理，不要求手动卸载。
	if conflict := s.detectPluginConflicts(pluginKindDNS,
		[]string{"cloudflare", "route53"}, nil, true,
		"dns-all-native", "DNS 全量包", "lactssl-dns-all-native"); conflict != nil {
		t.Fatalf("covered conflict should be auto-cleanable: %+v", conflict.Conflicts)
	}

	// 目标文件自身不参与比较，保证升级/重装不误报冲突。
	if conflict := s.detectPluginConflicts(pluginKindDNS,
		[]string{"cloudflare", "route53"}, nil, false,
		"dns-old", "old bundle", "lactssl-dns-old"); conflict != nil {
		t.Fatalf("upgrading the same file must not conflict: %+v", conflict.Conflicts)
	}
}

func TestDetectPluginConflictsIgnoresReplacedFile(t *testing.T) {
	s, _, dir := newPluginTestServer(t)
	writeConflictTestFile(t, dir, "lactssl-dns-old")
	s.upsertPluginFileMeta(pluginKindDNS, "lactssl-dns-old", "old", "1.0.0", "sha", []string{"cloudflare"})
	if err := s.Store.UpsertPluginInstallation(model.PluginInstallation{
		Kind: pluginKindDNS, ID: "cloudflare", Version: "1.0.0", Filename: "lactssl-dns-old",
	}); err != nil {
		t.Fatal(err)
	}
	// 文件名规则变化导致升级目标文件名改变时，旧记录文件会被卸载，不应算冲突。
	if conflict := s.detectPluginConflicts(pluginKindDNS,
		[]string{"cloudflare"}, nil, false, "cloudflare", "Cloudflare", "lactssl-dns-new", "lactssl-dns-old"); conflict != nil {
		t.Fatalf("replaced file must be ignored: %+v", conflict.Conflicts)
	}
}

func TestFailPluginConflictResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugins/repo/install", nil)
	c.Request.Header.Set("X-LACTSSL-Language", "zh-CN")

	err := &pluginConflictError{
		PluginID:   "dns-all-native",
		PluginName: "DNS 全量包",
		Conflicts: []pluginConflict{{
			Kind: pluginKindDNS, ID: "cloudflare", Type: pluginConflictTypeID,
			Existing: &pluginConflictExisting{Kind: pluginKindDNS, File: "lactssl-dns-old", Source: "manual", Enabled: true},
			Actions:  []string{"uninstall_existing", "cancel"},
		}},
	}
	if !failPluginConflict(c, err) {
		t.Fatal("expected conflict response")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code      string           `json:"code"`
		Conflicts []pluginConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "api.error.plugin_conflict" || len(body.Conflicts) != 1 {
		t.Fatalf("unexpected payload: %+v", body)
	}
	if body.Conflicts[0].Existing == nil || body.Conflicts[0].Existing.File != "lactssl-dns-old" {
		t.Fatalf("missing existing file: %+v", body.Conflicts[0])
	}
}

func TestDetectPluginConflictsBuiltin(t *testing.T) {
	s, _, _ := newPluginTestServer(t)
	conflict := s.detectPluginConflicts(pluginKindDeploy,
		[]string{"local"}, nil, false, "local", "local plugin", "lactssl-deploy-local")
	if conflict == nil || len(conflict.Conflicts) != 1 {
		t.Fatalf("expected built-in conflict: %+v", conflict)
	}
	item := conflict.Conflicts[0]
	if item.Type != pluginConflictTypeBuiltin || item.Existing != nil {
		t.Fatalf("unexpected built-in conflict: %+v", item)
	}
	if len(item.Actions) != 1 || item.Actions[0] != "cancel" {
		t.Fatalf("built-in conflict must not offer uninstall: %+v", item.Actions)
	}
}

// TestDetectPluginConflictsReservesDisabledBuiltin 验证未启用的内置条目
// 仍然占用 ID。修复前只检查运行期注册表，未启用的 cloudflare / webhook
// 不会被视为冲突，上传后会在发现阶段被静默跳过。
func TestDetectPluginConflictsReservesDisabledBuiltin(t *testing.T) {
	s, _, _ := newPluginTestServer(t)
	s.BuiltinPluginsEnabled = true
	s.DeployPluginsEnabled = true
	t.Cleanup(func() {
		s.PluginsEnabled = false
		s.DeployPluginsEnabled = false
		s.BuiltinPluginsEnabled = false
		s.DiscoverPlugins()
		s.DiscoverDeployPlugins()
	})

	s.DiscoverPlugins()
	s.DiscoverDeployPlugins()
	if dnsplugin.Has("cloudflare") {
		t.Fatal("cloudflare should be disabled before the test")
	}

	conflict := s.detectPluginConflicts(pluginKindDNS, []string{"cloudflare"}, nil, false, "helper", "Helper")
	if conflict == nil || len(conflict.Conflicts) != 1 {
		t.Fatalf("disabled builtin DNS provider should conflict: %+v", conflict)
	}
	if conflict.Conflicts[0].Type != pluginConflictTypeBuiltin {
		t.Fatalf("unexpected conflict type: %+v", conflict.Conflicts[0])
	}

	conflict = s.detectPluginConflicts(pluginKindDeploy, []string{"webhook"}, nil, false, "helper", "Helper")
	if conflict == nil || len(conflict.Conflicts) != 1 {
		t.Fatalf("builtin deploy target should conflict: %+v", conflict)
	}
	if conflict.Conflicts[0].Type != pluginConflictTypeBuiltin {
		t.Fatalf("unexpected conflict type: %+v", conflict.Conflicts[0])
	}
}
