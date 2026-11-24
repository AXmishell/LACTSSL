package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/host/deploy"
	"lactssl/internal/service"
)

type optionsDeployer struct {
	req deployplugin.OptionsRequest
}

func (d *optionsDeployer) ID() string        { return "optpanel" }
func (d *optionsDeployer) Aliases() []string { return nil }
func (d *optionsDeployer) Source() deployplugin.Source {
	return deployplugin.SourceExec
}
func (d *optionsDeployer) Deploy(context.Context, deployplugin.Request) error { return nil }
func (d *optionsDeployer) Info() deployplugin.ExternalDeployer {
	return deployplugin.ExternalDeployer{
		ID:     "optpanel",
		Name:   "Options Panel",
		Fields: []deployplugin.ManifestField{{Key: "api_secret", Label: "API Secret", Type: "password", Secret: true}},
		DeployFields: []deployplugin.ManifestField{
			{Key: "target", Label: "Target", Type: "select", Default: "site"},
			{Key: "sites", Label: "Sites", Type: "text", OptionsSource: "panel_sites", Multiple: true},
		},
	}
}
func (d *optionsDeployer) ListOptions(_ context.Context, req deployplugin.OptionsRequest) ([]deployplugin.ManifestOption, error) {
	d.req = req
	return []deployplugin.ManifestOption{{Label: "Docker A", Value: "docker-a"}}, nil
}

func TestListDeployAccountOptions(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	deployer := &optionsDeployer{}
	if err := deployplugin.RegisterExternal(deployer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	account := &model.DeployAccount{
		Name:   "panel",
		Type:   "optpanel",
		Config: map[string]string{"api_secret": "secret"},
	}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/deploy-accounts/%d/options", account.ID)
	body := `{"field":"sites","config":{"target":"docker"}}`
	response := sessionRequest(t, router, http.MethodPost, path, body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("options failed: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"value":"docker-a"`) {
		t.Fatalf("missing option: %s", response.Body.String())
	}
	if deployer.req.Field != "sites" || deployer.req.Source != "panel_sites" {
		t.Fatalf("unexpected options request: %+v", deployer.req)
	}
	if deployer.req.Config["target"] != "docker" || deployer.req.Auth["api_secret"] != "secret" {
		t.Fatalf("credentials/config missing from options request: %+v", deployer.req)
	}
}

func TestDeployAccountBodyKeepsOnlyAuthFields(t *testing.T) {
	body := deployAccountBody{
		Name: "local",
		Type: "local",
		Config: map[string]string{
			"cert_path":      "/etc/ssl/fullchain.pem",
			"key_path":       "/etc/ssl/private.key",
			"reload_command": "nginx -s reload",
		},
	}
	if err := body.validate(i18n.Default, nil); err != nil {
		t.Fatal(err)
	}
	if len(body.Config) != 0 {
		t.Fatalf("目标凭据不应保存部署参数: %+v", body.Config)
	}
}

func TestValidateFieldsSkipsHiddenRequired(t *testing.T) {
	fields := []service.FieldDef{
		{Key: "target", Type: "select", Default: "cdn"},
		{Key: "bucket", Type: "text", Required: true, ShowIf: &service.Condition{Key: "target", Value: "oss"}},
		{Key: "region", Type: "text", Required: true, ShowIf: &service.Condition{Key: "target", Values: []string{"oss", "waf"}}},
	}
	if err := validateFields(fields, map[string]string{"target": "cdn"}, i18n.Default); err != nil {
		t.Fatalf("隐藏的必填字段不应阻塞保存: %v", err)
	}
	if err := validateFields(fields, map[string]string{"target": "oss"}, i18n.Default); err == nil {
		t.Fatal("oss 应要求 bucket 和 region")
	}
	if err := validateFields(fields, map[string]string{"target": "oss", "bucket": "demo"}, i18n.Default); err == nil {
		t.Fatal("oss 应要求 region")
	}
	if err := validateFields(fields, map[string]string{"target": "oss", "bucket": "demo", "region": "oss-cn-hangzhou"}, i18n.Default); err != nil {
		t.Fatalf("oss 字段齐全时不应报错: %v", err)
	}
}

func TestDeployAccountBodyKeepsConnectionFields(t *testing.T) {
	body := deployAccountBody{
		Name: "ssh",
		Type: "ssh",
		Config: map[string]string{
			"host":      "192.0.2.10",
			"user":      "root",
			"password":  "secret",
			"cert_path": "/etc/ssl/fullchain.pem",
		},
	}
	if err := body.validate(i18n.Default, nil); err != nil {
		t.Fatal(err)
	}
	if body.Config["host"] != "192.0.2.10" || body.Config["password"] != "secret" {
		t.Fatalf("认证字段丢失: %+v", body.Config)
	}
	if _, ok := body.Config["cert_path"]; ok {
		t.Fatalf("部署字段未过滤: %+v", body.Config)
	}
}

// TestDeployAccountBodyKeepsSecretWhenMasked 验证脱敏后的空密钥不会清空已保存的密码。
func TestDeployAccountBodyKeepsSecretWhenMasked(t *testing.T) {
	base := map[string]string{"host": "192.0.2.10", "user": "root", "password": "old-secret", "port": "22"}
	body := deployAccountBody{
		Name:   "ssh",
		Type:   "ssh",
		Config: map[string]string{"host": "192.0.2.11", "user": "root", "password": ""},
	}
	if err := body.validate(i18n.Default, base); err != nil {
		t.Fatal(err)
	}
	if body.Config["password"] != "old-secret" {
		t.Fatalf("空密钥应保留原值: %+v", body.Config)
	}
	if body.Config["host"] != "192.0.2.11" {
		t.Fatalf("非密钥字段应更新: %+v", body.Config)
	}
}

// TestMaskProviderConfigHidesSecrets 验证接口脱敏只清空密钥字段。
func TestMaskProviderConfigHidesSecrets(t *testing.T) {
	def, ok := service.DeployTypeInfo("ssh")
	if !ok {
		t.Fatal("缺少 ssh 类型")
	}
	masked := service.MaskProviderConfig(def, map[string]string{
		"host": "192.0.2.10", "user": "root", "password": "secret", "private_key": "pem",
	})
	if masked["password"] != "" || masked["private_key"] != "" {
		t.Fatalf("密钥字段未脱敏: %+v", masked)
	}
	if masked["host"] != "192.0.2.10" || masked["user"] != "root" {
		t.Fatalf("非密钥字段不应被清空: %+v", masked)
	}
}
