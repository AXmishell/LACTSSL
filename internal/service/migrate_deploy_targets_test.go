package service

import (
	"context"
	"strconv"
	"testing"

	"lactssl/internal/deployplugin"
	"lactssl/internal/model"
)

type targetMergeDeployer struct{ id string }

func (d *targetMergeDeployer) ID() string                  { return d.id }
func (d *targetMergeDeployer) Aliases() []string           { return nil }
func (d *targetMergeDeployer) Source() deployplugin.Source { return deployplugin.SourceExec }
func (d *targetMergeDeployer) Deploy(context.Context, deployplugin.Request) error {
	return nil
}

func registerMergedDeployers(t *testing.T) {
	t.Helper()
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })
	for _, id := range []string{"qiniu", "baota", "1panel"} {
		if err := deployplugin.RegisterExternal(&targetMergeDeployer{id: id}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
}

func TestMigrateDeployTargets(t *testing.T) {
	registerMergedDeployers(t)
	m := newTestManager(t)

	accounts := []*model.DeployAccount{
		{Name: "qiniu-cdn", Type: "qiniu_cdn", Config: map[string]string{"access_key": "ak", "secret_key": "sk"}},
		{Name: "qiniu-kodo", Type: "qiniu_kodo", Config: map[string]string{"access_key": "ak", "secret_key": "sk"}},
		{Name: "baota-site", Type: "baota_site", Config: map[string]string{"panel_url": "http://panel", "api_secret": "secret"}},
		{Name: "baota-panel", Type: "baota_panel", Config: map[string]string{"panel_url": "http://panel", "api_secret": "secret"}},
		{Name: "1panel-site", Type: "1panel_site", Config: map[string]string{"panel_url": "http://panel", "api_key": "key"}},
		{Name: "1panel-panel", Type: "1panel_panel", Config: map[string]string{"panel_url": "http://panel", "api_key": "key"}},
	}
	nodes := []model.PipelineNode{
		{ID: "apply", Type: model.PipelineApply, Enabled: true},
	}
	for index, account := range accounts {
		if err := m.Store.CreateDeployAccount(account); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, model.PipelineNode{
			ID:      "deploy-" + strconv.Itoa(index+1),
			Type:    model.PipelineDeploy,
			Enabled: true,
			Config:  model.PipelineConfig{DeployAccountID: account.ID},
		})
	}
	// 节点中已有的部署对象不应被覆盖。
	nodes[1].Config.DeployConfig = map[string]string{"domain_type": "kodo"}
	wantNodeModes := []string{"kodo", "kodo", "site", "panel", "site", "panel"}
	task := &model.Task{
		Name:      "merge-targets",
		Domains:   []string{"example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		RenewDays: 30,
		Pipeline:  nodes,
		Enabled:   true,
		Status:    "pending",
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := m.MigrateDeployTargets(); err != nil {
		t.Fatal(err)
	}

	wantTypes := []string{"qiniu", "qiniu", "baota", "baota", "1panel", "1panel"}
	wantModes := []string{"cdn", "kodo", "site", "panel", "site", "panel"}
	for index, account := range accounts {
		got, err := m.Store.GetDeployAccount(account.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != wantTypes[index] {
			t.Errorf("account %d type = %q, want %q", account.ID, got.Type, wantTypes[index])
		}
		key := "target"
		if index < 2 {
			key = "domain_type"
		}
		if got.Config[key] != wantModes[index] {
			t.Errorf("account %d %s = %q, want %q", account.ID, key, got.Config[key], wantModes[index])
		}
	}

	gotTask, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range accounts {
		node := gotTask.Pipeline[index+1]
		key := "target"
		want := wantNodeModes[index]
		if index < 2 {
			key = "domain_type"
		}
		if got := node.Config.DeployConfig[key]; got != want {
			t.Errorf("node %s %s = %q, want %q", node.ID, key, got, want)
		}
	}
	// 已存在的 kodo 选择保持原样。
	if got := gotTask.Pipeline[2].Config.DeployConfig["domain_type"]; got != "kodo" {
		t.Errorf("existing node mode was overwritten: %q", got)
	}

	if value, _ := m.Store.GetSetting(deployTargetMergeMigrationKey); value != "1" {
		t.Fatalf("migration marker = %q", value)
	}
	// 幂等：再次执行不报错，也不会改写数据。
	if err := m.MigrateDeployTargets(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateDeployTargetsKeepsLegacyWhenHelperMissing(t *testing.T) {
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	m := newTestManager(t)
	account := &model.DeployAccount{Name: "qiniu-kodo", Type: "qiniu_kodo", Config: map[string]string{"access_key": "ak", "secret_key": "sk"}}
	if err := m.Store.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := m.MigrateDeployTargets(); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.GetDeployAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "qiniu_kodo" {
		t.Fatalf("legacy type should be kept without helper: %q", got.Type)
	}
	if value, _ := m.Store.GetSetting(deployTargetMergeMigrationKey); value == "1" {
		t.Fatal("migration should not be marked complete without the merged helper")
	}
}

func TestLocalizeDeployTypesHidesDeprecatedAfterMerge(t *testing.T) {
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })
	for _, id := range []string{"qiniu", "qiniu_cdn", "qiniu_kodo"} {
		if err := deployplugin.RegisterExternal(&targetMergeDeployer{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	list := LocalizeDeployTypes("zh-CN")
	values := map[string]bool{}
	for _, item := range list {
		values[item.Value] = true
	}
	if !values["qiniu"] {
		t.Fatal("merged qiniu target should be listed")
	}
	if values["qiniu_cdn"] || values["qiniu_kodo"] {
		t.Fatal("deprecated qiniu targets should be hidden when merged target is available")
	}

	deployplugin.UnregisterSource(deployplugin.SourceExec)
	for _, id := range []string{"qiniu_cdn", "qiniu_kodo"} {
		if err := deployplugin.RegisterExternal(&targetMergeDeployer{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	list = LocalizeDeployTypes("zh-CN")
	values = map[string]bool{}
	for _, item := range list {
		values[item.Value] = true
	}
	if values["qiniu"] {
		t.Fatal("merged target should not be listed without a merged helper")
	}
	if !values["qiniu_cdn"] || !values["qiniu_kodo"] {
		t.Fatal("legacy qiniu targets should be listed when the merged helper is unavailable")
	}
}

func TestDeployModeFallback(t *testing.T) {
	account := &model.DeployAccount{Type: "qiniu", Config: map[string]string{"domain_type": "kodo"}}
	if got := deployModeFallback(account, nil)["domain_type"]; got != "kodo" {
		t.Fatalf("fallback domain_type = %q", got)
	}
	if got := deployModeFallback(account, map[string]string{"domain_type": "cdn"})["domain_type"]; got != "cdn" {
		t.Fatalf("explicit domain_type should win: %q", got)
	}
	legacy := &model.DeployAccount{Type: "qiniu_kodo"}
	if got := deployModeFallback(legacy, nil)["domain_type"]; got != "kodo" {
		t.Fatalf("legacy fallback domain_type = %q", got)
	}
}
