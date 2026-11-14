package pluginexec

import (
	"strings"
	"testing"
)

func TestParseVmRSS(t *testing.T) {
	data := []byte("Name:\thelper\nVmRSS:\t   12345 kB\nThreads:\t3\n")
	rss, ok := parseVmRSS(data)
	if !ok || rss != 12345 {
		t.Fatalf("parseVmRSS = %d, %v", rss, ok)
	}
	if _, ok := parseVmRSS([]byte("Name:\thelper\n")); ok {
		t.Fatal("expected missing VmRSS")
	}
}

func TestRedact(t *testing.T) {
	secrets := map[string]string{
		"token": "super-secret-token",
		"key":   "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n",
		"short": "abc",
	}
	message := "failed with super-secret-token and -----BEGIN PRIVATE KEY----- abc"
	got := Redact(message, secrets)
	if got == message {
		t.Fatal("expected redaction")
	}
	// 过短的值不参与替换，避免误伤普通文本。
	if !strings.Contains(got, "abc") {
		t.Fatalf("short secret should be kept: %s", got)
	}
	if strings.Contains(got, "super-secret-token") {
		t.Fatalf("token leaked: %s", got)
	}
}
