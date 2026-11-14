package service

import (
	"strings"

	"lactssl/internal/deployplugin"
	"lactssl/internal/model"
)

const deployTargetMergeMigrationKey = "migration.deploy_target_merge"

// deployTargetMergeRule 描述一个旧部署目标合并到新目标后的默认部署对象。
// 旧账号和新账号都可能在凭据配置中保留该字段，作为任务节点未提供时的兜底。
type deployTargetMergeRule struct {
	Legacy    string
	Canonical string
	ModeKey   string
	ModeValue string
}

var deployTargetMergeRules = []deployTargetMergeRule{
	{Legacy: "qiniu_cdn", Canonical: "qiniu", ModeKey: "domain_type", ModeValue: "cdn"},
	{Legacy: "qiniu_kodo", Canonical: "qiniu", ModeKey: "domain_type", ModeValue: "kodo"},
	{Legacy: "baota_site", Canonical: "baota", ModeKey: "target", ModeValue: "site"},
	{Legacy: "baota_panel", Canonical: "baota", ModeKey: "target", ModeValue: "panel"},
	{Legacy: "1panel_site", Canonical: "1panel", ModeKey: "target", ModeValue: "site"},
	{Legacy: "1panel_panel", Canonical: "1panel", ModeKey: "target", ModeValue: "panel"},
}

// deployTargetMode 描述合并目标在凭据中保存的部署对象字段及允许值。
type deployTargetMode struct {
	Key    string
	Values []string
}

var deployTargetModes = map[string]deployTargetMode{
	"qiniu":  {Key: "domain_type", Values: []string{"cdn", "kodo"}},
	"baota":  {Key: "target", Values: []string{"site", "panel"}},
	"1panel": {Key: "target", Values: []string{"site", "panel"}},
}

func deployTargetMergeRuleFor(deployType string) (deployTargetMergeRule, bool) {
	deployType = strings.TrimSpace(deployType)
	for _, rule := range deployTargetMergeRules {
		if rule.Legacy == deployType {
			return rule, true
		}
	}
	return deployTargetMergeRule{}, false
}

// deployTargetModeForAccount 返回账号原本的部署对象。
// 旧账号由类型推导；已经迁移过的账号从凭据配置中的兜底字段恢复。
func deployTargetModeForAccount(account model.DeployAccount) (key, value string, ok bool) {
	if rule, found := deployTargetMergeRuleFor(account.Type); found {
		return rule.ModeKey, rule.ModeValue, true
	}
	mode, found := deployTargetModes[strings.TrimSpace(account.Type)]
	if !found {
		return "", "", false
	}
	value = strings.TrimSpace(account.Config[mode.Key])
	for _, allowed := range mode.Values {
		if value == allowed {
			return mode.Key, value, true
		}
	}
	return "", "", false
}

// MigrateDeployTargets 把已合并的部署目标从旧类型迁移到规范类型，并把原来的
// 网站/面板、CDN/Kodo 选择写入任务部署节点。
//
// 必须在 MigrateDeployConfigSplit 之后执行：旧迁移仍按旧类型从凭据中提取
// 部署参数，先改类型会导致七牛云 Kodo 的单域名 domain 字段无法识别。
func (m *Manager) MigrateDeployTargets() error {
	if value, err := m.Store.GetSetting(deployTargetMergeMigrationKey); err != nil {
		return err
	} else if value == "1" {
		return nil
	}

	accounts, err := m.Store.ListDeployAccounts()
	if err != nil {
		return err
	}
	modes := make(map[int64][2]string, len(accounts))
	allMigrated := true
	for _, account := range accounts {
		rule, found := deployTargetMergeRuleFor(account.Type)
		if found && !deployplugin.Has(rule.Canonical) {
			// helper 尚未提供合并目标：保留旧类型，等下次启动再迁移。
			allMigrated = false
			continue
		}
		if key, value, ok := deployTargetModeForAccount(account); ok {
			modes[account.ID] = [2]string{key, value}
		}
	}

	tasks, err := m.Store.ListTasks()
	if err != nil {
		return err
	}
	for i := range tasks {
		task := &tasks[i]
		if !task.HasPipeline() && len(task.DeployIDs) == 0 {
			continue
		}
		pipeline := task.EffectivePipeline()
		changed := !task.HasPipeline()
		for j := range pipeline {
			node := &pipeline[j]
			if node.Type != model.PipelineDeploy || node.Config.DeployAccountID <= 0 {
				continue
			}
			mode, found := modes[node.Config.DeployAccountID]
			if !found {
				continue
			}
			if node.Config.DeployConfig == nil {
				node.Config.DeployConfig = map[string]string{}
			}
			if strings.TrimSpace(node.Config.DeployConfig[mode[0]]) != "" {
				continue
			}
			node.Config.DeployConfig[mode[0]] = mode[1]
			changed = true
		}
		if !changed {
			continue
		}
		task.Pipeline = pipeline
		task.DeployIDs = pipelineDeployIDs(pipeline)
		if err := m.Store.UpdateTask(task); err != nil {
			return err
		}
	}

	for i := range accounts {
		account := &accounts[i]
		rule, found := deployTargetMergeRuleFor(account.Type)
		if !found || !deployplugin.Has(rule.Canonical) {
			continue
		}
		if account.Config == nil {
			account.Config = map[string]string{}
		}
		// 记录原部署对象，供没有显式任务参数的历史调用兜底；保存凭据时会被过滤。
		if strings.TrimSpace(account.Config[rule.ModeKey]) == "" {
			account.Config[rule.ModeKey] = rule.ModeValue
		}
		account.Type = rule.Canonical
		if err := m.Store.UpdateDeployAccount(account); err != nil {
			return err
		}
	}

	if !allMigrated {
		return nil
	}
	return m.Store.SetSetting(deployTargetMergeMigrationKey, "1")
}
