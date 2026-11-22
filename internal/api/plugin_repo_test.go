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
	"strconv"
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

func TestRepoProxyFallbackAndOverride(t *testing.T) {
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
		Store:           store.New(conn, cipher),
		PluginRepoProxy: "http://127.0.0.1:7890",
	}
	if got := s.repoProxy(); got != "http://127.0.0.1:7890" {
		t.Fatalf("configuration fallback not used: %q", got)
	}
	if got := s.repoStatus()["proxy"]; got != "http://127.0.0.1:7890" {
		t.Fatalf("status proxy = %v", got)
	}

	const runtimeProxy = "socks5://user:pass@127.0.0.1:1080"
	if err := s.Store.SetSecret(repoProxySetting, runtimeProxy); err != nil {
		t.Fatal(err)
	}
	if got := s.repoProxy(); got != runtimeProxy {
		t.Fatalf("runtime proxy not used: %q", got)
	}
	status := s.repoStatus()
	if status["proxy"] != "socks5://127.0.0.1:1080" || status["proxy_set"] != true {
		t.Fatalf("status 不应回显代理凭据: %+v", status)
	}

	// 显式保存空值应关闭代理，而不是回退到配置文件。
	if err := s.Store.SetSecret(repoProxySetting, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.repoProxy(); got != "" {
		t.Fatalf("explicit empty proxy should override configuration: %q", got)
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
	audit := pluginrepo.IndexAudit{Revision: 7, IndexDigest: "index-digest", EntryDigest: pluginrepo.PluginDigest(plugin)}
	record, _, err := s.installRepoPluginWithAudit("dns", plugin, asset, audit)
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != "1.0.0" || record.Source != "repo" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.IndexRevision != 7 || record.IndexDigest != "index-digest" || record.EntryDigest == "" {
		t.Fatalf("audit fields = %+v", record)
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
	// 新下载的插件默认停用文件级加载，启用后才探测 manifest。
	if s.pluginFileEnabled(pluginKindDNS, "lactssl-dns-cloudflare") {
		t.Fatal("installed helper should be disabled by default")
	}
	if _, found := dnsplugin.ExternalProviderInfo("cloudflare"); found {
		t.Fatal("disabled helper should not be discovered")
	}
	if err := s.updatePluginFileEnabled(pluginKindDNS, "lactssl-dns-cloudflare", true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()
	if meta, found := dnsplugin.ExternalProviderInfo("cloudflare"); !found {
		t.Fatal("installed plugin should be discovered after enabling the file")
	} else if meta.Enabled {
		t.Fatal("items must stay disabled after enabling the file")
	}
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })
}

// TestRepoInstallPreservesFileStateOnUpgrade 验证升级同名 helper 时不会重新套用
// “新下载默认停用”，管理员已启用的文件在升级后仍保持启用。
func TestRepoInstallPreservesFileStateOnUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is unix-only")
	}
	dir := t.TempDir()
	archives := map[string][]byte{}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"test dns plugin","version":"` + version + `","providers":[{"id":"cloudflare"}]}
JSON
  ;;
esac
`
		pluginJSON := `{"schema":1,"kind":"dns","id":"cloudflare","version":"` + version + `","protocol":"v1","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","entrypoint":"lactssl-dns-cloudflare"}`
		path := filepath.Join(t.TempDir(), version+".tar.gz")
		writeTestArchive(t, path, map[string]string{
			"plugin.json":            pluginJSON,
			"lactssl-dns-cloudflare": script,
		})
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		archives[version] = raw
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := archives[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".tar.gz")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
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
	install := func(version string) {
		t.Helper()
		raw := archives[version]
		sum := sha256.Sum256(raw)
		plugin := pluginrepo.Plugin{Kind: "dns", ID: "cloudflare", Version: version, Protocol: "v1", Single: true}
		asset := pluginrepo.Asset{URL: server.URL + "/" + version + ".tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))}
		if _, _, err := s.installRepoPlugin("dns", plugin, asset); err != nil {
			t.Fatal(err)
		}
	}
	install("1.0.0")
	if s.pluginFileEnabled(pluginKindDNS, "lactssl-dns-cloudflare") {
		t.Fatal("newly installed helper should be disabled")
	}
	if err := s.updatePluginFileEnabled(pluginKindDNS, "lactssl-dns-cloudflare", true); err != nil {
		t.Fatal(err)
	}
	install("1.1.0")
	if !s.pluginFileEnabled(pluginKindDNS, "lactssl-dns-cloudflare") {
		t.Fatal("upgrading an installed helper must preserve the file state")
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

// TestUpdatePluginFromRepoRefreshesStatus 验证更新接口返回的结构足以让前端立即
// 刷新状态，并且更新后列表中的 installed_version / update_available 同步变化。
func TestUpdatePluginFromRepoRefreshesStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test is unix-only")
	}
	dir := t.TempDir()
	archives := map[string][]byte{}
	for _, version := range []string{"0.9.0", "1.0.0"} {
		script := `#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
{"protocol":"v1","name":"test dns plugin","version":"` + version + `","providers":[{"id":"cloudflare"}]}
JSON
  ;;
esac
`
		pluginJSON := `{"schema":1,"kind":"dns","id":"cloudflare","version":"` + version + `","protocol":"v1","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","entrypoint":"lactssl-dns-cloudflare"}`
		path := filepath.Join(t.TempDir(), version+".tar.gz")
		writeTestArchive(t, path, map[string]string{
			"plugin.json":            pluginJSON,
			"lactssl-dns-cloudflare": script,
		})
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		archives[version] = raw
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := archives[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".tar.gz")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	newRaw := archives["1.0.0"]
	newSum := sha256.Sum256(newRaw)
	index := `{"schema":1,"channel":"stable","protocol":"v1","plugins":[{"kind":"dns","id":"cloudflare","name":"Cloudflare","version":"1.0.0","protocol":"v1","single":true,"assets":{"` + runtime.GOOS + `/` + runtime.GOARCH + `":{"url":"` + server.URL + `/1.0.0.tar.gz","sha256":"` + hex.EncodeToString(newSum[:]) + `","size":` + strconv.Itoa(len(newRaw)) + `}}}]}`
	indexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(index))
	}))
	defer indexServer.Close()

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
		PluginRepoEnabled:       true,
		PluginsEnabled:          true,
		PluginDir:               dir,
		PluginMemoryLimitMB:     128,
		PluginTimeout:           5 * time.Second,
		PluginMaxOutputKB:       64,
		PluginRepoURL:           indexServer.URL,
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	oldRaw := archives["0.9.0"]
	oldSum := sha256.Sum256(oldRaw)
	oldPlugin := pluginrepo.Plugin{Kind: "dns", ID: "cloudflare", Version: "0.9.0", Protocol: "v1", Single: true}
	oldAsset := pluginrepo.Asset{URL: server.URL + "/0.9.0.tar.gz", SHA256: hex.EncodeToString(oldSum[:]), Size: int64(len(oldRaw))}
	if _, _, err := s.installRepoPlugin("dns", oldPlugin, oldAsset); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.Discover(dnsplugin.DiscoverConfig{}) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/plugins/repo/plugins", s.listPluginRepoPlugins)
	r.POST("/plugins/repo/update", s.updatePluginFromRepo)

	listPlugins := func() repoPluginView {
		t.Helper()
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
		if len(body.Data.Plugins) != 1 {
			t.Fatalf("plugins=%+v", body.Data.Plugins)
		}
		return body.Data.Plugins[0]
	}

	if before := listPlugins(); !before.UpdateAvailable || before.InstalledVersion != "0.9.0" {
		t.Fatalf("unexpected state before update: %+v", before)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plugins/repo/update", strings.NewReader(`{"kind":"dns","id":"cloudflare"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Data struct {
			Results []struct {
				Kind    string                   `json:"kind"`
				ID      string                   `json:"id"`
				To      string                   `json:"to"`
				Updated bool                     `json:"updated"`
				Error   string                   `json:"error"`
				Plugin  model.PluginInstallation `json:"plugin"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Data.Results) != 1 {
		t.Fatalf("results=%+v", updated.Data.Results)
	}
	item := updated.Data.Results[0]
	if !item.Updated || item.Error != "" || item.To != "1.0.0" || item.Plugin.Version != "1.0.0" {
		t.Fatalf("unexpected update result: %+v", item)
	}

	after := listPlugins()
	if after.UpdateAvailable || after.InstalledVersion != "1.0.0" {
		t.Fatalf("status not refreshed after update: %+v", after)
	}

	// 再次更新应明确返回 updated=false，前端据此提示“已是最新”，而不是报成功。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/plugins/repo/update", strings.NewReader(`{"kind":"dns","id":"cloudflare"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	updated.Data.Results = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Data.Results) != 1 || updated.Data.Results[0].Updated {
		t.Fatalf("repeat update should be a no-op: %+v", updated.Data.Results)
	}

	// 指定未安装的插件时返回 404，避免前端把静默成功当成已更新。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/plugins/repo/update", strings.NewReader(`{"kind":"dns","id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
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
	if err := s.updatePluginFileEnabled(pluginKindDNS, "lactssl-dns-all", true); err != nil {
		t.Fatal(err)
	}
	s.DiscoverPlugins()
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
	for _, name := range []string{"lactssl-dns-core", "lactssl-dns-heavy"} {
		if err := s.updatePluginFileEnabled(pluginKindDNS, name, true); err != nil {
			t.Fatal(err)
		}
	}
	s.DiscoverPlugins()
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
// 旧索引不会沿用“已验签”状态被安装；缓存必须由当前公钥重新验签。
func TestCachedRepoIndexRejectedWhenPublicKeyChanged(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-cache-test-key"))
	raw := []byte(`{"schema":1,"protocol":"v1","plugins":[]}`)
	marker := &Server{Auth: manager}
	if err := st.SetSetting(repoIndexSetting, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(string(raw))); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoCheckedSetting, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	pubA, privA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privA, raw)
	if err := st.SetSetting(repoIndexSigSetting, base64.StdEncoding.EncodeToString(signature)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedSetting, "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedKeySetting, pluginrepo.PublicKeyFingerprint(pubA)); err != nil {
		t.Fatal(err)
	}

	same := &Server{Store: st, Auth: manager, PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pubA)}
	if _, ok := same.loadCachedRepoIndex(); !ok {
		t.Fatal("公钥未变化且签名有效时缓存索引应可用")
	}
	if !same.repoSignatureVerified() {
		t.Fatal("有效签名应被标记为已验签")
	}

	changed := &Server{Store: st, Auth: manager, PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pubB)}
	if _, ok := changed.loadCachedRepoIndex(); ok {
		t.Fatal("公钥变化后不得沿用旧索引的验签状态")
	}
}

// TestRepoStatusRestoresVerifiedCacheAfterRefreshFailure 验证进程启动后后台刷新失败时，
// 状态接口会恢复本地已验签缓存，而不是误报签名校验失败。
func TestRepoStatusRestoresVerifiedCacheAfterRefreshFailure(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-status-test-key"))
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema":1,"protocol":"v1","plugins":[]}`)
	signature := ed25519.Sign(priv, raw)
	marker := &Server{Auth: manager}
	if err := st.SetSetting(repoIndexSetting, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(string(raw))); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexSigSetting, base64.StdEncoding.EncodeToString(signature)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoCheckedSetting, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:               st,
		Auth:                manager,
		PluginRepoEnabled:   true,
		PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pub),
	}
	s.pluginRepoErr = "dial tcp: network unreachable"
	status := s.repoStatus()
	if status["signature_verified"] != true {
		t.Fatalf("有效缓存应被标记为已验签: %+v", status)
	}
	if status["error"] != "dial tcp: network unreachable" {
		t.Fatalf("恢复缓存后仍应保留最近一次刷新错误: %+v", status)
	}
}

// TestCachedRepoIndexRejectsUnsignedCache 验证配置公钥后，历史无签名缓存
// 即使数据库标记为“已验签”也不能被信任。
func TestCachedRepoIndexRejectsUnsignedCache(t *testing.T) {
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
	if err := st.SetSetting(repoETagSetting, `"stale"`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedSetting, "true"); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedKeySetting, pluginrepo.PublicKeyFingerprint(pub)); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, Auth: manager, PluginRepoPublicKey: base64.StdEncoding.EncodeToString(pub)}
	if _, ok := s.loadCachedRepoIndex(); ok {
		t.Fatal("无签名的历史缓存不应被当作已验签")
	}
	if etag, _ := st.GetSetting(repoETagSetting); etag != "" {
		t.Fatalf("拒绝缓存后应清空 ETag，避免 304 复用: %q", etag)
	}
}

// TestFetchRepoIndexRevalidatesOn304 验证 304 分支不会把未验签缓存标记为已验签，
// 而是强制全量重取并校验 .sig。
func TestFetchRepoIndexRevalidatesOn304(t *testing.T) {
	st := newSessionTestStore(t)
	manager := auth.New([]byte("repo-304-test-key"))
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema":1,"protocol":"v1","plugins":[]}`)
	signature := ed25519.Sign(priv, raw)
	var notModified, fullFetch, sigFetch int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			if r.Header.Get("If-None-Match") == `"v1"` {
				notModified++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			fullFetch++
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write(raw)
		case "/index.json.sig":
			sigFetch++
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(signature)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 模拟升级前的无签名缓存：数据库标记为已验签，但没有保存签名。
	marker := &Server{Auth: manager}
	if err := st.SetSetting(repoIndexSetting, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoIndexMACSetting, marker.repoIndexMAC(string(raw))); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoETagSetting, `"v1"`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedSetting, "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(repoVerifiedKeySetting, pluginrepo.PublicKeyFingerprint(pub)); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Store:                   st,
		Auth:                    manager,
		PluginRepoURL:           server.URL + "/index.json",
		PluginRepoPublicKey:     base64.StdEncoding.EncodeToString(pub),
		PluginRepoTimeout:       5 * time.Second,
		PluginRepoMaxDownloadMB: 8,
	}
	if _, err := s.fetchRepoIndex(true); err != nil {
		t.Fatal(err)
	}
	if !s.repoSignatureVerified() {
		t.Fatal("304 后应通过全量重取完成验签")
	}
	if notModified == 0 || fullFetch == 0 || sigFetch == 0 {
		t.Fatalf("304 应触发全量重取并请求签名: 304=%d full=%d sig=%d", notModified, fullFetch, sigFetch)
	}
}

func TestRepoUpdateAvailableUsesAssetHashForSameVersion(t *testing.T) {
	plugin := pluginrepo.Plugin{Kind: "dns", ID: "all-lego", Version: "4.35.2"}
	asset := pluginrepo.Asset{SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	record := model.PluginInstallation{Kind: "dns", ID: "all-lego", Version: "4.35.2", AssetSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if !repoUpdateAvailable(plugin, asset, record) {
		t.Fatal("same version with a different asset hash must be updateable")
	}
	record.AssetSHA256 = asset.SHA256
	if repoUpdateAvailable(plugin, asset, record) {
		t.Fatal("same version and same asset hash should be up to date")
	}
	record.AssetSHA256 = ""
	if repoUpdateAvailable(plugin, asset, record) {
		t.Fatal("missing installed asset hash should not trigger a blind update")
	}
}
