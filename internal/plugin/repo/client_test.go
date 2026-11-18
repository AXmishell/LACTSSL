package pluginrepo

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.0", "1.2.0", 0},
		{"1.2.1", "1.2.0", 1},
		{"1.2.0", "1.3.0", -1},
		{"v1.2.0", "1.2.0", 0},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc.1", 1},
	}
	for _, tc := range cases {
		if got := CompareVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersion(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompatible(t *testing.T) {
	if !Compatible(Plugin{Protocol: "v1", MinHost: "1.2.0"}, "v1.2.1") {
		t.Fatal("should be compatible")
	}
	if Compatible(Plugin{Protocol: "v1", MinHost: "1.3.0"}, "1.2.1") {
		t.Fatal("should be incompatible")
	}
	if Compatible(Plugin{Protocol: "v2"}, "1.2.1") {
		t.Fatal("protocol mismatch should be incompatible")
	}
	if !Compatible(Plugin{Protocol: "v1", MinHost: "1.2.0"}, "1.2.0-3-ga28f63b-dirty") {
		t.Fatal("git describe suffix should not make the host older")
	}
}

func TestSelectAsset(t *testing.T) {
	p := Plugin{Assets: map[string]Asset{"linux/amd64": {URL: "x"}}}
	if _, ok := SelectAsset(p, "linux", "amd64"); !ok {
		t.Fatal("asset missing")
	}
	if _, ok := SelectAsset(p, "windows", "amd64"); ok {
		t.Fatal("unexpected asset")
	}
}

func TestExtractPackage(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "plugin.tar.gz")
	pluginJSON := []byte(`{"schema":1,"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","os":"linux","arch":"amd64","entrypoint":"lactssl-dns-cloudflare"}`)
	writeArchive(t, archive, map[string]string{
		"plugin.json":            string(pluginJSON),
		"lactssl-dns-cloudflare": "binary",
		"LICENSE":                "license",
	})
	dest := filepath.Join(dir, "extract")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	meta, binary, err := ExtractPackage(archive, dest)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "cloudflare" || filepath.Base(binary) != "lactssl-dns-cloudflare" {
		t.Fatalf("unexpected meta=%+v binary=%s", meta, binary)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPackageRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bad.tar.gz")
	writeArchive(t, archive, map[string]string{"../evil": "x"})
	if _, _, err := ExtractPackage(archive, dir); err == nil {
		t.Fatal("path traversal should be rejected")
	}
}

func TestFetchAndSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	index := []byte(`{"schema":1,"channel":"stable","protocol":"v1","plugins":[{"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","assets":{"linux/amd64":{"url":"http://x/a.tgz","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":1}}}]}`)
	sig := ed25519.Sign(priv, index)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(index)
		case "/index.json.sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/index.json", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _, _, _, err := client.Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plugins) != 1 || got.Plugins[0].ID != "cloudflare" {
		t.Fatalf("unexpected index: %+v", got)
	}

	badClient, _ := NewClient(server.URL+"/index.json", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	index[0] = '['
	if _, _, _, _, _, err := badClient.Fetch(context.Background(), ""); err == nil {
		t.Fatal("signature mismatch should fail")
	}
}

func TestParsePublicKeyAndFingerprint(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(pub),
		base64.RawStdEncoding.EncodeToString(pub),
	} {
		parsed, err := ParsePublicKey(encoded)
		if err != nil {
			t.Fatalf("ParsePublicKey(%q): %v", encoded, err)
		}
		if !parsed.Equal(pub) {
			t.Fatal("parsed key does not match")
		}
	}
	for _, invalid := range []string{"", "not-base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := ParsePublicKey(invalid); err == nil {
			t.Fatalf("ParsePublicKey(%q) should fail", invalid)
		}
	}
	fingerprint := PublicKeyFingerprint(pub)
	if !strings.HasPrefix(fingerprint, "SHA256:") || fingerprint == PublicKeyFingerprint(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))) {
		t.Fatalf("unexpected fingerprint: %s", fingerprint)
	}
}

func TestDownloadRejectsInsecureAssetScheme(t *testing.T) {
	payload := []byte("plugin archive")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClient("https://example.com/index.json", "stable", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Download(context.Background(), Asset{
		URL: server.URL + "/asset", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}, filepath.Join(t.TempDir(), "asset.tgz")); err == nil {
		t.Fatal("HTTPS 索引引用 HTTP 资产应被拒绝")
	}
	fileClient, err := NewClient("https://example.com/index.json", "stable", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileClient.Download(context.Background(), Asset{
		URL: "file:///etc/passwd", SHA256: hex.EncodeToString(sum[:]),
	}, filepath.Join(t.TempDir(), "asset.tgz")); err == nil {
		t.Fatal("HTTPS 索引引用 file:// 资产应被拒绝")
	}
}

func TestDownloadChecksum(t *testing.T) {
	payload := []byte("plugin archive")
	sum := sha256.Sum256(payload)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/index.json", "stable", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "asset.tgz")
	digest, err := client.Download(context.Background(), Asset{URL: server.URL + "/asset", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest=%s", digest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}

	// 缺少 sha256：必须在下载前拒绝。
	before := requests
	destMissing := filepath.Join(dir, "missing.tgz")
	if _, err := client.Download(context.Background(), Asset{URL: server.URL + "/asset"}, destMissing); err == nil {
		t.Fatal("missing sha256 should fail")
	}
	if requests != before {
		t.Fatal("missing sha256 should be rejected before downloading")
	}
	if _, err := os.Stat(destMissing); !os.IsNotExist(err) {
		t.Fatal("missing sha256 must not create a destination file")
	}

	// 非法格式：同样在下载前拒绝。
	if _, err := client.Download(context.Background(), Asset{URL: server.URL + "/asset", SHA256: "bad"}, filepath.Join(dir, "bad.tgz")); err == nil {
		t.Fatal("malformed sha256 should fail")
	}

	// 摘要不匹配：下载后拒绝并清理临时文件。
	destMismatch := filepath.Join(dir, "mismatch.tgz")
	wrong := sha256.Sum256([]byte("other"))
	if _, err := client.Download(context.Background(), Asset{URL: server.URL + "/asset", SHA256: hex.EncodeToString(wrong[:]), Size: int64(len(payload))}, destMismatch); err == nil {
		t.Fatal("checksum mismatch should fail")
	}
	if _, err := os.Stat(destMismatch); !os.IsNotExist(err) {
		t.Fatal("mismatched download must be removed")
	}
}

func writeArchive(t *testing.T, path string, files map[string]string) {
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

func TestIndexValidateRequiresSHA256(t *testing.T) {
	valid := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	base := Index{Schema: 1, Protocol: "v1", Plugins: []Plugin{{
		Kind: "dns", ID: "cloudflare", Version: "1.0.0",
		Assets: map[string]Asset{"linux/amd64": {URL: "https://example.invalid/a.tgz", SHA256: valid, Size: 1}},
	}}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid index rejected: %v", err)
	}
	missing := base
	missing.Plugins = []Plugin{{
		Kind: "dns", ID: "cloudflare", Version: "1.0.0",
		Assets: map[string]Asset{"linux/amd64": {URL: "https://example.invalid/a.tgz"}},
	}}
	if err := missing.Validate(); err == nil {
		t.Fatal("index without sha256 should be rejected")
	}
	duplicate := base
	duplicate.Plugins = append(duplicate.Plugins, duplicate.Plugins[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate plugin should be rejected")
	}
}

func TestIndexValidateBundleProviders(t *testing.T) {
	valid := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	asset := Asset{URL: "https://example.invalid/a.tgz", SHA256: valid, Size: 1}
	index := Index{Schema: 1, Protocol: "v1", Plugins: []Plugin{{
		Kind: "dns", ID: "all", Version: "1.0.0", Bundle: true,
		Providers: []string{"cloudflare", "route53"},
		Assets:    map[string]Asset{"linux/amd64": asset},
	}}}
	if err := index.Validate(); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
	index.Plugins[0].Providers = nil
	if err := index.Validate(); err == nil {
		t.Fatal("bundle without providers should be rejected")
	}
	index.Plugins[0].Providers = []string{"cloudflare", "cloudflare"}
	if err := index.Validate(); err == nil {
		t.Fatal("bundle with duplicate providers should be rejected")
	}
}
