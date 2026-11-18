package api

import (
	"testing"

	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

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
