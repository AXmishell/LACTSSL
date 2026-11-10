package service

import (
	"path/filepath"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return New(store.New(conn, cipher))
}

func TestDeployTypesSplitAuthAndDeployFields(t *testing.T) {
	for _, def := range DeployTypes {
		authKeys := map[string]bool{}
		for _, field := range def.Fields {
			authKeys[field.Key] = true
		}
		for _, field := range def.DeployFields {
			if authKeys[field.Key] {
				t.Errorf("%s 的字段 %s 同时出现在认证字段和部署字段中", def.Value, field.Key)
			}
		}
		if def.Value == "local" && len(def.Fields) != 0 {
			t.Errorf("local 不应包含认证字段: %+v", def.Fields)
		}
		if def.Value == "ssh" {
			if authKeys["cert_path"] || authKeys["key_path"] {
				t.Error("SSH 路径不应再出现在认证字段中")
			}
		}
	}
}

func TestMergeDeployConfig(t *testing.T) {
	def, _ := DeployTypeInfo("ssh")
	auth := map[string]string{
		"host":      "192.0.2.10",
		"user":      "root",
		"password":  "secret",
		"cert_path": "/old/cert.pem",
	}
	deploy := map[string]string{
		"cert_path": "/new/fullchain.pem",
		"key_path":  "/new/private.key",
	}
	cfg := MergeDeployConfig(def, auth, deploy)
	if cfg["host"] != "192.0.2.10" || cfg["password"] != "secret" {
		t.Fatalf("认证字段未保留: %+v", cfg)
	}
	if cfg["cert_path"] != "/new/fullchain.pem" || cfg["key_path"] != "/new/private.key" {
		t.Fatalf("部署参数未覆盖: %+v", cfg)
	}
}

func TestMigrateDeployConfigSplit(t *testing.T) {
	m := newTestManager(t)
	account := &model.DeployAccount{
		Name: "local",
		Type: "local",
		Config: map[string]string{
			"cert_path":      "/etc/nginx/ssl/fullchain.pem",
			"key_path":       "/etc/nginx/ssl/private.key",
			"reload_command": "nginx -s reload",
		},
	}
	if err := m.Store.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Name:      "example.com",
		Domains:   []string{"example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		RenewDays: 30,
		DeployIDs: []int64{account.ID},
		Enabled:   true,
		Status:    "pending",
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := m.MigrateDeployConfigSplit(); err != nil {
		t.Fatal(err)
	}

	gotTask, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var deployNode *model.PipelineNode
	for i := range gotTask.Pipeline {
		if gotTask.Pipeline[i].Type == model.PipelineDeploy {
			deployNode = &gotTask.Pipeline[i]
			break
		}
	}
	if deployNode == nil {
		t.Fatalf("迁移后缺少部署节点: %+v", gotTask.Pipeline)
	}
	if deployNode.Config.DeployConfig["cert_path"] != "/etc/nginx/ssl/fullchain.pem" {
		t.Fatalf("部署参数未迁移: %+v", deployNode.Config.DeployConfig)
	}

	gotAccount, err := m.Store.GetDeployAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotAccount.Config) != 0 {
		t.Fatalf("凭据中的部署参数未清理: %+v", gotAccount.Config)
	}

	if err := m.MigrateDeployConfigSplit(); err != nil {
		t.Fatal(err)
	}
	if marker, _ := m.Store.GetSetting(deployConfigSplitMigrationKey); marker != "1" {
		t.Fatalf("迁移标记未写入: %q", marker)
	}
}

func TestMigrateDeployConfigSplitKeepsAuthFields(t *testing.T) {
	m := newTestManager(t)
	account := &model.DeployAccount{
		Name: "ssh",
		Type: "ssh",
		Config: map[string]string{
			"host":      "192.0.2.10",
			"user":      "root",
			"password":  "secret",
			"cert_path": "/etc/ssl/fullchain.pem",
			"key_path":  "/etc/ssl/private.key",
			"auth_type": "password",
		},
	}
	if err := m.Store.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := m.MigrateDeployConfigSplit(); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.GetDeployAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config["host"] != "192.0.2.10" || got.Config["password"] != "secret" {
		t.Fatalf("认证字段丢失: %+v", got.Config)
	}
	if got.Config["cert_path"] != "" || got.Config["key_path"] != "" {
		t.Fatalf("部署字段未清理: %+v", got.Config)
	}
}
