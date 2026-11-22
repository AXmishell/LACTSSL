package pluginrepo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func validPackageSeed() []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	meta := []byte(`{"schema":1,"kind":"dns","id":"demo","version":"1.0.0","protocol":"v1","os":"linux","arch":"amd64","entrypoint":"demo"}`)
	_ = tw.WriteHeader(&tar.Header{Name: "plugin.json", Mode: 0o644, Size: int64(len(meta)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(meta)
	_ = tw.WriteHeader(&tar.Header{Name: "demo", Mode: 0o755, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("bin\n"))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func FuzzExtractPackage(f *testing.F) {
	f.Add(validPackageSeed())
	f.Add([]byte("not a gzip"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		archive := filepath.Join(dir, "pkg.tar.gz")
		if err := os.WriteFile(archive, data, 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "out")
		_ = os.MkdirAll(dest, 0o700)
		_, _, _ = ExtractPackage(archive, dest)
		// 任何写出的文件都必须位于 dest 内。
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if path == archive {
				return nil
			}
			rel, relErr := filepath.Rel(dest, path)
			if relErr != nil || rel == ".." || len(rel) >= 3 && rel[:3] == ".." {
				t.Fatalf("extracted outside dest: %s", path)
			}
			return nil
		})
	})
}

func FuzzParseIndex(f *testing.F) {
	f.Add([]byte(`{"schema":1,"channel":"stable","protocol":"v1","plugins":[{"kind":"dns","id":"demo","version":"1.0.0","protocol":"v1","assets":{"linux/amd64":{"url":"https://example.invalid/a.tgz","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":1}}}]}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = ParseIndex(raw, ParseOptions{
			Channel:  "stable",
			IndexURL: "https://example.invalid/index.json",
		})
	})
}
