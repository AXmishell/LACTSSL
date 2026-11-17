package pluginrepo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// MaxIconBytes 是单张图标的解码后大小上限。
const MaxIconBytes = 64 << 10

// Icon 是仓库索引内联的一张服务商图标。
// Data 是完整的 data URI，前端可直接用作 img src。
type Icon struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	MIME    string   `json:"mime"`
	SHA256  string   `json:"sha256"`
	Data    string   `json:"data"`
	Aliases []string `json:"aliases,omitempty"`
}

// IconFile 是插件包内 plugin.json 声明的图标文件。
type IconFile struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

var iconMIMEs = map[string]string{
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".webp": "image/webp",
}

// IconMIME 返回扩展名对应的白名单 MIME，空字符串表示不支持。
func IconMIME(ext string) string {
	return iconMIMEs[strings.ToLower(ext)]
}

// IconExt 返回 MIME 对应的文件扩展名，空字符串表示不支持。
func IconExt(mime string) string {
	for ext, known := range iconMIMEs {
		if known == mime {
			return ext
		}
	}
	return ""
}

// DecodeIconDataURI 解析并校验 data URI，返回解码后的字节。
func DecodeIconDataURI(data, mime string) ([]byte, error) {
	const prefix = "data:"
	if !strings.HasPrefix(data, prefix) {
		return nil, errors.New("图标不是 data URI")
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(data, prefix), ",")
	if !found {
		return nil, errors.New("图标 data URI 缺少负载")
	}
	parts := strings.Split(header, ";")
	if len(parts) < 2 || parts[0] != mime || !containsString(parts[1:], "base64") {
		return nil, errors.New("图标 data URI 头部不合法")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("图标 base64 解码失败: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxIconBytes {
		return nil, fmt.Errorf("图标大小超出限制（最大 %d 字节）", MaxIconBytes)
	}
	return raw, nil
}

// EncodeIconDataURI 把图标字节编码为 data URI。
func EncodeIconDataURI(mime string, raw []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// IconSHA256 返回图标字节的十六进制摘要。
func IconSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SanitizeIcons 丢弃非法、重复或损坏的图标，并统一 data URI 与别名。
// 索引签名仍然覆盖原始条目；这里只影响宿主实际使用的内容，避免
// 第三方仓库的一张坏图导致整份索引不可用。
func (i *Index) SanitizeIcons() {
	if len(i.Icons) == 0 {
		return
	}
	seen := make(map[string]bool, len(i.Icons))
	out := make([]Icon, 0, len(i.Icons))
	for _, icon := range i.Icons {
		kind := strings.ToLower(strings.TrimSpace(icon.Kind))
		id := strings.TrimSpace(icon.ID)
		if (kind != "dns" && kind != "deploy" && kind != "notify") || !idPattern.MatchString(id) {
			continue
		}
		mime := strings.ToLower(strings.TrimSpace(icon.MIME))
		if IconExt(mime) == "" {
			continue
		}
		raw, err := DecodeIconDataURI(icon.Data, mime)
		if err != nil {
			continue
		}
		sum := IconSHA256(raw)
		if icon.SHA256 != "" && !strings.EqualFold(icon.SHA256, sum) {
			continue
		}
		key := kind + "/" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		aliases := make([]string, 0, len(icon.Aliases))
		aliasSeen := map[string]bool{id: true}
		for _, alias := range icon.Aliases {
			alias = strings.TrimSpace(alias)
			if !idPattern.MatchString(alias) || aliasSeen[alias] {
				continue
			}
			aliasSeen[alias] = true
			aliases = append(aliases, alias)
		}
		out = append(out, Icon{
			Kind: kind, ID: id, MIME: mime, SHA256: sum,
			Data: EncodeIconDataURI(mime, raw), Aliases: aliases,
		})
	}
	i.Icons = out
}

// IconMap 把索引图标展开为 kind -> provider -> data URI，包含别名。
func (i Index) IconMap() map[string]map[string]string {
	out := make(map[string]map[string]string, 3)
	for _, icon := range i.Icons {
		byID := out[icon.Kind]
		if byID == nil {
			byID = map[string]string{}
			out[icon.Kind] = byID
		}
		byID[icon.ID] = icon.Data
		for _, alias := range icon.Aliases {
			byID[alias] = icon.Data
		}
	}
	return out
}

// ParseIconPath 解析包内图标路径 icons/<id><ext>。
func ParseIconPath(name string) (id, ext string, ok bool) {
	if !strings.HasPrefix(name, "icons/") || strings.Contains(strings.TrimPrefix(name, "icons/"), "/") {
		return "", "", false
	}
	ext = strings.ToLower(filepath.Ext(name))
	if IconMIME(ext) == "" {
		return "", "", false
	}
	id = strings.TrimSuffix(strings.TrimPrefix(name, "icons/"), filepath.Ext(name))
	if !idPattern.MatchString(id) {
		return "", "", false
	}
	return id, ext, true
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
