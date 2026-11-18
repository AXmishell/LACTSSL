package api

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/repo"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func TestRepoStatusShowsPublicKeyValidation(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:               store.New(conn, cipher),
		PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pub),
	}
	status := s.repoStatus()
	if status["public_key_set"] != true || status["public_key_valid"] != true {
		t.Fatalf("unexpected key status: %+v", status)
	}
	if status["public_key_fingerprint"] != pluginrepo.PublicKeyFingerprint(pub) {
		t.Fatalf("fingerprint = %v", status["public_key_fingerprint"])
	}
	if status["signature_verified"] != false {
		t.Fatalf("signature should not be verified before loading: %+v", status)
	}

	s.pluginRepoVerified = true
	if s.repoStatus()["signature_verified"] != true {
		t.Fatal("verified index should be reported")
	}

	s.PluginRepoPublicKey = "not-a-key"
	status = s.repoStatus()
	if status["public_key_valid"] != false || status["signature_verified"] != false {
		t.Fatalf("invalid key should be rejected: %+v", status)
	}
	if status["public_key_fingerprint"] != "" {
		t.Fatalf("invalid key should not expose a fingerprint: %+v", status)
	}
}

func TestInstallRepoPlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is unix-only")
	}
	dir := t.TempDir()
	dataDir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"test dns plugin","version":"1.0.0","providers":[{"id":"cloudflare","name":"Cloudflare"}]}
JSON
  ;;
esac
`
	icon := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	iconSum := pluginrepo.IconSHA256(icon)
	pluginJSON := `{"schema":1,"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","entrypoint":"lactssl-dns-cloudflare","icons":{"cloudflare":{"file":"icons/cloudflare.svg","sha256":"` + iconSum + `"}}}`
	archivePath := filepath.Join(t.TempDir(), "plugin.tar.gz")
	writeTestArchive(t, archivePath, map[string]string{
		"plugin.json":            pluginJSON,
		"lactssl-dns-cloudflare": script,
		"icons/cloudflare.svg":   string(icon),
	})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	conn, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:                   store.New(conn, cipher),
		DataDir:                 dataDir,
		PluginsEnabled:          true,
		PluginDir:               dir,
		PluginMemoryLimitMB:     128,
		PluginTimeout:           5 * time.Second,
		PluginMaxOutputKB:       64,
		PluginRepoURL:           server.URL,
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	plugin := pluginrepo.Plugin{Kind: "dns", ID: "cloudflare", Version: "1.0.0", Protocol: "v1", Single: true}
	asset := pluginrepo.Asset{URL: server.URL + "/plugin.tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(archive))}
	record, _, err := s.installRepoPlugin("dns", plugin, asset)
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != "1.0.0" || record.Source != "repo" {
		t.Fatalf("unexpected record: %+v", record)
	}
	installed := filepath.Join(dir, "lactssl-dns-cloudflare")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("installed plugin should be executable")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugin-icons", "dns", "cloudflare", "cloudflare.svg")); err != nil {
		t.Fatalf("plugin icon not persisted: %v", err)
	}
	if _, found := dnsplugin.ExternalProviderInfo("cloudflare"); !found {
		t.Fatal("installed plugin should be discovered")
	}
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
}

func writeTestArchive(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestListPluginRepoPlugins(t *testing.T) {
	index := `{"schema":1,"channel":"stable","protocol":"v1","plugins":[
		{"kind":"dns","id":"cloudflare","name":"Cloudflare","version":"1.0.0","protocol":"v1","single":true,"assets":{"` + runtime.GOOS + `/` + runtime.GOARCH + `":{"url":"http://example.invalid/a.tgz","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":123}}},
		{"kind":"notify","id":"telegram","name":"Telegram","version":"1.0.0","protocol":"v1","single":true,"assets":{"` + runtime.GOOS + `/` + runtime.GOARCH + `":{"url":"http://example.invalid/t.tgz","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":456}}}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(index))
	}))
	defer server.Close()
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
	s := &Server{
		Store:                   st,
		PluginRepoEnabled:       true,
		PluginRepoURL:           server.URL,
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	if err := st.UpsertPluginInstallation(model.PluginInstallation{
		Kind: "dns", ID: "cloudflare", Version: "0.9.0", Filename: "lactssl-dns-cloudflare", Source: "repo",
	}); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/plugins/repo/plugins", s.listPluginRepoPlugins)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugins/repo/plugins", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Plugins []repoPluginView `json:"plugins"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Plugins) != 2 {
		t.Fatalf("plugins=%d", len(body.Data.Plugins))
	}
	for _, item := range body.Data.Plugins {
		if item.ID == "cloudflare" && (!item.Installed || !item.UpdateAvailable || item.InstalledVersion != "0.9.0") {
			t.Fatalf("unexpected cloudflare view: %+v", item)
		}
	}
}

func TestInstallRepoBundleReplacesSinglePlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is unix-only")
	}
	dir := t.TempDir()
	oldScript := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"old dns plugin","version":"0.9.0","providers":[{"id":"cloudflare"}]}
JSON
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "lactssl-dns-cloudflare"), []byte(oldScript), 0o755); err != nil {
		t.Fatal(err)
	}

	bundleScript := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"dns bundle","version":"1.0.0","providers":[{"id":"cloudflare"},{"id":"route53"}]}
JSON
  ;;
esac
`
	pluginJSON := `{"schema":1,"kind":"dns","id":"all","version":"1.0.0","protocol":"v1","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","entrypoint":"lactssl-dns-all","bundle":true,"providers":["cloudflare","route53"]}`
	archivePath := filepath.Join(t.TempDir(), "bundle.tar.gz")
	writeTestArchive(t, archivePath, map[string]string{
		"plugin.json":     pluginJSON,
		"lactssl-dns-all": bundleScript,
	})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

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
	if err := st.UpsertPluginInstallation(model.PluginInstallation{
		Kind: "dns", ID: "cloudflare", Version: "0.9.0", Filename: "lactssl-dns-cloudflare", Source: "repo",
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:                   st,
		PluginsEnabled:          true,
		PluginDir:               dir,
		PluginMemoryLimitMB:     128,
		PluginTimeout:           5 * time.Second,
		PluginMaxOutputKB:       64,
		PluginRepoURL:           server.URL,
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	plugin := pluginrepo.Plugin{
		Kind: "dns", ID: "all", Version: "1.0.0", Protocol: "v1", Bundle: true,
		Providers: []string{"cloudflare", "route53"},
	}
	asset := pluginrepo.Asset{URL: server.URL + "/bundle.tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(archive))}
	record, scanErrors, err := s.installRepoPlugin("dns", plugin, asset)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "all" || record.Filename != "lactssl-dns-all" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if len(scanErrors) != 0 {
		t.Fatalf("scan errors: %v", scanErrors)
	}
	if _, err := os.Stat(filepath.Join(dir, "lactssl-dns-cloudflare")); !os.IsNotExist(err) {
		t.Fatalf("old single plugin should be removed, err=%v", err)
	}
	if _, found, _ := st.GetPluginInstallation("dns", "cloudflare"); found {
		t.Fatal("old installation record should be removed")
	}
	for _, id := range []string{"cloudflare", "route53"} {
		if _, found := dnsplugin.ExternalProviderInfo(id); !found {
			t.Fatalf("provider %s should be discovered", id)
		}
	}
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
}

func TestInstallSplitBundlesReplacesLegacyHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is unix-only")
	}
	dir := t.TempDir()
	legacy := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"legacy dns helper","version":"0.9.0","providers":[{"id":"cloudflare"},{"id":"route53"}]}
JSON
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "lactssl-dns-helper"), []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}

	makeArchive := func(kind, id string, providers []string) []byte {
		script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"` + id + `","version":"1.0.0","providers":[` + providerJSON(providers) + `]}
JSON
  ;;
esac
`
		pluginJSON := `{"schema":1,"kind":"` + kind + `","id":"` + id + `","version":"1.0.0","protocol":"v1","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","entrypoint":"lactssl-dns-` + id + `","bundle":true,"providers":` + stringListJSON(providers) + `}`
		archivePath := filepath.Join(t.TempDir(), id+".tar.gz")
		writeTestArchive(t, archivePath, map[string]string{
			"plugin.json":       pluginJSON,
			"lactssl-dns-" + id: script,
		})
		raw, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	coreArchive := makeArchive("dns", "core", []string{"cloudflare"})
	heavyArchive := makeArchive("dns", "heavy", []string{"route53"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/core.tgz":
			_, _ = w.Write(coreArchive)
		case "/heavy.tgz":
			_, _ = w.Write(heavyArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	conn, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:                   store.New(conn, cipher),
		PluginsEnabled:          true,
		PluginDir:               dir,
		PluginMemoryLimitMB:     128,
		PluginTimeout:           5 * time.Second,
		PluginMaxOutputKB:       64,
		PluginRepoURL:           server.URL,
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	install := func(id, path string, raw []byte, providers []string) {
		t.Helper()
		sum := sha256.Sum256(raw)
		plugin := pluginrepo.Plugin{Kind: "dns", ID: id, Version: "1.0.0", Protocol: "v1", Bundle: true, Providers: providers}
		asset := pluginrepo.Asset{URL: server.URL + path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))}
		if _, _, err := s.installRepoPlugin("dns", plugin, asset); err != nil {
			t.Fatal(err)
		}
	}
	install("core", "/core.tgz", coreArchive, []string{"cloudflare"})
	if _, err := os.Stat(filepath.Join(dir, "lactssl-dns-helper")); err != nil {
		t.Fatalf("legacy helper must survive until all its providers are covered: %v", err)
	}
	install("heavy", "/heavy.tgz", heavyArchive, []string{"route53"})
	if _, err := os.Stat(filepath.Join(dir, "lactssl-dns-helper")); !os.IsNotExist(err) {
		t.Fatalf("legacy helper should be removed after both bundles are installed, err=%v", err)
	}
	for _, id := range []string{"cloudflare", "route53"} {
		if _, found := dnsplugin.ExternalProviderInfo(id); !found {
			t.Fatalf("provider %s should be discovered", id)
		}
	}
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
}

func stringListJSON(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, `"`+id+`"`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func providerJSON(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, `{"id":"`+id+`"}`)
	}
	return strings.Join(parts, ",")
}

// TestInstallRepoPluginRequiresVerifiedIndex 验证配置公钥后，未通过验签的索引
// 不能用于安装插件，避免“只校验 HTTPS + sha256”的降级路径。
func TestInstallRepoPluginRequiresVerifiedIndex(t *testing.T) {
	st := newSessionTestStore(t)
	s := &Server{
		Store:               st,
		PluginsEnabled:      true,
		PluginDir:           t.TempDir(),
		PluginRepoURL:       "https://example.com/index.json",
		PluginRepoPublicKey: "t7Q4vd0B1zh3mRf0t6Q6qlBViL7cKhoipgpGWx5WSss=",
	}
	_, _, err := s.installRepoPlugin("dns", pluginrepo.Plugin{Kind: "dns", ID: "cloudflare"}, pluginrepo.Asset{})
	if err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("未验签的索引应拒绝安装: %v", err)
	}
}

// TestRepoPublicKeyCanBeExplicitlyCleared 验证 UI 清空公钥后不会回退到配置默认值，
// 否则用户无法按文档关闭验签。
func TestRepoPublicKeyCanBeExplicitlyCleared(t *testing.T) {
	st := newSessionTestStore(t)
	s := &Server{Store: st, PluginRepoPublicKey: "configured-default"}
	if got := s.repoPublicKey(); got != "configured-default" {
		t.Fatalf("未写入设置时应回退到配置默认值: %q", got)
	}
	if err := st.SetSetting(repoPublicKeySetting, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.repoPublicKey(); got != "" {
		t.Fatalf("显式清空后不应回退到默认值: %q", got)
	}
}

// TestCachedRepoIndexRejectedWhenPublicKeyChanged 验证仅修改配置文件公钥并重启时，
// 旧索引不会沿用“已验签”状态被安装。
func TestCachedRepoIndexRejectedWhenPublicKeyChanged(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-cache-test-key"))
	raw := `{"schema":1,"protocol":"v1","plugins":[]}`
	marker := &Server{Auth: manager}
	if err := st.SetSetting(repoIndexSetting, raw); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoCheckedSetting, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedSetting, "true"); err != nil {
		t.Fatal(err)
	}

	keyA := make([]byte, ed25519.PublicKeySize)
	keyB := make([]byte, ed25519.PublicKeySize)
	keyA[0] = 1
	keyB[0] = 2
	fingerprintA := pluginrepo.PublicKeyFingerprint(ed25519.PublicKey(keyA))
	if err := st.SetSetting(repoVerifiedKeySetting, fingerprintA); err != nil {
		t.Fatal(err)
	}

	same := &Server{Store: st, Auth: manager, PluginRepoPublicKey: base64.StdEncoding.EncodeToString(keyA)}
	if _, ok := same.loadCachedRepoIndex(); !ok {
		t.Fatal("公钥未变化时缓存索引应可用")
	}

	changed := &Server{Store: st, Auth: manager, PluginRepoPublicKey: base64.StdEncoding.EncodeToString(keyB)}
	if _, ok := changed.loadCachedRepoIndex(); ok {
		t.Fatal("公钥变化后不得沿用旧索引的验签状态")
	}
}
