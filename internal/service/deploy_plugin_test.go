package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/plugin/deploy"
)

type captureDeployer struct {
	id       string
	req      deployplugin.Request
	err      error
	external *deployplugin.ExternalDeployer
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
	if d.external != nil {
		info := *d.external
		info.ID = d.id
		return info
	}
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
	ids := DefaultPluginDeployIDs()
	if len(ids) == 0 {
		t.Fatal("plugin deploy default IDs should not be empty")
	}
	for _, id := range ids {
		if _, ok := builtinDeployers[id]; ok {
			t.Fatalf("插件目标 %s 不应再作为内置实现", id)
		}
		if _, ok := DeployTypeInfo(id); !ok {
			t.Fatalf("插件目标 %s 缺少元数据", id)
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

func TestLegacyDeployerIDForMergedTargets(t *testing.T) {
	cases := []struct {
		canonical string
		target    string
		want      string
	}{
		{"baota", "", "baota_site"},
		{"baota", "docker", "baota_docker"},
		{"acepanel", "panel", "acepanel_panel"},
		{"safeline", "panel", "safeline_panel"},
		{"safeline", "portal", "safeline_portal"},
		{"tencent", "cos", "tencent_cos"},
		{"tencent", "teo", "tencent_teo"},
		{"aliyun", "oss", "aliyun_oss"},
		{"aliyun", "waf", "aliyun_waf"},
		{"volcengine", "dcdn", "volcengine_dcdn"},
	}
	for _, tc := range cases {
		cfg := map[string]string{}
		if tc.target != "" {
			cfg["target"] = tc.target
		}
		if got := legacyDeployerID(tc.canonical, cfg); got != tc.want {
			t.Errorf("%s/%s legacy id = %q, want %q", tc.canonical, tc.target, got, tc.want)
		}
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

func TestLocalizeDeployTypesUsesManifestTranslations(t *testing.T) {
	external := &deployplugin.ExternalDeployer{
		Name:            "默认名称",
		NameI18n:        map[string]string{"zh-CN": "默认名称", "en-US": "Manifest Target"},
		DescriptionI18n: map[string]string{"zh-CN": "默认描述", "en-US": "Manifest description"},
		Fields: []deployplugin.ManifestField{
			{Key: "token", Label: "令牌", LabelI18n: map[string]string{"zh-CN": "令牌", "en-US": "Token"}, Type: "text"},
		},
	}
	deployer := &captureDeployer{id: "manifest-i18n", external: external}
	if err := deployplugin.RegisterExternal(deployer); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	for _, tc := range []struct {
		locale i18n.Locale
		label  string
		field  string
	}{
		{i18n.ZhCN, "默认名称", "令牌"},
		{i18n.EnUS, "Manifest Target", "Token"},
	} {
		found := false
		for _, item := range LocalizeDeployTypes(tc.locale) {
			if item.Value != "manifest-i18n" {
				continue
			}
			found = true
			if item.Label != tc.label || item.Desc == "" {
				t.Fatalf("%s label/desc = %q/%q", tc.locale, item.Label, item.Desc)
			}
			if len(item.Fields) != 1 || item.Fields[0].Label != tc.field {
				t.Fatalf("%s field label = %+v", tc.locale, item.Fields)
			}
		}
		if !found {
			t.Fatalf("%s manifest target missing from list", tc.locale)
		}
	}
}

func TestExternalDeployerUsesManifestTranslations(t *testing.T) {
	external := deployplugin.ExternalDeployer{
		ID:              "example-panel",
		Name:            "默认名称",
		NameI18n:        map[string]string{"zh-CN": "默认名称", "en-US": "Example Panel"},
		Description:     "默认描述",
		DescriptionI18n: map[string]string{"zh-CN": "默认描述", "en-US": "Example description"},
		Fields: []deployplugin.ManifestField{
			{
				Key:       "token",
				Label:     "令牌",
				LabelI18n: map[string]string{"zh-CN": "令牌", "en-US": "Token"},
				Help:      "默认帮助",
				HelpI18n:  map[string]string{"zh-CN": "默认帮助", "en-US": "Help"},
				Options: []deployplugin.ManifestOption{
					{Label: "默认", LabelI18n: map[string]string{"zh-CN": "默认", "en-US": "Default"}, Value: "default"},
				},
			},
		},
	}

	def := externalDeployerDef(external)
	zh := def.Localized(i18n.ZhCN)
	en := def.Localized(i18n.EnUS)
	if zh.Label != "默认名称" || en.Label != "Example Panel" {
		t.Fatalf("unexpected localized labels: zh=%q en=%q", zh.Label, en.Label)
	}
	if zh.Desc != "默认描述" || en.Desc != "Example description" {
		t.Fatalf("unexpected localized descriptions: zh=%q en=%q", zh.Desc, en.Desc)
	}
	if zh.Fields[0].Label != "令牌" || en.Fields[0].Label != "Token" {
		t.Fatalf("unexpected field labels: zh=%q en=%q", zh.Fields[0].Label, en.Fields[0].Label)
	}
	if zh.Fields[0].Help != "默认帮助" || en.Fields[0].Help != "Help" {
		t.Fatalf("unexpected field help: zh=%q en=%q", zh.Fields[0].Help, en.Fields[0].Help)
	}
	if zh.Fields[0].Options[0].Label != "默认" || en.Fields[0].Options[0].Label != "Default" {
		t.Fatalf("unexpected option labels: zh=%q en=%q", zh.Fields[0].Options[0].Label, en.Fields[0].Options[0].Label)
	}
}
