// Package meta 提供 lego v5 全量 provider 的元数据查询。
//
// 现有 catalog 只覆盖历史 194 个 provider；v5 新增的 provider
// 由 meta_gen.go 补充。宿主安装 v5 helper 后，manifest 中的字段
// 与文案来自这里。
package meta

import (
	"strings"

	"lactssl/providers/catalog"
)

// LookupDNS 按规范 ID 查找 provider 元数据，先查历史 catalog，再查 v5 补充项。
func LookupDNS(id string) (catalog.Provider, bool) {
	if def, ok := catalog.LookupDNS(id); ok {
		return withLegacyHint(def), true
	}
	id = normalize(id)
	for _, def := range extraProviders {
		if normalize(def.Value) == id {
			return def, true
		}
	}
	return catalog.Provider{}, false
}

// IDs 返回 lego v5 全量 provider 的规范 ID，已排序。
func IDs() []string {
	out := make([]string, len(v5ProviderIDs))
	copy(out, v5ProviderIDs)
	return out
}

// Providers 返回 catalog 未覆盖的 v5 补充元数据。
func Providers() []catalog.Provider {
	out := make([]catalog.Provider, len(extraProviders))
	copy(out, extraProviders)
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
