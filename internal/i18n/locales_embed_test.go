//go:build embed

package i18n

import "testing"

// TestEmbeddedCatalogs 校验构建期生成的 gzip 语言包完整可用。
// 该测试仅在 embed 构建下运行，CI 会先执行 scripts/bundle-i18n.go。
func TestEmbeddedCatalogs(t *testing.T) {
	want := []Locale{ZhCN, ZhTW, EnUS, JaJP, RuRU}
	for _, locale := range want {
		if !Has(locale) {
			t.Fatalf("嵌入语言包缺少 %s", locale)
		}
		if got := T(locale, "api.error.page_not_found", nil); got == "" || got == "api.error.page_not_found" {
			t.Fatalf("%s 缺少 api.error.page_not_found 翻译: %q", locale, got)
		}
	}
	if got := len(Supported()); got < len(want) {
		t.Fatalf("Supported() 只返回 %d 种语言，期望至少 %d 种", got, len(want))
	}
}
