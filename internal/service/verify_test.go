package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/i18n"
	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
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

func TestVerifyDNSAccountWithoutDomainOnlyChecksPermission(t *testing.T) {
	fake := &fakeChallengeProvider{}
	if err := dnsplugin.RegisterExternal(fakeDNSPlugin{provider: fake}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "test-account", "verify-fake", nil, "", "auto")
	if !result.OK {
		t.Fatalf("没有测试域名时应降级为权限检查并通过: %+v", result)
	}
	if result.Mode != "scope" {
		t.Fatalf("没有测试域名时应使用 scope 模式: %s", result.Mode)
	}
	if result.Message == "" {
		t.Fatal("权限检查应返回说明消息")
	}
	if fake.presentCalls != 0 || fake.cleanupCalls != 0 {
		t.Fatalf("权限检查不应写入 DNS 记录: present=%d cleanup=%d", fake.presentCalls, fake.cleanupCalls)
	}
	status := ""
	for _, check := range result.Checks {
		switch check.Key {
		case "domain":
			if check.Status != VerifyStatusSkipped {
				t.Fatalf("domain 检查应为 skipped: %+v", result.Checks)
			}
		case "access":
			status = check.Status
		}
	}
	if status != VerifyStatusOK {
		t.Fatalf("access 检查应为 ok: %+v", result.Checks)
	}
}

func TestVerifyDNSAccountWithoutDomainRejectsWriteMode(t *testing.T) {
	fake := &fakeChallengeProvider{}
	if err := dnsplugin.RegisterExternal(fakeDNSPlugin{provider: fake}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "", "write")
	if result.OK {
		t.Fatalf("显式写测试没有域名时不能返回通过: %+v", result)
	}
	if fake.presentCalls != 0 || fake.cleanupCalls != 0 {
		t.Fatalf("缺少域名时不应写入 DNS 记录: present=%d cleanup=%d", fake.presentCalls, fake.cleanupCalls)
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
	response   pluginproto.VerifyResponse
	readErr    error
	readCalls  int
	writeCalls int
	// writeErr 非空时模拟不支持单进程写验证的旧 helper。
	writeErr error
}

func (p *fakeVerifyingDNSPlugin) Verify(_ context.Context, _ map[string]string, domain, mode, locale string) (pluginproto.VerifyResponse, error) {
	if mode == "write" {
		p.writeCalls++
		return p.response, p.writeErr
	}
	p.readCalls++
	return p.response, p.readErr
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

	// helper 声明了只读能力但实际返回 unsupported 时，auto 模式应回退到写测试。
	plugin.response = pluginproto.VerifyResponse{OK: true, Checks: []pluginproto.VerifyCheck{
		{Key: "verify", Status: pluginproto.VerifyStatusUnsupported},
	}}
	// 旧 helper 不声明 verify-write-v1，宿主必须继续走 present/cleanup。
	plugin.writeErr = dnsplugin.ErrVerifyUnsupported
	result = m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "auto")
	if !result.OK || result.Mode != "write" || fake.presentCalls != 1 || fake.cleanupCalls != 1 {
		t.Fatalf("unsupported 应回退到写测试: %+v present=%d cleanup=%d", result, fake.presentCalls, fake.cleanupCalls)
	}
}

// 新 helper 在 verify 子命令内完成 present/cleanup 时，宿主不应再启动
// 额外的 present/cleanup 进程。
func TestVerifyDNSAccountUsesSingleProcessWriteWhenAvailable(t *testing.T) {
	fake := &fakeChallengeProvider{}
	plugin := &fakeVerifyingDNSPlugin{
		fakeDNSPlugin: fakeDNSPlugin{provider: fake},
		response: pluginproto.VerifyResponse{OK: true, Checks: []pluginproto.VerifyCheck{
			{Key: "write", Status: pluginproto.VerifyStatusOK},
			{Key: "cleanup", Status: pluginproto.VerifyStatusOK},
		}},
		// 只读探测不可用时 auto 会直接走单进程写验证。
		readErr: dnsplugin.ErrVerifyUnsupported,
	}
	if err := dnsplugin.RegisterExternal(plugin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "auto")
	if !result.OK || result.Mode != "write" {
		t.Fatalf("单进程写验证应通过: %+v", result)
	}
	if plugin.writeCalls != 1 {
		t.Fatalf("writeCalls = %d", plugin.writeCalls)
	}
	if fake.presentCalls != 0 || fake.cleanupCalls != 0 {
		t.Fatalf("不应再启动独立的 present/cleanup: present=%d cleanup=%d", fake.presentCalls, fake.cleanupCalls)
	}
}

func TestVerifyDNSAccountSingleProcessWriteReportsCleanupFailure(t *testing.T) {
	fake := &fakeChallengeProvider{}
	plugin := &fakeVerifyingDNSPlugin{
		fakeDNSPlugin: fakeDNSPlugin{provider: fake},
		response: pluginproto.VerifyResponse{OK: false, Checks: []pluginproto.VerifyCheck{
			{Key: "write", Status: pluginproto.VerifyStatusOK},
			{Key: "cleanup", Status: pluginproto.VerifyStatusFailed, Message: "cleanup failed"},
		}},
	}
	if err := dnsplugin.RegisterExternal(plugin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", "verify-fake", nil, "example.com", "write")
	if result.OK {
		t.Fatalf("清理失败时不能返回通过: %+v", result)
	}
	if plugin.writeCalls != 1 || fake.presentCalls != 0 || fake.cleanupCalls != 0 {
		t.Fatalf("应使用单进程写验证: write=%d present=%d cleanup=%d", plugin.writeCalls, fake.presentCalls, fake.cleanupCalls)
	}
	if got := verifyCheckMessage(result, "cleanup"); got != "cleanup failed" {
		t.Fatalf("cleanup message = %q", got)
	}
}

const tlsVerifyFailureMessage = `验证失败: 请求宝塔面板 API 失败: Post "https://panel.example.com:22462/***?***": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2026-11-19T08:15:11+08:00 is after 2026-11-17T10:59:59Z`

func TestNormalizeVerifyMessage(t *testing.T) {
	cases := []struct {
		name    string
		loc     i18n.Locale
		message string
		want    string
	}{
		{name: "expired", loc: i18n.ZhCN, message: tlsVerifyFailureMessage, want: "TLS 证书已过期或尚未生效，请先更新目标证书"},
		{name: "unknown authority", loc: i18n.EnUS, message: "x509: certificate signed by unknown authority", want: "The TLS certificate is not trusted; make sure the issuing CA has been imported"},
		{name: "hostname mismatch", loc: i18n.ZhCN, message: "tls: failed to verify certificate: x509: certificate is valid for panel.example.com, not 192.168.1.10", want: "TLS 证书与访问地址不匹配，请改用证书中的域名或关闭 TLS 校验"},
		{name: "ip sans mismatch", loc: i18n.ZhCN, message: "x509: cannot validate certificate for 192.168.1.10 because it doesn't contain any IP SANs", want: "TLS 证书与访问地址不匹配，请改用证书中的域名或关闭 TLS 校验"},
		{name: "case insensitive", loc: i18n.ZhCN, message: "TLS: FAILED TO VERIFY CERTIFICATE", want: "TLS 校验失败"},
		{name: "plain error", loc: i18n.ZhCN, message: "部署目标验证失败: 认证失败", want: "部署目标验证失败: 认证失败"},
		{name: "empty", loc: i18n.ZhCN, message: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeVerifyMessage(tc.loc, tc.message); got != tc.want {
				t.Fatalf("normalizeVerifyMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeTLSVerifyingDNSPlugin struct {
	fakeDNSPlugin
	id       string
	response pluginproto.VerifyResponse
}

func (p *fakeTLSVerifyingDNSPlugin) ID() string { return p.id }
func (p *fakeTLSVerifyingDNSPlugin) Verify(context.Context, map[string]string, string, string, string) (pluginproto.VerifyResponse, error) {
	return p.response, nil
}

func TestVerifyDNSAccountNormalizesTLSFailure(t *testing.T) {
	plugin := &fakeTLSVerifyingDNSPlugin{
		fakeDNSPlugin: fakeDNSPlugin{provider: &fakeChallengeProvider{}},
		id:            "verify-tls-dns",
		response: pluginproto.VerifyResponse{Checks: []pluginproto.VerifyCheck{
			{Key: "verify", Status: pluginproto.VerifyStatusFailed, Message: tlsVerifyFailureMessage},
		}},
	}
	if err := dnsplugin.RegisterExternal(plugin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dnsplugin.UnregisterSource(dnsplugin.SourceExec) })

	m := New(nil)
	result := m.VerifyDNSAccount(context.Background(), i18n.ZhCN, "", plugin.id, nil, "example.com", "read")
	if result.OK {
		t.Fatalf("TLS 校验失败时不能返回通过: %+v", result)
	}
	const expired = "TLS 证书已过期或尚未生效，请先更新目标证书"
	if result.Message != expired {
		t.Fatalf("result.Message = %q", result.Message)
	}
	if got := verifyCheckMessage(result, "verify"); got != expired {
		t.Fatalf("verify check message = %q", got)
	}
}

type fakeVerifyingDeployer struct {
	id       string
	response pluginproto.VerifyResponse
	err      error
}

func (d *fakeVerifyingDeployer) ID() string                  { return d.id }
func (d *fakeVerifyingDeployer) Aliases() []string           { return nil }
func (d *fakeVerifyingDeployer) Source() deployplugin.Source { return deployplugin.SourceExec }
func (d *fakeVerifyingDeployer) Deploy(context.Context, deployplugin.Request) error {
	return nil
}
func (d *fakeVerifyingDeployer) Info() deployplugin.ExternalDeployer {
	return deployplugin.ExternalDeployer{ID: d.id, Name: "TLS Verify", Enabled: true}
}
func (d *fakeVerifyingDeployer) Verify(context.Context, deployplugin.Request) (pluginproto.VerifyResponse, error) {
	return d.response, d.err
}

func TestVerifyDeployAccountNormalizesTLSFailure(t *testing.T) {
	deployer := &fakeVerifyingDeployer{id: "verify-tls-deploy"}
	if err := deployplugin.RegisterExternal(deployer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	m := New(nil)
	deployer.response = pluginproto.VerifyResponse{Checks: []pluginproto.VerifyCheck{
		{Key: "verify", Status: pluginproto.VerifyStatusFailed, Message: tlsVerifyFailureMessage},
	}}
	result := m.VerifyDeployAccount(context.Background(), i18n.ZhCN, deployer.id, nil, nil, "auto", nil)
	if result.OK {
		t.Fatalf("TLS 校验失败时不能返回通过: %+v", result)
	}
	const expired = "TLS 证书已过期或尚未生效，请先更新目标证书"
	if result.Message != expired {
		t.Fatalf("result.Message = %q", result.Message)
	}
	if got := verifyCheckMessage(result, "verify"); got != expired {
		t.Fatalf("verify check message = %q", got)
	}

	// 插件进程执行失败时同样收敛提示。
	deployer.response = pluginproto.VerifyResponse{}
	deployer.err = errors.New(tlsVerifyFailureMessage)
	result = m.VerifyDeployAccount(context.Background(), i18n.ZhCN, deployer.id, nil, nil, "auto", nil)
	if result.OK || result.Message != expired {
		t.Fatalf("执行错误未归一化: %+v", result)
	}

	// 非 TLS 错误必须原样保留，避免掩盖鉴权、权限等问题。
	deployer.err = errors.New("authentication failed")
	result = m.VerifyDeployAccount(context.Background(), i18n.ZhCN, deployer.id, nil, nil, "auto", nil)
	if result.OK || result.Message != "部署目标验证失败: authentication failed" {
		t.Fatalf("非 TLS 错误不应被改写: %+v", result)
	}
}

func verifyCheckMessage(result VerifyResult, key string) string {
	for _, check := range result.Checks {
		if check.Key == key {
			return check.Message
		}
	}
	return ""
}
