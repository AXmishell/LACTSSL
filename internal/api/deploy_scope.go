package api

import (
	"strings"

	"lactssl/internal/model"
)

// deployNodeUsesCommand 判断部署节点是否会执行本地/SSH reload 命令。
// 命令执行与“写证书文件”是两种风险等级不同的能力，API 密钥必须显式拥有
// deploy:command 才能触发。
func deployNodeUsesCommand(node model.PipelineNode) bool {
	if node.Type != model.PipelineDeploy || !node.Enabled {
		return false
	}
	return strings.TrimSpace(node.Config.DeployConfig["reload_command"]) != ""
}

// pipelineUsesDeployCommand 判断任务流程中是否存在会执行命令的部署节点。
func pipelineUsesDeployCommand(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if deployNodeUsesCommand(node) {
			return true
		}
	}
	return false
}

// manualDeployUsesCommand 在真正执行任何目标之前，判断本次手动部署是否
// 会使用 reload 命令。命令可能来自请求、证书所属任务的部署节点，或旧版
// 凭据中的兼容字段，这里统一做保守检查，避免执行到一半才发现缺少权限。
func (s *Server) manualDeployUsesCommand(targets []deployTargetBody, taskConfigs map[int64]map[string]string) bool {
	for _, target := range targets {
		if strings.TrimSpace(target.DeployConfig["reload_command"]) != "" {
			return true
		}
		if cfg := taskConfigs[target.DeployAccountID]; strings.TrimSpace(cfg["reload_command"]) != "" {
			return true
		}
		if account, err := s.Store.GetDeployAccount(target.DeployAccountID); err == nil && account != nil {
			if strings.TrimSpace(account.Config["reload_command"]) != "" {
				return true
			}
		}
	}
	return false
}
