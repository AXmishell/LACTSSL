package service

import (
	"strconv"
	"strings"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/store"
)

// TaskInput 是任务创建/更新用例的输入。
//
// 该结构同时保留 JSON 标签，便于 API 层在第一阶段直接复用；后续可将传输 DTO
// 与领域输入拆开，但业务规范化必须留在 TaskService 内。
type TaskInput struct {
	Name         string           `json:"name"`
	Domains      []string         `json:"domains"`
	Challenge    string           `json:"challenge"`
	DNSAccountID int64            `json:"dns_account_id"`
	DNSRoutes    []model.DNSRoute `json:"dns_routes"`
	// PreservedDNSRoutes 仅供更新任务时沿用已保存的多账户快照，不来自客户端 JSON。
	PreservedDNSRoutes []model.DNSRoute `json:"-"`
	CA                 string                 `json:"ca"`
	CAPoolID           int64                  `json:"ca_pool_id"`
	KeyType            string                 `json:"key_type"`
	NoKeyStorage       bool                   `json:"no_key_storage"`
	AutoRenew          bool                   `json:"auto_renew"`
	RenewDays          int                    `json:"renew_days"`
	DeployIDs          []int64                `json:"deploy_ids"`
	Pipeline           []model.PipelineNode   `json:"pipeline"`
	FlowMode           string                 `json:"flow_mode"`
	TargetFlow         model.TargetFlowConfig `json:"target_flow"`
	Enabled            bool                   `json:"enabled"`
}

// TaskService 负责任务配置的校验、规范化与调度时间计算。
// 任务执行仍由 Manager.RunTaskWithOptions 承担，避免两个组件互相递归。
type TaskService struct {
	store   *store.Store
	manager *Manager
}

// NewTaskService 创建任务配置服务。
func NewTaskService(st *store.Store, manager *Manager) *TaskService {
	return &TaskService{store: st, manager: manager}
}

// NormalizeTask 校验任务配置，并把旧版扁平字段与任务流程节点对齐。
func (s *TaskService) NormalizeTask(body *TaskInput) error {
	return s.normalizeTask(body)
}

// ResolveDNSRoutes 为任务域名生成 DNS 路由。
// 显式账户保持旧语义；未指定账户时按管理员维护的域名绑定逐域名解析。
func (s *TaskService) ResolveDNSRoutes(domains []string, explicitID int64) ([]model.DNSRoute, error) {
	return s.resolveInputDNSRoutes(domains, explicitID)
}

// NormalizeTargetFlow 校验目标流程 Beta 的来源、站点选取和证书分配策略。
func (s *TaskService) NormalizeTargetFlow(body *TaskInput) error {
	return s.normalizeTargetFlow(body)
}

// NextRunForTask 按任务模式计算下次执行时间。
func (s *TaskService) NextRunForTask(task *model.Task) *time.Time {
	if task == nil || !task.Enabled {
		return nil
	}
	if task.Status == "waiting_dns" {
		// 等待手动 DNS 验证期间不参与自动续期调度。
		return nil
	}
	if task.FlowMode == model.FlowTargetBeta {
		if !task.AutoRenew {
			return nil
		}
		certs, err := s.store.ListTaskCertificates(task.ID)
		if err == nil {
			var earliest *time.Time
			for index := range certs {
				cert := &certs[index]
				if cert.Status != "" && cert.Status != "valid" {
					continue
				}
				next := NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, true)
				if next != nil && (earliest == nil || next.Before(*earliest)) {
					earliest = next
				}
			}
			if earliest != nil {
				return earliest
			}
		}
		now := time.Now().UTC()
		return &now
	}
	if task.CertID > 0 {
		if cert, err := s.store.GetCertificate(task.CertID); err == nil && task.AutoRenew {
			return NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, true)
		}
		return nil
	}
	now := time.Now().UTC()
	return &now
}

// TaskBodyDNSAccountIDs 返回本次请求涉及的全部 DNS 账户。
// 目标流程自动路由时使用当前绑定账户集合做密钥范围校验。
func (s *TaskService) TaskBodyDNSAccountIDs(body *TaskInput) []int64 {
	ids := DNSRouteAccountIDs(body.DNSRoutes)
	if len(ids) == 0 && body.DNSAccountID > 0 {
		ids = []int64{body.DNSAccountID}
	}
	if len(ids) > 0 || body.FlowMode != model.FlowTargetBeta {
		return ids
	}
	bindings, err := s.store.ListEnabledDNSZoneBindings()
	if err != nil {
		return nil
	}
	return DNSRouteAccountIDs(bindingsToRoutes(bindings))
}

// TaskDNSAccountIDs 提取已保存任务实际会使用的全部 DNS 账户。
// 目标流程 Beta 自动路由时返回当前全部绑定账户，避免受限密钥在运行时绕过白名单。
func (s *TaskService) TaskDNSAccountIDs(task *model.Task) []int64 {
	if task == nil {
		return nil
	}
	ids := DNSRouteAccountIDs(task.DNSRoutes)
	if len(ids) > 0 {
		return ids
	}
	if task.DNSAccountID > 0 {
		return []int64{task.DNSAccountID}
	}
	if task.FlowMode == model.FlowTargetBeta {
		bindings, err := s.store.ListEnabledDNSZoneBindings()
		if err != nil {
			return nil
		}
		return DNSRouteAccountIDs(bindingsToRoutes(bindings))
	}
	return nil
}

// TaskCAValues 汇总任务可能使用的 CA 值：顶层 CA、申请节点 CA、调度组候选项。
func (s *TaskService) TaskCAValues(nodes []model.PipelineNode, ca string, poolID int64) []string {
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
	if poolID > 0 && s.store != nil {
		if pool, err := s.store.GetCAPool(poolID); err == nil && pool != nil {
			for _, item := range pool.EnabledItems() {
				add(item.CAValue)
			}
		}
	}
	return values
}

// TaskUsesPrivateCA 在申请节点基础上解析 CAPoolID，
// 避免通过包含 private-ca:<id> 的调度组绕过 private_cas:write。
func (s *TaskService) TaskUsesPrivateCA(nodes []model.PipelineNode, poolID int64) bool {
	if pipelineUsesPrivateCA(nodes) {
		return true
	}
	for _, value := range s.TaskCAValues(nodes, "", poolID) {
		if _, ok := ParsePrivateCAValue(value); ok {
			return true
		}
	}
	return false
}

// TaskUsesCustomCA 判断任务是否会借用自定义/需要 EAB 的上游 CA。
// 这类 CA 使用管理员保存的上游账户身份，任务路径应与 CSR 路径一样要求 ca:read。
func (s *TaskService) TaskUsesCustomCA(nodes []model.PipelineNode, ca string, poolID int64) bool {
	if s.manager == nil {
		return false
	}
	for _, value := range s.TaskCAValues(nodes, ca, poolID) {
		if _, private := ParsePrivateCAValue(value); private {
			continue
		}
		def, err := s.manager.ResolveCA(value)
		if err != nil {
			continue
		}
		if def.Custom || def.EAB {
			return true
		}
	}
	return false
}

func pipelineUsesPrivateCA(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if node.Type != model.PipelineApply || !node.Enabled {
			continue
		}
		if _, ok := ParsePrivateCAValue(node.Config.CA); ok {
			return true
		}
	}
	return false
}

// TaskUsesDeployCommand 判断任务是否会执行部署命令。
func (s *TaskService) TaskUsesDeployCommand(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
	if pipelineUsesDeployCommand(nodes) {
		return true
	}
	if flowMode != model.FlowTargetBeta {
		return false
	}
	for _, source := range targetFlow.Sources {
		if strings.TrimSpace(source.DeployConfig["reload_command"]) != "" {
			return true
		}
		if account, err := s.store.GetDeployAccount(source.DeployAccountID); err == nil && account != nil {
			if strings.TrimSpace(account.Config["reload_command"]) != "" {
				return true
			}
		}
	}
	return false
}

func (s *TaskService) setting(key, def string) string {
	value, _ := s.store.GetSetting(key)
	if value == "" {
		return def
	}
	return value
}

func (s *TaskService) settingInt(key string, def int) int {
	value, _ := strconv.Atoi(s.setting(key, ""))
	if value == 0 {
		return def
	}
	return value
}

func bindingsToRoutes(bindings []model.DNSZoneBinding) []model.DNSRoute {
	routes := make([]model.DNSRoute, 0, len(bindings))
	for _, binding := range bindings {
		if !binding.Enabled || binding.DNSAccountID <= 0 {
			continue
		}
		routes = append(routes, model.DNSRoute{DNSAccountID: binding.DNSAccountID})
	}
	return routes
}

func applyNodeConfig(nodes []model.PipelineNode) (model.PipelineConfig, bool) {
	for _, node := range nodes {
		if node.Type == model.PipelineApply {
			return node.Config, true
		}
	}
	return model.PipelineConfig{}, false
}

// RequiresDeployExecution 判断任务流程是否会实际执行部署节点。
func RequiresDeployExecution(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if node.Type == model.PipelineDeploy && node.Enabled {
			return true
		}
	}
	return false
}

// RequiresDeployExecutionFor 判断任务流程是否会实际执行部署节点。
// 目标流程 Beta 总会执行部署阶段。
func RequiresDeployExecutionFor(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
	if flowMode == model.FlowTargetBeta {
		return true
	}
	return RequiresDeployExecution(nodes)
}

func pipelineUsesDeployCommand(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if node.Type == model.PipelineDeploy && node.Enabled && strings.TrimSpace(node.Config.DeployConfig["reload_command"]) != "" {
			return true
		}
	}
	return false
}
