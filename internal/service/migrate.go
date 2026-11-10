package service

import "lactssl/internal/model"

const deployConfigSplitMigrationKey = "migration.deploy_config_split"

// MigrateDeployConfigSplit 把旧版目标凭据中的部署参数迁移到任务流程部署节点。
//
// 迁移顺序很重要：先把路径、站点、域名等参数复制到任务节点并保存，再清理凭据配置，
// 这样旧任务在升级后仍能继续部署。函数可重复执行，迁移标记只在全部成功后写入。
func (m *Manager) MigrateDeployConfigSplit() error {
	if value, err := m.Store.GetSetting(deployConfigSplitMigrationKey); err != nil {
		return err
	} else if value == "1" {
		return nil
	}

	accounts, err := m.Store.ListDeployAccounts()
	if err != nil {
		return err
	}
	byID := make(map[int64]model.DeployAccount, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
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
			if len(node.Config.DeployConfig) > 0 {
				continue
			}
			account, ok := byID[node.Config.DeployAccountID]
			if !ok {
				continue
			}
			def, found := DeployTypeInfo(account.Type)
			if !found {
				continue
			}
			config := ExtractDeployConfig(def, account.Config)
			if len(config) > 0 {
				node.Config.DeployConfig = config
				changed = true
			}
		}
		ids := pipelineDeployIDs(pipeline)
		if !sameIDs(task.DeployIDs, ids) {
			changed = true
		}
		if !changed {
			continue
		}
		task.Pipeline = pipeline
		task.DeployIDs = ids
		if err := m.Store.UpdateTask(task); err != nil {
			return err
		}
	}

	for i := range accounts {
		account := &accounts[i]
		def, found := DeployTypeInfo(account.Type)
		if !found {
			continue
		}
		// 配置为空且该类型需要认证字段时，可能是解密失败或数据异常，跳过以免误清空。
		if len(account.Config) == 0 && len(def.Fields) > 0 {
			continue
		}
		auth := FilterAuthConfig(def, account.Config)
		account.Config = auth
		if err := m.Store.UpdateDeployAccount(account); err != nil {
			return err
		}
	}

	return m.Store.SetSetting(deployConfigSplitMigrationKey, "1")
}

func pipelineDeployIDs(nodes []model.PipelineNode) []int64 {
	ids := make([]int64, 0)
	for _, node := range nodes {
		if node.Type == model.PipelineDeploy && node.Enabled && node.Config.DeployAccountID > 0 {
			ids = append(ids, node.Config.DeployAccountID)
		}
	}
	return ids
}

func sameIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
