package api

import (
	"lactssl/internal/model"
	"lactssl/internal/service"
)

// pipelineUsesPrivateCA 判断任务是否会使用私有 CA 签发证书。
// 私有 CA 的签发能力需要 private_cas:write；tasks:write 本身不应隐含该能力。
func pipelineUsesPrivateCA(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if node.Type != model.PipelineApply || !node.Enabled {
			continue
		}
		if _, ok := service.ParsePrivateCAValue(node.Config.CA); ok {
			return true
		}
	}
	return false
}
