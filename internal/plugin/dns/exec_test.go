package dnsplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginproto "lactssl/internal/plugin/proto"
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

// TestDiscoverRejectsMismatchedInstalledDigest 验证仓库安装记录中的摘要
// 不匹配时，helper 不会被执行 manifest，也不会注册到运行时。
func TestDiscoverRejectsMismatchedInstalledDigest(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
esac
`, testManifest)
	writeHelper(t, dir, "fake-helper", script)
	t.Cleanup(func() { UnregisterSource(SourceExec) })

	result := Discover(DiscoverConfig{
		Dir:              dir,
		EnabledProviders: []string{"fakedns"},
		ExpectedDigests:  map[string]string{"fake-helper": strings.Repeat("0", 64)},
	})
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], "安装记录不一致") {
		t.Fatalf("摘要不匹配应被拒绝: %+v", result)
	}
	if result.Providers != 0 {
		t.Fatalf("被篡改的 helper 不应注册服务商: %+v", result)
	}
}

// TestExecProviderDetectsHelperReplacement 验证扫描后替换 helper 文件时，
// present/cleanup 前会拒绝执行。
func TestExecProviderDetectsHelperReplacement(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
present) exit 0 ;;
esac
`, testManifest)
	path := writeHelper(t, dir, "fake-helper", script)
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	Discover(DiscoverConfig{Dir: dir, EnabledProviders: []string{"fakedns"}})
	plugin, ok := Lookup("fakedns")
	if !ok {
		t.Fatal("helper not discovered")
	}
	provider, err := plugin.New(nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("example.com", "token", "key-auth"); err == nil || !strings.Contains(err.Error(), "已变化") {
		t.Fatalf("替换后的 helper 应被拒绝: %v", err)
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

func TestDiscoverSkipsDisabledHelper(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	script := fmt.Sprintf(`#!/bin/sh
echo run >> %q
case "$1" in
manifest) printf '%%s' %q ;;
esac
`, marker, testManifest)
	writeHelper(t, dir, "fake-helper", script)

	result := Discover(DiscoverConfig{Dir: dir, Timeout: time.Second, DisabledHelpers: []string{"fake-helper"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if len(result.DisabledFiles) != 1 || result.DisabledFiles[0] != "fake-helper" {
		t.Fatalf("disabled files = %v", result.DisabledFiles)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("disabled helper should not be executed: %v", err)
	}
	if _, found := ExternalProviderInfo("fakedns"); found {
		t.Fatal("disabled helper should not register providers")
	}

	// 取消停用后 helper 才会被探测。
	result = Discover(DiscoverConfig{Dir: dir, Timeout: time.Second, EnabledProviders: []string{"fakedns"}})
	if len(result.DisabledFiles) != 0 || !dnspluginEnabled("fakedns") {
		t.Fatalf("re-enabled result = %+v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("enabled helper should be executed: %v", err)
	}
}

func dnspluginEnabled(id string) bool {
	info, found := ExternalProviderInfo(id)
	return found && info.Enabled
}

// TestVerifyReadOnlyCapability 验证只有声明 verify-v1 的 helper 才会收到
// verify 子进程调用，且结果按协议解析。
func TestVerifyReadOnlyCapability(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "verify.json")
	manifest := `{"protocol":"v1","capabilities":["verify-v1"],"name":"verify helper","version":"1.0.0","providers":[{"id":"verifydns","name":"Verify DNS","capabilities":["verify-v1"]}]}`
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
verify)
  cat > %q
  printf '{"ok":true,"checks":[{"key":"zone","status":"ok","message":"example.com"}]}'
  ;;
*)
  exit 2
  ;;
esac
`, manifest, output)
	writeHelper(t, dir, "verify-helper", script)

	result := Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledProviders: []string{"verifydns"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if result.Providers != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	plugin, ok := Lookup("verifydns")
	if !ok {
		t.Fatal("provider not registered")
	}
	verifier, ok := plugin.(Verifier)
	if !ok {
		t.Fatal("plugin should implement Verifier")
	}
	response, err := verifier.Verify(context.Background(), map[string]string{"api_token": "secret"}, "example.com", "read", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || len(response.Checks) != 1 || response.Checks[0].Status != pluginproto.VerifyStatusOK {
		t.Fatalf("unexpected response: %+v", response)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"verify"`, `"mode":"read"`, `"locale":"zh-CN"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("verify request missing %s: %s", want, data)
		}
	}
}

// TestVerifyUnsupportedWithoutCapability 验证旧 helper 不会被调用 verify。
func TestVerifyUnsupportedWithoutCapability(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
verify)
  echo unexpected >&2
  exit 3
  ;;
*)
  exit 2
  ;;
esac
`, testManifest)
	writeHelper(t, dir, "legacy-helper", script)
	Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledProviders: []string{"fakedns"}})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	plugin, _ := Lookup("fakedns")
	verifier, ok := plugin.(Verifier)
	if !ok {
		t.Fatal("plugin should implement Verifier")
	}
	if _, err := verifier.Verify(context.Background(), nil, "example.com", "read", "zh-CN"); !errors.Is(err, ErrVerifyUnsupported) {
		t.Fatalf("expected ErrVerifyUnsupported, got %v", err)
	}
}
