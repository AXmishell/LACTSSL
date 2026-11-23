//go:build !embed

package i18n

import (
	"embed"
	"io/fs"
	"strings"
)

// 开发构建直接嵌入原始 JSON，便于修改语言文件后无需额外生成步骤。
//
//go:embed locales/*.json
var localeFS embed.FS

// readLocaleFiles 返回 <locale>.json -> 原始内容。
func readLocaleFiles() (map[string][]byte, error) {
	entries, err := fs.ReadDir(localeFS, "locales")
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := localeFS.ReadFile("locales/" + entry.Name())
		if err != nil {
			continue
		}
		files[entry.Name()] = raw
	}
	return files, nil
}
