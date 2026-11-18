package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/i18n"
	dnsplugin "lactssl/internal/plugin/dns"
	pluginproto "lactssl/internal/plugin/proto"
)

type fakeChallengeProvider struct {
	presentCalls int
	cleanupCalls int
	presentErr   error
	cleanupErr   error
}

func (p *fakeChallengeProvider) Present(string, string, string) error {
	p.presentCalls++
	return p.presentErr
}

func (p *fakeChallengeProvider) CleanUp(string, string, string) error {
	p.cleanupCalls++
	return p.cleanupErr
}

type fakeDNSPlugin struct {
	provider *fakeChallengeProvider
}

func (p fakeDNSPlugin) ID() string               { return "verify-fake" }
func (p fakeDNSPlugin) Aliases() []string        { return nil }
func (p fakeDNSPlugin) Source() dnsplugin.Source { return dnsplugin.SourceExec }
func (p fakeDNSPlugin) New(map[string]string) (challenge.Provider, error) {
	return p.provider, nil
}

func TestVerifyDNSAccountWritesAndCleansUp(t *testing.T) {
	fake := &fakeChallengeProvider{}
	if err := dnsplugin.RegisterExternal(fakeDNSPlugin{provider: fake}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "test-account", "verify-fake", nil, "Example.COM.", "auto")
	if !result.OK {
		t.Fatalf("验证应通过: %+v", result)
	}
	if fake.presentCalls != 1 || fake.cleanupCalls != 1 {
		t.Fatalf("应写入并清理一次记录: present=%d cleanup=%d", fake.presentCalls, fake.cleanupCalls)
	}
	if result.Mode != "write" {
		t.Fatalf("默认模式应为 write: %s", result.Mode)
	}
}

func TestVerifyDNSAccountReportsCleanupFailure(t *testing.T) {
	fake := &fakeChallengeProvider{cleanupErr: os.ErrPermission}
	if err := dnsplugin.RegisterExternal(fakeDNSPlugin{provider: fake}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "write")
	if result.OK {
		t.Fatal("清理失败时不能返回验证通过")
	}
	status := ""
	for _, check := range result.Checks {
		if check.Key == "cleanup" {
			status = check.Status
		}
	}
	if status != VerifyStatusFailed {
		t.Fatalf("cleanup 检查应为 failed: %+v", result.Checks)
	}
}

func TestNormalizeVerifyDomain(t *testing.T) {
	cases := map[string]string{
		" Example.COM. ":   "example.com",
		"*.example.com":    "example.com",
		"example.com/path": "example.com",
		"127.0.0.1":        "",
		"*.127.0.0.1":      "",
		"":                 "",
		"not-a-domain":     "",
	}
	for input, want := range cases {
		if got := NormalizeVerifyDomain(input); got != want {
			t.Fatalf("NormalizeVerifyDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVerifyLocalDeployCreatesAndRemovesTempFile(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]string{
		"cert_path": filepath.Join(dir, "certs", "fullchain.pem"),
		"key_path":  filepath.Join(dir, "certs", "private.key"),
	}
	m := New(nil)
	result := m.VerifyDeployAccount(context.Background(), i18n.ZhCN, "local", nil, cfg, "auto", nil)
	if !result.OK {
		t.Fatalf("本地目录应可写: %+v", result)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".lactssl-verify-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("验证后不应残留临时文件: %v", matches)
	}
}

func TestVerifyLocalDeploySkipsWithoutPaths(t *testing.T) {
	m := New(nil)
	result := m.VerifyDeployAccount(context.Background(), i18n.ZhCN, "local", nil, nil, "auto", nil)
	if !result.OK {
		t.Fatalf("未提供路径时应降级为跳过而不是失败: %+v", result)
	}
	if len(result.Checks) != 2 || result.Checks[1].Status != VerifyStatusSkipped {
		t.Fatalf("应返回跳过检查: %+v", result.Checks)
	}
}

func TestVerifyLocalDeployRejectsSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	m := New(nil)
	result := m.VerifyDeployAccount(context.Background(), i18n.ZhCN, "local", nil, map[string]string{
		"cert_path": path,
		"key_path":  path,
	}, "auto", nil)
	if result.OK {
		t.Fatal("证书与私钥同路径时不能通过")
	}
}

func TestVerifyPluginDeployReturnsUnsupported(t *testing.T) {
	m := New(nil)
	result := m.VerifyDeployAccount(context.Background(), i18n.ZhCN, "ftp", map[string]string{"api_token": "x"}, nil, "auto", nil)
	if result.OK {
		t.Fatal("当前插件未实现 Verifier，不应返回通过")
	}
	found := false
	for _, check := range result.Checks {
		if check.Status == VerifyStatusUnsupported {
			found = true
		}
	}
	if !found {
		t.Fatalf("应返回 unsupported: %+v", result.Checks)
	}
}

type fakeVerifyingDNSPlugin struct {
	fakeDNSPlugin
	response  pluginproto.VerifyResponse
	err       error
	readCalls int
}

func (p *fakeVerifyingDNSPlugin) Verify(_ context.Context, _ map[string]string, domain, mode, locale string) (pluginproto.VerifyResponse, error) {
	p.readCalls++
	if mode != "read" {
		return pluginproto.VerifyResponse{}, nil
	}
	return p.response, p.err
}

func TestVerifyDNSAccountUsesReadOnlyWhenAvailable(t *testing.T) {
	fake := &fakeChallengeProvider{}
	plugin := &fakeVerifyingDNSPlugin{
		fakeDNSPlugin: fakeDNSPlugin{provider: fake},
		response: pluginproto.VerifyResponse{OK: true, Checks: []pluginproto.VerifyCheck{
			{Key: "zone", Status: pluginproto.VerifyStatusOK, Message: "example.com"},
		}},
	}
	if err := dnsplugin.RegisterExternal(plugin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "read")
	if !result.OK || result.Mode != "read" {
		t.Fatalf("只读验证应通过: %+v", result)
	}
	if fake.presentCalls != 0 || fake.cleanupCalls != 0 {
		t.Fatalf("只读模式不应写入记录: present=%d cleanup=%d", fake.presentCalls, fake.cleanupCalls)
	}
	if plugin.readCalls != 1 {
		t.Fatalf("readCalls = %d", plugin.readCalls)
	}

	// auto 模式优先只读，同样不写记录。
	result = m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "auto")
	if !result.OK || result.Mode != "read" || fake.presentCalls != 0 {
		t.Fatalf("auto 模式应优先只读: %+v", result)
	}

	// helper 声明了能力但实际返回 unsupported 时，auto 模式回退到写测试。
	plugin.response = pluginproto.VerifyResponse{OK: true, Checks: []pluginproto.VerifyCheck{
		{Key: "verify", Status: pluginproto.VerifyStatusUnsupported},
	}}
	result = m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "auto")
	if !result.OK || result.Mode != "write" || fake.presentCalls != 1 || fake.cleanupCalls != 1 {
		t.Fatalf("unsupported 应回退到写测试: %+v present=%d cleanup=%d", result, fake.presentCalls, fake.cleanupCalls)
	}
}
