package api

import (
	"testing"

	"lactssl/internal/i18n"
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
	if err := body.validate(i18n.Default); err != nil {
		t.Fatal(err)
	}
	if len(body.Config) != 0 {
		t.Fatalf("目标凭据不应保存部署参数: %+v", body.Config)
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
	if err := body.validate(i18n.Default); err != nil {
		t.Fatal(err)
	}
	if body.Config["host"] != "192.0.2.10" || body.Config["password"] != "secret" {
		t.Fatalf("认证字段丢失: %+v", body.Config)
	}
	if _, ok := body.Config["cert_path"]; ok {
		t.Fatalf("部署字段未过滤: %+v", body.Config)
	}
}
