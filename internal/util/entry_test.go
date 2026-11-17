package util

import (
	"strings"
	"testing"
)

func TestValidateEntry(t *testing.T) {
	valid := []struct {
		in   string
		want string
	}{
		{"kp-abc123", "kp-abc123"},
		{"  /kp-abc123/  ", "kp-abc123"},
		{"A_b-9xyz", "A_b-9xyz"},
	}
	for _, tc := range valid {
		got, err := ValidateEntry(tc.in)
		if err != nil {
			t.Fatalf("ValidateEntry(%q) 返回错误: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ValidateEntry(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	invalid := []string{"", "ab", "short12", "bad name", "bad/name", "含中文", "assets", "ASSETS", "api", "login", "install", "reset", "acme", "pki"}
	for _, in := range invalid {
		if _, err := ValidateEntry(in); err == nil {
			t.Fatalf("ValidateEntry(%q) 应当返回错误", in)
		}
	}
}

func TestGenerateEntry(t *testing.T) {
	entry, err := GenerateEntry()
	if err != nil {
		t.Fatalf("GenerateEntry 返回错误: %v", err)
	}
	if len(entry) != 20 {
		t.Fatalf("GenerateEntry 长度 = %d, want 20", len(entry))
	}
	for _, r := range entry {
		if !strings.ContainsRune(entryAlphabet, r) {
			t.Fatalf("GenerateEntry 包含非法字符 %q: %q", r, entry)
		}
	}
	if validated, err := ValidateEntry(entry); err != nil || validated != entry {
		t.Fatalf("生成的入口未通过校验: %q, err=%v", entry, err)
	}
}
