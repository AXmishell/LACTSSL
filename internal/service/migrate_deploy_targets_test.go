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

func (d *targetMergeDeployer) Info() deployplugin.ExternalDeployer {
	key := "target"
	values := []string{"site", "panel"}
	switch d.id {
	case "qiniu":
		key = "domain_type"
		values = []string{"cdn", "kodo"}
	case "baota":
		values = []string{"site", "panel", "docker"}
	case "acepanel":
		values = []string{"site", "panel"}
	case "safeline":
		values = []string{"waf", "panel", "portal"}
	case "tencent":
		values = []string{"cdn", "cos", "teo", "waf"}
	case "aliyun":
		values = []string{"cdn", "dcdn", "esa", "oss", "waf"}
	case "volcengine":
		values = []string{"cdn", "dcdn"}
	}
	options := make([]deployplugin.ManifestOption, 0, len(values))
	for _, value := range values {
		options = append(options, deployplugin.ManifestOption{Value: value})
	}
	return deployplugin.ExternalDeployer{
		ID: d.id,
		DeployFields: []deployplugin.ManifestField{{
			Key: key, Type: "select", Options: options,
		}},
	}
}

type legacyTargetDeployer struct{ id string }

func (d *legacyTargetDeployer) ID() string                  { return d.id }
func (d *legacyTargetDeployer) Aliases() []string           { return nil }
func (d *legacyTargetDeployer) Source() deployplugin.Source { return deployplugin.SourceExec }
func (d *legacyTargetDeployer) Deploy(context.Context, deployplugin.Request) error {
	return nil
}
func (d *legacyTargetDeployer) Info() deployplugin.ExternalDeployer {
	return deployplugin.ExternalDeployer{ID: d.id}
}

func registerMergedDeployers(t *testing.T) {
	t.Helper()
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })
	for _, id := range []string{"qiniu", "baota", "1panel", "acepanel", "safeline", "tencent", "aliyun", "volcengine"} {
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

func TestMigrateDeployTargetsVendorGroups(t *testing.T) {
	registerMergedDeployers(t)
	m := newTestManager(t)

	cases := []struct {
		legacy    string
		canonical string
		mode      string
	}{
		{"baota_docker", "baota", "docker"},
		{"acepanel_panel", "acepanel", "panel"},
		{"safeline_panel", "safeline", "panel"},
		{"safeline_portal", "safeline", "portal"},
		{"tencent_cdn", "tencent", "cdn"},
		{"tencent_cos", "tencent", "cos"},
		{"tencent_teo", "tencent", "teo"},
		{"tencent_waf", "tencent", "waf"},
		{"aliyun_cdn", "aliyun", "cdn"},
		{"aliyun_dcdn", "aliyun", "dcdn"},
		{"aliyun_esa", "aliyun", "esa"},
		{"aliyun_oss", "aliyun", "oss"},
		{"aliyun_waf", "aliyun", "waf"},
		{"volcengine_cdn", "volcengine", "cdn"},
		{"volcengine_dcdn", "volcengine", "dcdn"},
	}

	nodes := []model.PipelineNode{{ID: "apply", Type: model.PipelineApply, Enabled: true}}
	accounts := make([]*model.DeployAccount, 0, len(cases))
	for index, tc := range cases {
		account := &model.DeployAccount{Name: tc.legacy, Type: tc.legacy, Config: map[string]string{"token": "value"}}
		if err := m.Store.CreateDeployAccount(account); err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account)
		nodes = append(nodes, model.PipelineNode{
			ID:      "deploy-" + strconv.Itoa(index+1),
			Type:    model.PipelineDeploy,
			Enabled: true,
			Config:  model.PipelineConfig{DeployAccountID: account.ID},
		})
	}
	task := &model.Task{
		Name:      "vendor-groups",
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
	// 旧版本已经写入过旧迁移标记，v2 仍必须执行。
	if err := m.Store.SetSetting("migration.deploy_target_merge", "1"); err != nil {
		t.Fatal(err)
	}

	if err := m.MigrateDeployTargets(); err != nil {
		t.Fatal(err)
	}

	for index, tc := range cases {
		got, err := m.Store.GetDeployAccount(accounts[index].ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != tc.canonical {
			t.Errorf("%s type = %q, want %q", tc.legacy, got.Type, tc.canonical)
		}
		if got.Config["target"] != tc.mode {
			t.Errorf("%s target = %q, want %q", tc.legacy, got.Config["target"], tc.mode)
		}
	}
	gotTask, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, tc := range cases {
		node := gotTask.Pipeline[index+1]
		if got := node.Config.DeployConfig["target"]; got != tc.mode {
			t.Errorf("node %s target = %q, want %q", tc.legacy, got, tc.mode)
		}
	}
	if value, _ := m.Store.GetSetting(deployTargetMergeMigrationKey); value != "1" {
		t.Fatalf("v2 migration marker = %q", value)
	}
}

func TestEveryDeprecatedDeployTypeHasMergeRule(t *testing.T) {
	for legacy, canonical := range DeprecatedDeployTypeCanonical {
		rule, ok := deployTargetMergeRuleFor(legacy)
		if !ok {
			t.Errorf("deprecated %s -> %s has no merge rule", legacy, canonical)
			continue
		}
		if rule.Canonical != canonical {
			t.Errorf("%s rule canonical = %s, want %s", legacy, rule.Canonical, canonical)
		}
		mode, ok := deployTargetModes[canonical]
		if !ok {
			t.Errorf("canonical %s has no target modes", canonical)
			continue
		}
		if mode.Key != rule.ModeKey {
			t.Errorf("%s mode key = %s, want %s", canonical, mode.Key, rule.ModeKey)
		}
		found := false
		for _, value := range mode.Values {
			if value == rule.ModeValue {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("canonical %s does not allow mode %s", canonical, rule.ModeValue)
		}
	}
}

func TestDeployModesMatchCatalog(t *testing.T) {
	for canonical, mode := range deployTargetModes {
		def, found := PluginDeployTypeInfo(canonical)
		if !found {
			t.Errorf("canonical %s has no catalog metadata", canonical)
			continue
		}
		var field *FieldDef
		for index := range def.DeployFields {
			if def.DeployFields[index].Key == mode.Key {
				field = &def.DeployFields[index]
				break
			}
		}
		if field == nil {
			t.Errorf("%s catalog has no %s deploy field", canonical, mode.Key)
			continue
		}
		values := make(map[string]bool, len(field.Options))
		for _, option := range field.Options {
			values[option.Value] = true
		}
		for _, value := range mode.Values {
			if !values[value] {
				t.Errorf("%s catalog mode %s does not support %s", canonical, mode.Key, value)
			}
		}
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

func TestLegacyHelperDoesNotClaimNewTargets(t *testing.T) {
	deployplugin.UnregisterSource(deployplugin.SourceExec)
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })
	for _, id := range []string{"safeline", "safeline_panel", "baota", "baota_docker"} {
		if err := deployplugin.RegisterExternal(&legacyTargetDeployer{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	if deprecatedDeployTypeReplaced("safeline_panel") {
		t.Fatal("legacy safeline helper should not replace panel target")
	}
	if deprecatedDeployTypeReplaced("baota_docker") {
		t.Fatal("legacy baota helper should not replace docker target")
	}
}

func TestLocalizeDeployTypesMergedVendors(t *testing.T) {
	registerMergedDeployers(t)
	values := map[string]bool{}
	for _, item := range LocalizeDeployTypes("zh-CN") {
		values[item.Value] = true
	}
	for _, id := range []string{"qiniu", "baota", "1panel", "acepanel", "safeline", "tencent", "aliyun", "volcengine"} {
		if !values[id] {
			t.Errorf("merged target %s should be listed", id)
		}
	}
	for _, id := range []string{
		"qiniu_cdn", "qiniu_kodo", "baota_site", "baota_panel", "baota_docker",
		"1panel_site", "1panel_panel", "acepanel_panel",
		"safeline_panel", "safeline_portal",
		"tencent_cdn", "tencent_cos", "tencent_teo", "tencent_waf",
		"aliyun_cdn", "aliyun_dcdn", "aliyun_esa", "aliyun_oss", "aliyun_waf",
		"volcengine_cdn", "volcengine_dcdn",
	} {
		if values[id] {
			t.Errorf("deprecated target %s should be hidden", id)
		}
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
