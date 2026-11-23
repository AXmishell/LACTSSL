package i18n

import "testing"

func TestParseAcceptLanguageWeights(t *testing.T) {
	if got := ParseAcceptLanguage("en-US;q=0, zh-CN;q=1"); got != ZhCN {
		t.Fatalf("q=0 language was selected: %q", got)
	}
	if got := ParseAcceptLanguage("en-US;q=0.5, zh-CN;q=0.9"); got != ZhCN {
		t.Fatalf("weighted order = %q", got)
	}
	if got := ParseAcceptLanguage("en-US;q=0.9, zh-CN;q=0.5"); got != EnUS {
		t.Fatalf("weighted order = %q", got)
	}
}

func TestReplaceParamsIsSinglePass(t *testing.T) {
	got := replaceParams("{a} {b}", map[string]any{"a": "{b}", "b": "value"}, nil)
	if got != "{b} value" {
		t.Fatalf("replaceParams = %q", got)
	}
}

func TestTranslationFallback(t *testing.T) {
	if len(Supported()) == 0 {
		t.Fatal("no locales loaded")
	}
	if got := T(EnUS, "missing.key", nil); got != "missing.key" {
		t.Fatalf("missing key = %q", got)
	}
}
