package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"lactssl/internal/plugin/repo"
)

func TestSavePluginIconsAndMerge(t *testing.T) {
	dataDir := t.TempDir()
	src := filepath.Join(dataDir, "extract-icons")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if err := os.WriteFile(filepath.Join(src, "cloudflare.svg"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{DataDir: dataDir}
	refs := map[string]pluginrepo.IconFile{
		"cloudflare": {File: "icons/cloudflare.svg", SHA256: pluginrepo.IconSHA256(raw)},
	}
	if err := s.savePluginIcons("dns", "core", refs, src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "baota.png"), []byte("baota"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.savePluginIcons("deploy", "all", map[string]pluginrepo.IconFile{
		"baota": {File: "icons/baota.png", SHA256: pluginrepo.IconSHA256([]byte("baota"))},
	}, src); err != nil {
		t.Fatal(err)
	}
	icons := s.pluginIconMap()
	if icons["dns"]["cloudflare"] == "" {
		t.Fatalf("package icon missing: %+v", icons)
	}
	if icons["deploy"]["baota_site"] == "" {
		t.Fatalf("deploy alias should reuse canonical icon offline: %+v", icons["deploy"])
	}

	// 索引图标作为兜底，并被已安装包内图标覆盖。
	old := pluginrepo.EncodeIconDataURI("image/png", []byte("old"))
	newIcon := pluginrepo.EncodeIconDataURI("image/svg+xml", raw)
	s.pluginRepoIndex = pluginrepo.Index{Icons: []pluginrepo.Icon{
		{Kind: "dns", ID: "cloudflare", MIME: "image/png", SHA256: pluginrepo.IconSHA256([]byte("old")), Data: old},
		{Kind: "deploy", ID: "1panel", MIME: "image/svg+xml", SHA256: pluginrepo.IconSHA256(raw), Data: newIcon},
	}}
	icons = s.pluginIconMap()
	if icons["dns"]["cloudflare"] == old {
		t.Fatal("package icon should override index icon")
	}
	if icons["deploy"]["1panel"] != newIcon {
		t.Fatalf("index icon missing: %+v", icons)
	}

	if err := s.removePluginIcons("dns", "core"); err != nil {
		t.Fatal(err)
	}
	icons = s.pluginIconMap()
	if icons["dns"]["cloudflare"] != old {
		t.Fatal("removed package icon should fall back to index icon")
	}
}

func TestSavePluginIconsRejectsBadDigest(t *testing.T) {
	dataDir := t.TempDir()
	src := filepath.Join(dataDir, "extract-icons")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "baota.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{DataDir: dataDir}
	refs := map[string]pluginrepo.IconFile{
		"baota": {File: "icons/baota.png", SHA256: "deadbeef"},
	}
	if err := s.savePluginIcons("deploy", "all", refs, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, pluginIconDir, "deploy", "all")); !os.IsNotExist(err) {
		t.Fatal("invalid icon should not be persisted")
	}
}

func TestListPluginIconsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()
	s := &Server{DataDir: dataDir}
	raw := []byte("svg")
	s.pluginRepoIndex = pluginrepo.Index{Icons: []pluginrepo.Icon{{
		Kind: "dns", ID: "alidns", MIME: "image/svg+xml",
		SHA256: pluginrepo.IconSHA256(raw),
		Data:   pluginrepo.EncodeIconDataURI("image/svg+xml", raw),
	}}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	s.listPluginIcons(ctx)
	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") == "" {
		t.Fatal("missing cache header")
	}
	var body struct {
		Success bool                         `json:"success"`
		Data    map[string]map[string]string `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Data["dns"]["alidns"] == "" {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestPluginMarketIcon(t *testing.T) {
	icons := map[string]map[string]string{"dns": {"alidns": "a", "cloudflare": "c", "core": "bundle"}}
	single := pluginrepo.Plugin{Kind: "dns", ID: "cloudflare"}
	if got := pluginMarketIcon(single, icons); got != "c" {
		t.Fatalf("single icon = %q", got)
	}
	bundle := pluginrepo.Plugin{Kind: "dns", ID: "core", Bundle: true, Providers: []string{"unknown", "alidns"}}
	if got := pluginMarketIcon(bundle, icons); got != "bundle" {
		t.Fatalf("bundle icon = %q", got)
	}
	providerIconOnly := pluginrepo.Plugin{Kind: "dns", ID: "heavy", Bundle: true, Providers: []string{"alidns"}}
	if got := pluginMarketIcon(providerIconOnly, icons); got != "" {
		t.Fatalf("bundle should not borrow provider icon = %q", got)
	}
	if got := pluginMarketIcon(pluginrepo.Plugin{Kind: "dns", ID: "x"}, icons); got != "" {
		t.Fatalf("missing icon = %q", got)
	}
	hidden := pluginrepo.Plugin{Kind: "dns", ID: "core", Bundle: true, Providers: []string{"alidns"}, HideIcon: true}
	if got := pluginMarketIcon(hidden, icons); got != "" {
		t.Fatalf("hidden icon = %q", got)
	}
}
