package dnsplugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testManifest = `{"protocol":"v1","name":"test helper","version":"1.0.0","providers":[{"id":"fakedns","name":"Fake DNS","description":"test provider","aliases":["fake"],"fields":[{"key":"api_token","label":"API Token","type":"password","required":true}]}]}`

const multiManifest = `{"protocol":"v1","name":"multi helper","version":"1.0.0","providers":[{"id":"fakedns","name":"Fake DNS"},{"id":"otherdns","name":"Other DNS","aliases":["other"]}]}`

func writeHelper(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return path
}

func TestDiscoverAndExecProvider(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "request.json")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
present|cleanup)
  cat > %q
  ;;
*)
  exit 2
  ;;
esac
`, testManifest, output)
	writeHelper(t, dir, "fake-helper", script)

	result := Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledProviders: []string{"fakedns"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if result.Helpers != 1 || result.Providers != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	plugin, ok := Lookup("fake")
	if !ok || plugin.ID() != "fakedns" || plugin.Source() != SourceExec {
		t.Fatalf("lookup failed: %v %v", plugin, ok)
	}
	provider, err := plugin.New(map[string]string{"api_token": "secret-token"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := provider.Present("example.com", "token", "key-auth"); err != nil {
		t.Fatalf("present: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	for _, want := range []string{`"provider":"fakedns"`, `"api_token":"secret-token"`, `"domain":"example.com"`, `"key_auth":"key-auth"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("request missing %s: %s", want, data)
		}
	}
}

func TestExecProviderRedactsAndTimesOut(t *testing.T) {
	dir := t.TempDir()
	failScript := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
present) echo "token secret-token failed" >&2; exit 1 ;;
esac
`, testManifest)
	writeHelper(t, dir, "fail-helper", failScript)

	Discover(DiscoverConfig{Dir: dir, Timeout: 500 * time.Millisecond, EnabledProviders: []string{"fakedns"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	plugin, ok := Lookup("fakedns")
	if !ok {
		t.Fatal("helper not discovered")
	}
	provider, err := plugin.New(map[string]string{"api_token": "secret-token"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	err = provider.Present("example.com", "token", "key-auth")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("secret leaked in error: %v", err)
	}

	slowDir := t.TempDir()
	slowScript := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
present) sleep 3 ;;
esac
`, testManifest)
	writeHelper(t, slowDir, "slow-helper", slowScript)
	Discover(DiscoverConfig{Dir: slowDir, Timeout: 300 * time.Millisecond, EnabledProviders: []string{"fakedns"}})
	plugin, ok = Lookup("fakedns")
	if !ok {
		t.Fatal("slow helper not discovered")
	}
	provider, err = plugin.New(nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	start := time.Now()
	err = provider.Present("example.com", "token", "key-auth")
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout did not interrupt helper: %s", elapsed)
	}
}

func TestDiscoverProvidersDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
esac
`, multiManifest)
	writeHelper(t, dir, "multi-helper", script)
	t.Cleanup(func() { UnregisterSource(SourceExec) })

	// 未提供启用白名单：插件服务商默认全部停用，但元数据仍会保留。
	result := Discover(DiscoverConfig{Dir: dir})
	if result.Helpers != 1 || result.Providers != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Disabled) != 2 {
		t.Fatalf("disabled = %v", result.Disabled)
	}
	if Has("fakedns") || Has("otherdns") || Has("other") {
		t.Fatal("plugin providers should be disabled by default")
	}
	for _, id := range []string{"fakedns", "otherdns"} {
		meta, ok := ExternalProviderInfo(id)
		if !ok || meta.Enabled {
			t.Fatalf("disabled metadata for %s = %+v, %v", id, meta, ok)
		}
	}

	// 只启用一个服务商。
	result = Discover(DiscoverConfig{Dir: dir, EnabledProviders: []string{"fakedns"}})
	if result.Providers != 1 || len(result.Disabled) != 1 || result.Disabled[0] != "otherdns" {
		t.Fatalf("partial enable result: %+v", result)
	}
	if !Has("fakedns") {
		t.Fatal("enabled provider should be registered")
	}
	if Has("otherdns") || Has("other") {
		t.Fatal("disabled provider should not be registered")
	}
	if enabled := ExternalEnabledProviders(); len(enabled) != 1 || enabled[0].ID != "fakedns" {
		t.Fatalf("enabled providers = %+v", enabled)
	}

	// 全部启用。
	result = Discover(DiscoverConfig{Dir: dir, EnabledProviders: []string{"fakedns", "otherdns"}})
	if result.Providers != 2 || len(result.Disabled) != 0 {
		t.Fatalf("full enable result: %+v", result)
	}
	if !Has("otherdns") || !Has("other") {
		t.Fatal("re-enabled provider should be registered")
	}
}

// TestDiscoverLargeManifest 验证 manifest 不受 max_output_kb（默认 64 KB）截断。
func TestDiscoverLargeManifest(t *testing.T) {
	dir := t.TempDir()
	padding := strings.Repeat("x", 70*1024)
	manifest := fmt.Sprintf(`{"protocol":"v1","name":"big helper","junk":"%s","providers":[{"id":"bigdns","name":"Big DNS"}]}`, padding)
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
esac
`, manifest)
	writeHelper(t, dir, "big-helper", script)
	t.Cleanup(func() { UnregisterSource(SourceExec) })

	result := Discover(DiscoverConfig{Dir: dir, EnabledProviders: []string{"bigdns"}})
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Helpers != 1 || result.Providers != 1 || !Has("bigdns") {
		t.Fatalf("large manifest not discovered: %+v", result)
	}
}

func TestExecProviderMemoryLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("memory watchdog test requires /proc")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
present) exec python3 -c 'import time
b = bytearray(32 * 1024 * 1024)
time.sleep(5)' ;;
esac
`, testManifest)
	writeHelper(t, dir, "memory-helper", script)
	Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 8, Timeout: 5 * time.Second, EnabledProviders: []string{"fakedns"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	plugin, ok := Lookup("fakedns")
	if !ok {
		t.Fatal("helper not discovered")
	}
	provider, err := plugin.New(nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	start := time.Now()
	err = provider.Present("example.com", "token", "key-auth")
	if err == nil || !strings.Contains(err.Error(), "内存占用超过上限") {
		t.Fatalf("expected memory limit error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("memory watchdog did not stop helper: %s", elapsed)
	}
}
