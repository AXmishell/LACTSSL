//go:build embed

package i18n

import (
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"strings"
)

// 发布构建嵌入构建期生成的 gzip 语言包（scripts/bundle-i18n.go）。
// 语言包为纯文本 JSON，压缩后体积约降至 22%。
//
//go:embed all:dist
var localeFS embed.FS

// readLocaleFiles 返回 <locale>.json -> 解压后的原始内容。
func readLocaleFiles() (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(localeFS, "dist", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			return nil
		}
		file, err := localeFS.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		zr, err := gzip.NewReader(file)
		if err != nil {
			// 单个语言包损坏时跳过，不影响其他语言。
			return nil
		}
		defer zr.Close()
		raw, err := io.ReadAll(zr)
		if err != nil {
			return nil
		}
		files[strings.TrimSuffix(entry.Name(), ".gz")] = raw
		return nil
	})
	return files, err
}
