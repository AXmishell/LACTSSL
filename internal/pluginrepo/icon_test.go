package pluginrepo

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testIconData(t *testing.T, mime, content string) (string, string) {
	t.Helper()
	raw := []byte(content)
	return EncodeIconDataURI(mime, raw), IconSHA256(raw)
}

func TestSanitizeIcons(t *testing.T) {
	svg, svgSum := testIconData(t, "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	png := EncodeIconDataURI("image/png", []byte("png"))
	index := Index{Icons: []Icon{
		{Kind: "dns", ID: "cloudflare", MIME: "image/svg+xml", SHA256: svgSum, Data: svg, Aliases: []string{"cf", "cf", "bad id", "cloudflare"}},
		{Kind: "dns", ID: "cloudflare", MIME: "image/svg+xml", SHA256: svgSum, Data: svg},
		{Kind: "deploy", ID: "baota", MIME: "image/png", SHA256: "deadbeef", Data: png},
		{Kind: "bogus", ID: "x", MIME: "image/png", SHA256: "whatever", Data: png},
		{Kind: "dns", ID: "bad id", MIME: "image/svg+xml", SHA256: svgSum, Data: svg},
		{Kind: "dns", ID: "script", MIME: "text/html", SHA256: svgSum, Data: svg},
	}}
	index.SanitizeIcons()
	if len(index.Icons) != 1 {
		t.Fatalf("expected one icon after sanitize, got %+v", index.Icons)
	}
	icon := index.Icons[0]
	if icon.ID != "cloudflare" || icon.Kind != "dns" {
		t.Fatalf("unexpected icon: %+v", icon)
	}
	if len(icon.Aliases) != 1 || icon.Aliases[0] != "cf" {
		t.Fatalf("unexpected aliases: %v", icon.Aliases)
	}
	if icon.SHA256 != svgSum {
		t.Fatalf("sha256 = %s", icon.SHA256)
	}
	icons := index.IconMap()
	if icons["dns"]["cloudflare"] == "" || icons["dns"]["cf"] == "" {
		t.Fatalf("alias not expanded: %+v", icons)
	}
}

func TestDecodeIconDataURI(t *testing.T) {
	data, _ := testIconData(t, "image/png", "payload")
	raw, err := DecodeIconDataURI(data, "image/png")
	if err != nil || string(raw) != "payload" {
		t.Fatalf("decode = %q, %v", raw, err)
	}
	if _, err := DecodeIconDataURI(data, "image/svg+xml"); err == nil {
		t.Fatal("mismatched mime should fail")
	}
	if _, err := DecodeIconDataURI("data:image/png,plain", "image/png"); err == nil {
		t.Fatal("non-base64 data URI should fail")
	}
	if _, err := DecodeIconDataURI("data:image/png;base64,"+strings.Repeat("A", MaxIconBytes*2), "image/png"); err == nil {
		t.Fatal("oversized icon should fail")
	}
}

func TestExtractPackageWithIcons(t *testing.T) {
	dir := t.TempDir()
	icon := `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
	iconSum := IconSHA256([]byte(icon))
	pluginJSON := `{"schema":1,"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","os":"linux","arch":"amd64","entrypoint":"lactssl-dns-cloudflare","icons":{"cloudflare":{"file":"icons/cloudflare.svg","sha256":"` + iconSum + `"}}}`
	archive := filepath.Join(dir, "plugin.tar.gz")
	writeArchive(t, archive, map[string]string{
		"plugin.json":            pluginJSON,
		"lactssl-dns-cloudflare": "binary",
		"icons/cloudflare.svg":   icon,
	})
	dest := filepath.Join(dir, "extract")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	meta, _, err := ExtractPackage(archive, dest)
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := meta.Icons["cloudflare"]; !ok || ref.SHA256 != iconSum {
		t.Fatalf("unexpected icons: %+v", meta.Icons)
	}
	if _, err := os.Stat(filepath.Join(dest, "icons", "cloudflare.svg")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPackageRejectsBadIcons(t *testing.T) {
	icon := []byte("<svg></svg>")
	pluginJSON := `{"schema":1,"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","os":"linux","arch":"amd64","entrypoint":"lactssl-dns-cloudflare","icons":{"cloudflare":{"file":"icons/cloudflare.svg","sha256":"deadbeef"}}}`
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"digest", map[string]string{"plugin.json": pluginJSON, "lactssl-dns-cloudflare": "bin", "icons/cloudflare.svg": string(icon)}},
		{"undeclared", map[string]string{"plugin.json": `{"schema":1,"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","os":"linux","arch":"amd64","entrypoint":"lactssl-dns-cloudflare"}`, "lactssl-dns-cloudflare": "bin", "icons/cloudflare.svg": string(icon)}},
		{"missing", map[string]string{"plugin.json": pluginJSON, "lactssl-dns-cloudflare": "bin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "plugin.tar.gz")
			writeArchive(t, archive, tc.files)
			if _, _, err := ExtractPackage(archive, dir); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIconDataURIRoundTrip(t *testing.T) {
	raw := []byte("abc")
	uri := EncodeIconDataURI("image/webp", raw)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/webp;base64,"))
	if err != nil || string(decoded) != "abc" {
		t.Fatalf("round trip = %q, %v", decoded, err)
	}
}
