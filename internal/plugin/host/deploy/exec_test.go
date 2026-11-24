package deployplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginproto "lactssl/internal/plugin/proto"
)

const testManifest = `{"protocol":"v1","name":"test deploy helper","version":"1.0.0","deployers":[{"id":"fakepanel","name":"Fake Panel","name_i18n":{"zh-CN":"模拟面板","en-US":"Fake Panel"},"description":"test target","description_i18n":{"zh-CN":"测试目标","en-US":"test target"},"aliases":["fakep"],"fields":[{"key":"api_token","label":"API Token","label_i18n":{"zh-CN":"API 令牌","en-US":"API Token"},"type":"password","secret":true,"required":true}],"deploy_fields":[{"key":"sites","label":"Sites","label_i18n":{"zh-CN":"站点","en-US":"Sites"},"type":"text","required":true}]}]}`

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
	info, ok := ExternalDeployerInfo("fakepanel")
	if !ok || info.NameI18n["en-US"] != "Fake Panel" || info.NameI18n["zh-CN"] != "模拟面板" {
		t.Fatalf("external deployer translations not kept: %+v", info)
	}
	if info.Fields[0].LabelI18n["zh-CN"] != "API 令牌" {
		t.Fatalf("field translations not kept: %+v", info.Fields[0])
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

func TestListOptions(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "options.json")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
options)
  cat > %q
  printf '{"options":[{"label":" Site A ","value":"a.example.com"},{"label":"Site A again","value":"a.example.com"},{"label":"","value":""},{"label":"Site B","value":"b.example.com"}]}'
  ;;
*)
  exit 2
  ;;
esac
`, testManifest, output)
	writeHelper(t, dir, "fake-helper", script, 0o700)
	Discover(DiscoverConfig{Dir: dir, EnabledDeployers: []string{"fakepanel"}})

	options, err := ListOptions(context.Background(), "fakepanel", OptionsRequest{
		Field:  "sites",
		Source: "baota_sites",
		Query:  "example",
		Auth:   map[string]string{"api_token": "secret-token"},
		Config: map[string]string{"target": "site"},
	})
	if err != nil {
		t.Fatalf("list options: %v", err)
	}
	if len(options) != 2 || options[0].Label != "Site A" || options[1].Value != "b.example.com" {
		t.Fatalf("unexpected options: %+v", options)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read options request: %v", err)
	}
	for _, want := range []string{
		`"action":"options"`,
		`"provider":"fakepanel"`,
		`"field":"sites"`,
		`"source":"baota_sites"`,
		`"query":"example"`,
		`"api_token":"secret-token"`,
		`"target":"site"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("options request missing %s: %s", want, data)
		}
	}
}

func TestListOptionsUnsupported(t *testing.T) {
	resetExternalState(t)
	if _, err := ListOptions(context.Background(), "missing", OptionsRequest{}); err == nil {
		t.Fatal("expected missing target error")
	}
	RegisterExternal(fakeDeployer{id: "fake", source: SourceExec})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if _, err := ListOptions(context.Background(), "fake", OptionsRequest{}); !errors.Is(err, ErrOptionsUnsupported) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlan(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "plan.json")
	manifest := strings.Replace(testManifest, `"deploy_fields"`, `"capabilities":["plan-v1"],"deploy_fields"`, 1)
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
plan)
  cat > %q
  printf '{"ok":true,"field":"sites","multiple":true,"objects":[{"value":"a.example.com","name":"Site A","domains":["a.example.com","www.a.example.com"]},{"value":"b.example.com"}]}'
  ;;
*)
  exit 2
  ;;
esac
`, manifest, output)
	writeHelper(t, dir, "fake-helper", script, 0o700)
	Discover(DiscoverConfig{Dir: dir, EnabledDeployers: []string{"fakepanel"}})

	result, err := Plan(context.Background(), "fakepanel", PlanRequest{
		Auth:   map[string]string{"api_token": "secret-token"},
		Config: map[string]string{"target": "site"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if result.Field != "sites" || !result.Multiple || len(result.Objects) != 2 {
		t.Fatalf("unexpected plan: %+v", result)
	}
	if len(result.Objects[0].Domains) != 2 || result.Objects[0].Domains[1] != "www.a.example.com" {
		t.Fatalf("unexpected object: %+v", result.Objects[0])
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read plan request: %v", err)
	}
	for _, want := range []string{
		`"action":"plan"`,
		`"provider":"fakepanel"`,
		`"api_token":"secret-token"`,
		`"target":"site"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("plan request missing %s: %s", want, data)
		}
	}
}

func TestPlanUnsupported(t *testing.T) {
	resetExternalState(t)
	if _, err := Plan(context.Background(), "missing", PlanRequest{}); err == nil {
		t.Fatal("expected missing target error")
	}
	RegisterExternal(fakeDeployer{id: "fake", source: SourceExec})
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if _, err := Plan(context.Background(), "fake", PlanRequest{}); !errors.Is(err, ErrPlanUnsupported) {
		t.Fatalf("unexpected error: %v", err)
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
	localized := Manifest{Protocol: "v1", Deployers: []ManifestDeployer{{
		ID:              "ok",
		NameI18n:        map[string]string{"zh-CN": "名称", "en-US": "Name"},
		DescriptionI18n: map[string]string{"en-US": "Description"},
		Fields: []ManifestField{{
			Key:             "token",
			Type:            "text",
			LabelI18n:       map[string]string{"en-US": "Token"},
			PlaceholderI18n: map[string]string{"en-US": "Enter token"},
			HelpI18n:        map[string]string{"en-US": "Help"},
			Options:         []ManifestOption{{Value: "a", LabelI18n: map[string]string{"en-US": "Option A"}}},
		}},
	}}}
	if err := validateManifest(localized); err != nil {
		t.Fatalf("valid localized manifest rejected: %v", err)
	}
	cases := []Manifest{
		{Protocol: "v2", Deployers: []ManifestDeployer{{ID: "ok"}}},
		{Protocol: "v1"},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "Bad ID"}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Aliases: []string{"b"}}, {ID: "b"}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Fields: []ManifestField{{Key: "x", Type: "text"}}, DeployFields: []ManifestField{{Key: "x", Type: "text"}}}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Fields: []ManifestField{{Key: "x", Type: "unknown"}}}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", NameI18n: map[string]string{"bad locale": "x"}}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", Fields: []ManifestField{{Key: "x", Type: "text", LabelI18n: map[string]string{"en-US": strings.Repeat("x", 129)}}}}}},
		{Protocol: "v1", Deployers: []ManifestDeployer{{ID: "a", DeployFields: []ManifestField{{Key: "x", Type: "text", OptionsSource: "bad source"}}}}},
	}
	for index, manifest := range cases {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("case %d: expected error", index)
		}
	}
}

// TestVerifyExternalDeployer 验证 verify-v1 能力位与结构化检查结果。
func TestVerifyExternalDeployer(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "verify.json")
	manifest := `{"protocol":"v1","capabilities":["verify-v1"],"name":"verify helper","version":"1.0.0","deployers":[{"id":"verifypanel","name":"Verify Panel","capabilities":["verify-v1"],"fields":[{"key":"api_token","label":"API Token","type":"password"}]}]}`
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
verify)
  cat > %q
  printf '{"ok":false,"checks":[{"key":"auth","status":"failed","message":"bad token"}]}'
  ;;
*)
  exit 2
  ;;
esac
`, manifest, output)
	writeHelper(t, dir, "verify-helper", script, 0o700)
	result := Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledDeployers: []string{"verifypanel"}})
	if result.Deployers != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	plugin, ok := Lookup("verifypanel")
	if !ok {
		t.Fatal("deployer not registered")
	}
	verifier, ok := plugin.(Verifier)
	if !ok {
		t.Fatal("deployer should implement Verifier")
	}
	response, err := verifier.Verify(context.Background(), Request{Auth: map[string]string{"api_token": "secret"}, Locale: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || len(response.Checks) != 1 || response.Checks[0].Status != pluginproto.VerifyStatusFailed {
		t.Fatalf("unexpected response: %+v", response)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"verify"`, `"mode":"read"`, `"locale":"zh-CN"`, `"api_token":"secret"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("verify request missing %s: %s", want, data)
		}
	}
}

// TestVerifyUnsupportedDeployer 验证未声明能力的旧 helper 不会被调用。
func TestVerifyUnsupportedDeployer(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	writeHelper(t, dir, "legacy-helper", fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
verify)
  exit 3
  ;;
*)
  exit 2
  ;;
esac
`, testManifest), 0o700)
	Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledDeployers: []string{"fakepanel"}})
	plugin, ok := Lookup("fakepanel")
	if !ok {
		t.Fatal("deployer not registered")
	}
	verifier, ok := plugin.(Verifier)
	if !ok {
		t.Fatal("deployer should implement Verifier")
	}
	if _, err := verifier.Verify(context.Background(), Request{}); !errors.Is(err, ErrVerifyUnsupported) {
		t.Fatalf("expected ErrVerifyUnsupported, got %v", err)
	}
}
