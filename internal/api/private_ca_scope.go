package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

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

// taskCAValues 汇总任务可能使用的 CA 值：顶层 CA、申请节点 CA、调度组候选项。
func (s *Server) taskCAValues(nodes []model.PipelineNode, ca string, poolID int64) []string {
	values := make([]string, 0, len(nodes)+1)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	add(ca)
	for _, node := range nodes {
		if node.Type == model.PipelineApply && node.Enabled {
			add(node.Config.CA)
		}
	}
	if poolID > 0 && s.Store != nil {
		if pool, err := s.Store.GetCAPool(poolID); err == nil && pool != nil {
			for _, item := range pool.EnabledItems() {
				add(item.CAValue)
			}
		}
	}
	return values
}

// taskUsesPrivateCA 在 pipelineUsesPrivateCA 基础上解析 CAPoolID，
// 避免通过包含 private-ca:<id> 的调度组绕过 private_cas:write。
func (s *Server) taskUsesPrivateCA(nodes []model.PipelineNode, poolID int64) bool {
	if pipelineUsesPrivateCA(nodes) {
		return true
	}
	for _, value := range s.taskCAValues(nodes, "", poolID) {
		if _, ok := service.ParsePrivateCAValue(value); ok {
			return true
		}
	}
	return false
}

// requireAPIKeyCARead 允许 ca:read 或更宽的 ca:write 读取/借用上游 CA。
func (s *Server) requireAPIKeyCARead(c *gin.Context) bool {
	key := apiKeyFromContext(c)
	if key == nil || key.HasScope(apiScopeCARead) || key.HasScope(apiScopeCAWrite) {
		return true
	}
	failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeCARead)
	return false
}

// taskUsesCustomCA 判断任务是否会借用自定义/需要 EAB 的上游 CA。
// 这类 CA 使用管理员保存的上游账户身份，任务路径应与 CSR 路径一样要求 ca:read。
func (s *Server) taskUsesCustomCA(nodes []model.PipelineNode, ca string, poolID int64) bool {
	if s.Manager == nil {
		return false
	}
	for _, value := range s.taskCAValues(nodes, ca, poolID) {
		if _, private := service.ParsePrivateCAValue(value); private {
			continue
		}
		def, err := s.Manager.ResolveCA(value)
		if err != nil {
			continue
		}
		if def.Custom || def.EAB {
			return true
		}
	}
	return false
}
