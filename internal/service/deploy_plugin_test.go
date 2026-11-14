package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"lactssl/internal/deployplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

type captureDeployer struct {
	id  string
	req deployplugin.Request
	err error
}

func (d *captureDeployer) ID() string        { return d.id }
func (d *captureDeployer) Aliases() []string { return []string{"capture-alias"} }
func (d *captureDeployer) Source() deployplugin.Source {
	return deployplugin.SourceExec
}
func (d *captureDeployer) Deploy(_ context.Context, req deployplugin.Request) error {
	d.req = req
	return d.err
}

func (d *captureDeployer) Info() deployplugin.ExternalDeployer {
	return deployplugin.ExternalDeployer{
		ID:      d.id,
		Name:    "Capture Target",
		Aliases: []string{"capture-alias"},
		Fields: []deployplugin.ManifestField{
			{Key: "api_token", Label: "API Token", Type: "password", Secret: true, Required: true},
		},
		DeployFields: []deployplugin.ManifestField{
			{Key: "site", Label: "Site", Type: "text", Required: true},
		},
	}
}

func TestBuiltinDeployersCoverDeployTypes(t *testing.T) {
	for _, def := range DeployTypes {
		if _, ok := builtinDeployers[def.Value]; !ok {
			t.Fatalf("内置部署类型 %s 缺少实现", def.Value)
		}
	}
	if len(builtinDeployers) != len(DeployTypes) {
		t.Fatalf("内置实现数量 %d 与类型列表 %d 不一致", len(builtinDeployers), len(DeployTypes))
	}
	// 本地文件与 SSH 是仅有的内置目标，其余目标必须由部署插件提供。
	if _, ok := builtinDeployers["local"]; !ok {
		t.Fatal("local should stay built-in")
	}
	if _, ok := builtinDeployers["ssh"]; !ok {
		t.Fatal("ssh should stay built-in")
	}
}

func TestPluginDeployTypesAreImplementedByPlugin(t *testing.T) {
	if len(PluginDeployTypes) == 0 {
		t.Fatal("plugin deploy types should not be empty")
	}
	for _, def := range PluginDeployTypes {
		if _, ok := builtinDeployers[def.Value]; ok {
			t.Fatalf("插件目标 %s 不应再作为内置实现", def.Value)
		}
		if _, ok := DeployTypeInfo(def.Value); !ok {
			t.Fatalf("插件目标 %s 缺少元数据", def.Value)
		}
	}
}

func TestPluginDeployTypeMetadataSurvivesMissingPlugin(t *testing.T) {
	if deployplugin.Has("ftp") {
		deployplugin.UnregisterSource(deployplugin.SourceExec)
	}
	def, ok := DeployTypeInfo("ftp")
	if !ok {
		t.Fatal("plugin deploy metadata should remain available")
	}
	if def.Source != string(deployplugin.SourceExec) || def.Available {
		t.Fatalf("unexpected def: %+v", def)
	}
	// 历史凭据仍能按元数据字段脱敏。
	if len(def.Fields) == 0 || len(def.DeployFields) == 0 {
		t.Fatalf("metadata fields missing: %+v", def)
	}
}

func TestLocalizeDeployTypesOnlyBuiltinsWithoutPlugins(t *testing.T) {
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	list := LocalizeDeployTypes(i18n.ZhCN)
	if len(list) != len(DeployTypes) {
		t.Fatalf("deploy types without plugins = %d, builtins = %d", len(list), len(DeployTypes))
	}
	for _, item := range list {
		if item.Source != string(deployplugin.SourceBuiltin) {
			t.Fatalf("unexpected non-builtin target without plugins: %+v", item)
		}
		if item.Value != "local" && item.Value != "ssh" {
			t.Fatalf("unexpected builtin target: %+v", item)
		}
	}
}

func TestLocalizeDeployTypesIncludesLoadedPluginTarget(t *testing.T) {
	deployer := &captureDeployer{id: "ftp"}
	if err := deployplugin.RegisterExternal(deployer); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	found := false
	for _, item := range LocalizeDeployTypes(i18n.ZhCN) {
		if item.Value != "ftp" {
			continue
		}
		found = true
		if item.Source != string(deployplugin.SourceExec) || !item.Available {
			t.Fatalf("unexpected plugin item: %+v", item)
		}
		if item.Label == "" {
			t.Fatal("plugin label should be localized from metadata")
		}
	}
	if !found {
		t.Fatal("loaded plugin target missing from deploy type list")
	}
}

func TestExternalDeployerIntegration(t *testing.T) {
	deployer := &captureDeployer{id: "capture-target"}
	if err := deployplugin.RegisterExternal(deployer); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	def, found := DeployTypeInfo("capture-target")
	if !found {
		t.Fatal("external deploy type not found")
	}
	if def.Source != string(deployplugin.SourceExec) || !def.Available {
		t.Fatalf("unexpected def: %+v", def)
	}
	if len(def.Fields) != 1 || def.Fields[0].Key != "api_token" || !def.Fields[0].Secret {
		t.Fatalf("unexpected auth fields: %+v", def.Fields)
	}
	if len(def.DeployFields) != 1 || def.DeployFields[0].Key != "site" {
		t.Fatalf("unexpected deploy fields: %+v", def.DeployFields)
	}

	// 别名同样可以解析到外部部署类型。
	if _, found := DeployTypeInfo("capture-alias"); !found {
		t.Fatal("alias should resolve")
	}

	// 元数据列表应包含外部目标，且不重复内置项。
	list := LocalizeDeployTypes("zh-CN")
	found = false
	for _, item := range list {
		if item.Value == "capture-target" {
			found = true
		}
	}
	if !found {
		t.Fatal("external deploy type missing from localized list")
	}

	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		Domains:       []string{"example.com", "www.example.com"},
		Issuer:        "Test CA",
		Serial:        "123",
		NotBefore:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		CertPEM:       "CERT",
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE KEY",
	}
	account := &model.DeployAccount{Type: "capture-target", Config: map[string]string{"api_token": "token"}}
	manager := &Manager{}
	err := manager.Deploy(cert, account, map[string]string{"site": "example.com"})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if deployer.req.Auth["api_token"] != "token" {
		t.Fatalf("auth not passed: %+v", deployer.req.Auth)
	}
	if deployer.req.Config["site"] != "example.com" {
		t.Fatalf("config not passed: %+v", deployer.req.Config)
	}
	if deployer.req.Certificate.KeyPEM != "PRIVATE KEY" || deployer.req.Certificate.Domain != "example.com" {
		t.Fatalf("certificate not passed: %+v", deployer.req.Certificate)
	}
	if deployer.req.Certificate.NotAfter != "2026-04-01T00:00:00Z" {
		t.Fatalf("unexpected not_after: %q", deployer.req.Certificate.NotAfter)
	}
}

func TestDeployBuiltinRejectsUnknownType(t *testing.T) {
	err := DeployBuiltin(context.Background(), "missing", &model.Certificate{KeyPEM: "key"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestPluginCertificateZeroTimes(t *testing.T) {
	value := pluginCertificate(&model.Certificate{PrimaryDomain: "example.com"})
	if value.NotBefore != "" || value.NotAfter != "" {
		t.Fatalf("zero times should stay empty: %+v", value)
	}
}
