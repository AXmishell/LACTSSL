package util

import "testing"

func TestNormalizeDomains(t *testing.T) {
	got := NormalizeDomains([]string{"https://Example.com/path, www.example.com", "example.com", " *.api.example.com ", "bad_domain"})
	want := []string{"example.com", "www.example.com", "*.api.example.com", "bad_domain"}
	if len(got) != len(want) {
		t.Fatalf("域名数量不一致: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项不一致: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestValidDomain(t *testing.T) {
	cases := map[string]bool{
		"example.com":      true,
		"*.example.com":    true,
		"a.b.example.com":  true,
		"bad_domain":       false,
		"-bad.example.com": false,
		"example":          false,
		"example..com":     false,
	}
	for domain, want := range cases {
		if got := ValidDomain(domain); got != want {
			t.Errorf("ValidDomain(%q)=%v, want %v", domain, got, want)
		}
	}
}

func TestCipher(t *testing.T) {
	c, err := NewCipher([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.Encrypt("sensitive-config")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "sensitive-config" {
		t.Fatal("敏感内容未被加密")
	}
	plain, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "sensitive-config" {
		t.Fatalf("解密结果不一致: %s", plain)
	}
}
