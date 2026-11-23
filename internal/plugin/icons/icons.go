// Package icons 提供插件图标的 MIME、大小限制与 data URI 编码。
package icons

import (
	"encoding/base64"
	"path/filepath"
	"strings"
)

// MaxBytes 是单张图标的解码后大小上限。
const MaxBytes = 64 << 10

var mimes = map[string]string{
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".webp": "image/webp",
}

// MIME 返回扩展名对应的白名单 MIME，空字符串表示不支持。
func MIME(ext string) string {
	return mimes[strings.ToLower(ext)]
}

// Ext 返回 MIME 对应的文件扩展名，空字符串表示不支持。
func Ext(mime string) string {
	for ext, known := range mimes {
		if known == mime {
			return ext
		}
	}
	return ""
}

// EncodeDataURI 把图标字节编码为 data URI。
func EncodeDataURI(mime string, raw []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// ParsePath 解析包内图标路径 icons/<id><ext>。
func ParsePath(name string) (id, ext string, ok bool) {
	if !strings.HasPrefix(name, "icons/") || strings.Contains(strings.TrimPrefix(name, "icons/"), "/") {
		return "", "", false
	}
	ext = strings.ToLower(filepath.Ext(name))
	if MIME(ext) == "" {
		return "", "", false
	}
	id = strings.TrimSuffix(strings.TrimPrefix(name, "icons/"), filepath.Ext(name))
	if id == "" {
		return "", "", false
	}
	return id, ext, true
}
