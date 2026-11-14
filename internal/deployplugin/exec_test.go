package deployplugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testManifest = `{"protocol":"v1","name":"test deploy helper","version":"1.0.0","deployers":[{"id":"fakepanel","name":"Fake Panel","description":"test target","aliases":["fakep"],"fields":[{"key":"api_token","label":"API Token","type":"password","secret":true,"required":true}],"deploy_fields":[{"key":"sites","label":"Sites","type":"text","required":true}]}]}`

func writeHelper(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return path
}

func resetExternalState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		UnregisterSource(SourceExec)
		externalState.Lock()
		externalState.deployers = make(map[string]ExternalDeployer)
		externalState.helpers = 0
		externalState.Unlock()
	})
}

func TestDiscoverAndDeploy(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "request.json")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
deploy)
  cat > %q
  printf '{"ok":true,"message":"deployed"}'
  ;;
*)
  exit 2
  ;;
esac
`, testManifest, output)
	writeHelper(t, dir, "fake-helper", script, 0o700)

	result := Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledDeployers: []string{"fakepanel"}})
	if result.Helpers != 1 || result.Deployers != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	plugin, ok := Lookup("fakep")
	if !ok || plugin.ID() != "fakepanel" || plugin.Source() != SourceExec {
		t.Fatalf("lookup failed: %v %v", plugin, ok)
	}
	req := Request{
		Auth:   map[string]string{"api_token": "secret-token"},
		Config: map[string]string{"sites": "a.example.com"},
		Certificate: Certificate{
			Domain:       "example.com",
			Domains:      []string{"example.com", "www.example.com"},
			CertPEM:      "CERT",
			FullchainPEM: "FULLCHAIN",
			KeyPEM:       "-----BEGIN PRIVATE KEY-----\nsecret-key-material\n-----END PRIVATE KEY-----\n",
		},
	}
	if err := plugin.Deploy(context.Background(), req); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	for _, want := range []string{
		`"provider":"fakepanel"`,
		`"action":"deploy"`,
		`"api_token":"secret-token"`,
		`"sites":"a.example.com"`,
		`"domain":"example.com"`,
		`"key_pem":"-----BEGIN PRIVATE KEY-----\nsecret-key-material`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("request missing %s: %s", want, data)
		}
	}
}

func TestDeployRedactsAndTimesOut(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	failScript := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
deploy) echo "token secret-token failed" >&2; exit 1 ;;
esac
`, testManifest)
	writeHelper(t, dir, "fail-helper", failScript, 0o700)

	Discover(DiscoverConfig{Dir: dir, Timeout: 500 * time.Millisecond, EnabledDeployers: []string{"fakepanel"}})
	plugin, ok := Lookup("fakepanel")
	if !ok {
		t.Fatal("helper not discovered")
	}
	err := plugin.Deploy(context.Background(), Request{
		Auth:        map[string]string{"api_token": "secret-token"},
		Certificate: Certificate{KeyPEM: "-----BEGIN PRIVATE KEY-----\nsecret-key-material\n-----END PRIVATE KEY-----\n"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "secret-key-material") {
		t.Fatalf("secret leaked in error: %v", err)
	}

	slowDir := t.TempDir()
	slowScript := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
deploy) sleep 3 ;;
esac
`, testManifest)
	writeHelper(t, slowDir, "slow-helper", slowScript, 0o700)
	Discover(DiscoverConfig{Dir: slowDir, Timeout: 300 * time.Millisecond, EnabledDeployers: []string{"fakepanel"}})
	plugin, ok = Lookup("fakepanel")
	if !ok {
		t.Fatal("slow helper not discovered")
	}
	start := time.Now()
	err = plugin.Deploy(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout did not interrupt helper: %s", elapsed)
	}
}

func TestDeployMemoryLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("memory watchdog test requires /proc")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 not available")
	}
	resetExternalState(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
deploy) exec python3 -c 'import time
b = bytearray(32 * 1024 * 1024)
time.sleep(5)' ;;
esac
`, testManifest)
	writeHelper(t, dir, "memory-helper", script, 0o700)
	Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 8, Timeout: 5 * time.Second, EnabledDeployers: []string{"fakepanel"}})
	plugin, ok := Lookup("fakepanel")
	if !ok {
		t.Fatal("helper not discovered")
	}
	start := time.Now()
	err := plugin.Deploy(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "内存占用超过上限") {
		t.Fatalf("expected memory limit error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("memory watchdog did not stop helper: %s", elapsed)
	}
}

func TestDiscoverKeepsDisabledDeployers(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
esac
`, testManifest)
	writeHelper(t, dir, "fake-helper", script, 0o700)

	result := Discover(DiscoverConfig{Dir: dir})
	if result.Deployers != 0 || len(result.Disabled) != 1 || result.Disabled[0] != "fakepanel" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Helpers != 1 {
		t.Fatalf("helper should still be discovered: %+v", result)
	}
	if _, ok := Lookup("fakepanel"); ok {
		t.Fatal("disabled deployer should not be registered")
	}
	info, ok := ExternalDeployerInfo("fakepanel")
	if !ok || info.Enabled {
		t.Fatalf("disabled metadata = %+v, %v", info, ok)
	}

	// 启用后重新扫描，目标注册到运行时。
	result = Discover(DiscoverConfig{Dir: dir, EnabledDeployers: []string{"fakepanel"}})
	if result.Deployers != 1 || len(result.Disabled) != 0 {
		t.Fatalf("unexpected enabled result: %+v", result)
	}
	if _, ok := Lookup("fakepanel"); !ok {
		t.Fatal("enabled deployer should be registered")
	}
	info, ok = ExternalDeployerInfo("fakepanel")
	if !ok || !info.Enabled {
		t.Fatalf("enabled metadata = %+v, %v", info, ok)
	}
}

func TestDiscoverSkipsBuiltinAndWritableFiles(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
esac
`, testManifest)
	writeHelper(t, dir, "fake-helper", script, 0o700)
	// 组或其他用户可写的 helper 会被拒绝。WriteFile 受 umask 影响，显式 chmod。
	unsafe := writeHelper(t, dir, "unsafe-helper", script, 0o700)
	if err := os.Chmod(unsafe, 0o777); err != nil {
		t.Fatalf("chmod unsafe helper: %v", err)
	}

	result := Discover(DiscoverConfig{
		Dir:      dir,
		KnownIDs: map[string]bool{"fakepanel": true},
	})
	if result.Deployers != 0 {
		t.Fatalf("expected no deployers, got %+v", result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "fakepanel" {
		t.Fatalf("expected skipped builtin id, got %+v", result.Skipped)
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected unsafe file error")
	}
}

func TestDeployRejectsReplacedHelper(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest) printf '%%s' %q ;;
deploy) exit 0 ;;
esac
`, testManifest)
	path := writeHelper(t, dir, "fake-helper", script, 0o700)

	Discover(DiscoverConfig{Dir: dir, Timeout: time.Second, EnabledDeployers: []string{"fakepanel"}})
	plugin, ok := Lookup("fakepanel")
	if !ok {
		t.Fatal("helper not discovered")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("replace helper: %v", err)
	}
	err := plugin.Deploy(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "已变化") {
		t.Fatalf("expected digest change error, got %v", err)
	}
}

func TestValidateManifest(t *testing.T) {
	base := Manifest{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "ok"}}}
	if err := validateManifest(base); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	cases := []Manifest{
		{Protocol: "v2", Deployers: []ManifestDeployer{{ID: "ok"}}},
		{Protocol: "v1"},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "Bad ID"}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Aliases: []string{"b"}}, {ID: "b"}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Fields: []ManifestField{{Key: "x", Type: "text"}}, DeployFields: []ManifestField{{Key: "x", Type: "text"}}}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Fields: []ManifestField{{Key: "x", Type: "unknown"}}}}},
	}
	for index, manifest := range cases {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("case %d: expected error", index)
		}
	}
}
