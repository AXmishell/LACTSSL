package i18n

import (
	"os"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]Locale{
		"zh-CN":          ZhCN,
		"zh-cn":          ZhCN,
		"zh":             ZhCN,
		"zh_Hans":        ZhCN,
		"zh-TW":          ZhTW,
		"zh-tw":          ZhTW,
		"zh-Hant":        ZhTW,
		"zh-HK":          ZhTW,
		"en-US":          EnUS,
		"en-GB":          EnUS,
		"en":             EnUS,
		"ru-RU":          RuRU,
		"ru":             RuRU,
		"ja-JP":          JaJP,
		"ja":             JaJP,
		"fr-FR":          Default,
		"":               Default,
		"not-a-language": Default,
	}
	for input, expected := range cases {
		if actual := Normalize(input); actual != expected {
			t.Fatalf("Normalize(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	cases := map[string]Locale{
		"en-US,en;q=0.9":          EnUS,
		"zh-CN,zh;q=0.9,en;q=0.8": ZhCN,
		"zh-TW,zh;q=0.9,en;q=0.8": ZhTW,
		"zh-HK,zh;q=0.9,en;q=0.8": ZhTW,
		"ru-RU,ru;q=0.9,en;q=0.8": RuRU,
		"ja-JP,ja;q=0.9,en;q=0.8": JaJP,
		"fr-FR,fr;q=0.9":          Default,
	}
	for input, expected := range cases {
		if actual := ParseAcceptLanguage(input); actual != expected {
			t.Fatalf("ParseAcceptLanguage(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestSupportedIncludesAddedLanguages(t *testing.T) {
	supported := map[Locale]bool{}
	for _, locale := range Supported() {
		supported[locale] = true
	}
	for _, locale := range []Locale{ZhTW, RuRU, JaJP} {
		if !supported[locale] {
			t.Errorf("%s is not registered", locale)
		}
	}
}

func TestTranslateAndRender(t *testing.T) {
	if got := T(EnUS, "api.error.invalid_params", nil); got != "Invalid request parameters" {
		t.Fatalf("unexpected translation: %q", got)
	}
	if got := T(EnUS, "api.error.field_required", map[string]any{"field": "Host"}); got != `Please fill in "Host"` {
		t.Fatalf("unexpected placeholder translation: %q", got)
	}
	encoded := Encode("monitor.message.expiring", map[string]any{"days": 7})
	if key, params, ok := Decode(encoded); !ok || key != "monitor.message.expiring" || params["days"] != float64(7) {
		t.Fatalf("unexpected decode result: %q %v %v", key, params, ok)
	}
	if got := Render(EnUS, encoded); got != "Certificate expires in 7 days" {
		t.Fatalf("unexpected render result: %q", got)
	}
	// 嵌套消息参数应按当前语言渲染，日志才能把稳定事件 ID 显示成可读文案。
	nested := Encode("log.security.notify_delivered", map[string]any{
		"kind":  Encode("email.security.title.credential.password.changed", nil),
		"count": 2,
	})
	if got := Render(EnUS, nested); got != "Security alert sent: Account password changed, 2 delivery targets" {
		t.Fatalf("unexpected nested render result: %q", got)
	}
	// 历史数据是普通文本，必须原样返回。
	if got := Render(EnUS, "历史日志"); got != "历史日志" {
		t.Fatalf("legacy text should be returned as-is, got %q", got)
	}
}

func TestCatalogsHaveSameKeys(t *testing.T) {
	load()
	base, ok := catalogs[Default]
	if !ok {
		t.Fatal("missing default catalog")
	}
	// 中英与繁体中文是默认校验范围；俄语、日语等其他语言默认跳过，可通过 I18N_TEST_ALL=1 做全量校验。
	all := os.Getenv("I18N_TEST_ALL") == "1"
	for locale, values := range catalogs {
		if locale == Default {
			continue
		}
		if !all && locale != EnUS && locale != ZhTW {
			continue
		}
		for key := range base {
			if _, exists := values[key]; !exists {
				t.Errorf("%s missing key %q", locale, key)
			}
		}
		for key := range values {
			if _, exists := base[key]; !exists {
				t.Errorf("%s has extra key %q", locale, key)
			}
		}
	}
}

func TestRenderLegacyMessages(t *testing.T) {
	cases := map[string]string{
		"系统设置已更新":                             "System settings updated",
		"后台调度器已启动":                            "Background scheduler started",
		"登录失败，来源 IP: 127.0.0.1":               "Login failed from IP: 127.0.0.1",
		"账户登录成功，来源 IP: 10.0.0.1":              "Signed in successfully from IP: 10.0.0.1",
		"证书将在 7 天后过期":                         "Certificate expires in 7 days",
		"监控「demo」(example.com): 证书有效，剩余 10 天": "Monitor \"demo\" (example.com): Certificate is valid; 10 days remaining",
		"已生成随机门禁入口: /5t7IxB4CkGJO3SrVcNpf/":   "Generated random secure entry: /5t7IxB4CkGJO3SrVcNpf/",
		"系统初始化完成，管理员账户: admin@qq.com":         "System initialized. Administrator account: admin@qq.com",
		"后台调度器已停止":                            "Background scheduler stopped",
	}
	for input, expected := range cases {
		if got := Render(EnUS, input); got != expected {
			t.Errorf("Render(%q) = %q, want %q", input, got, expected)
		}
	}
	// 无法匹配的自由文本保持原样。
	if got := Render(EnUS, "一段无法匹配的旧文本"); got != "一段无法匹配的旧文本" {
		t.Errorf("unmatched legacy text should stay unchanged, got %q", got)
	}
}
