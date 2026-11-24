package api

import (
	"time"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

// taskService 返回任务配置服务。New 会注入实例；这里保留懒初始化以兼容
// 直接构造 Server 的测试与嵌入式调用。
func (s *Server) taskService() *service.TaskService {
	if s.Tasks == nil {
		s.Tasks = service.NewTaskService(s.Store, s.Manager)
	}
	return s.Tasks
}

// 以下方法保留原有 receiver 形态，handler 与测试可以逐步切换到 s.Tasks；
// 任务规范化、DNS 路由解析与调度时间计算已经移入 service.TaskService。
func (s *Server) taskBodyDNSAccountIDs(body *taskBody) []int64 {
	return s.taskService().TaskBodyDNSAccountIDs(body)
}

func (s *Server) taskDNSAccountIDs(task *model.Task) []int64 {
	return s.taskService().TaskDNSAccountIDs(task)
}

func (s *Server) taskUsesDeployCommand(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
	return s.taskService().TaskUsesDeployCommand(flowMode, nodes, targetFlow)
}

func (s *Server) taskCAValues(nodes []model.PipelineNode, ca string, poolID int64) []string {
	return s.taskService().TaskCAValues(nodes, ca, poolID)
}

func (s *Server) taskUsesPrivateCA(nodes []model.PipelineNode, poolID int64) bool {
	return s.taskService().TaskUsesPrivateCA(nodes, poolID)
}

func (s *Server) taskUsesCustomCA(nodes []model.PipelineNode, ca string, poolID int64) bool {
	return s.taskService().TaskUsesCustomCA(nodes, ca, poolID)
}

func (s *Server) fillTaskBody(body *taskBody) error {
	return s.taskService().NormalizeTask(body)
}

func (s *Server) normalizeTargetFlow(body *taskBody) error {
	return s.taskService().NormalizeTargetFlow(body)
}

func (s *Server) nextRunForTask(task *model.Task) *time.Time {
	return s.taskService().NextRunForTask(task)
}

func requiresDeployExecutionFor(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
	return service.RequiresDeployExecutionFor(flowMode, nodes, targetFlow)
}
