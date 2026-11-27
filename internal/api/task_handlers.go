package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/service"
	"lactssl/internal/util"
)

// taskBody 是任务接口的传输结构。业务规范化已收口到 service.TaskService，
// 这里保留别名以兼容现有 handler 与测试。
type taskBody = service.TaskInput

// applyNodeConfig 返回任务流程中申请证书节点的配置。
func applyNodeConfig(nodes []model.PipelineNode) (model.PipelineConfig, bool) {
	for _, node := range nodes {
		if node.Type == model.PipelineApply {
			return node.Config, true
		}
	}
	return model.PipelineConfig{}, false
}

// taskBodyDNSAccountID 提取请求实际引用的 DNS 账户。
// 顶层字段为空时兼容只提交任务流程的客户端，从申请节点读取。
func taskBodyDNSAccountID(body *taskBody) int64 {
	if body.DNSAccountID > 0 {
		return body.DNSAccountID
	}
	if cfg, found := applyNodeConfig(body.Pipeline); found {
		return cfg.DNSAccountID
	}
	return 0
}

// taskUsesDNSAccount 判断任务是否引用指定 DNS 账户。
func taskUsesDNSAccount(task *model.Task, id int64) bool {
	if task == nil || id <= 0 {
		return false
	}
	if task.DNSAccountID == id {
		return true
	}
	for _, route := range task.DNSRoutes {
		if route.DNSAccountID == id {
			return true
		}
	}
	return false
}

// sameDomainSet 比较两组域名在规范化后是否完全一致（顺序无关）。
func sameDomainSet(left, right []string) bool {
	leftSet := make(map[string]bool)
	for _, domain := range util.NormalizeDomains(left) {
		leftSet[domain] = true
	}
	rightSet := make(map[string]bool)
	for _, domain := range util.NormalizeDomains(right) {
		rightSet[domain] = true
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for domain := range leftSet {
		if !rightSet[domain] {
			return false
		}
	}
	return true
}

func (s *Server) listTasks(c *gin.Context) {
	list, err := s.Store.ListTasks()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, localizeTasks(c, list))
}

func (s *Server) createTask(c *gin.Context) {
	var body taskBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	// 任务流程可以包含本地部署命令与部署路径。
	// 在 fillTaskBody 校验账户存在性之前先做授权，避免受限密钥通过
	// “账户不存在/无权使用”的不同响应枚举管理员保存的账户。
	if !s.requireAPIKeyTaskDNSAccess(c, []int64{taskBodyDNSAccountID(&body)}) {
		return
	}
	if err := s.fillTaskBody(&body); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if !s.requireAPIKeyTaskDNSAccess(c, s.taskBodyDNSAccountIDs(&body)) {
		return
	}
	if requiresDeployExecutionFor(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if body.Enabled && !s.requireAPIKeyScope(c, apiScopeTasksRun) {
		return
	}
	// 授权检查与 Enabled 解耦：只要流程包含私有 CA、custom/EAB CA 或命令，
	// 无论任务当前是否启用，都必须具备对应 scope，避免先以禁用状态写入、
	// 再通过竞态或管理员启用触发执行。
	if s.taskUsesPrivateCA(body.Pipeline, body.CAPoolID) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if s.taskUsesCustomCA(body.Pipeline, body.CA, body.CAPoolID) && !s.requireAPIKeyCARead(c) {
		return
	}
	if s.taskUsesDeployCommand(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	task := &model.Task{
		Name:         body.Name,
		Domains:      body.Domains,
		Challenge:    body.Challenge,
		DNSAccountID: body.DNSAccountID,
		DNSRoutes:    body.DNSRoutes,
		CA:           body.CA,
		CAPoolID:     body.CAPoolID,
		KeyType:      body.KeyType,
		NoKeyStorage: body.NoKeyStorage,
		AutoRenew:    body.AutoRenew,
		RenewDays:    body.RenewDays,
		DeployIDs:    body.DeployIDs,
		Pipeline:     body.Pipeline,
		FlowMode:     body.FlowMode,
		TargetFlow:   body.TargetFlow,
		Enabled:      body.Enabled,
		Status:       "pending",
		Message:      i18n.Encode("task.status.pending", nil),
	}
	if err := s.Store.CreateTask(task); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("task", task.ID, "info", "log.task.created", map[string]any{"name": task.Name})
	// 新建任务立即在后台执行一次签发；暂停状态的任务只保存，不触发签发和部署。
	if task.Enabled {
		snapshot := *task
		s.goSafe("task-created", func() {
			_ = s.Manager.RunTaskWithOptions(task.ID, "task.trigger.created", service.RunOptions{Task: &snapshot})
		})
	}
	ok(c, localizeTasks(c, []model.Task{*task})[0])
}

// nextRunForTask 按任务模式计算下次执行时间。目标流程可能关联多张证书，
// 取最早进入续期窗口的一张；没有证书时立即交给调度器发现站点。

func (s *Server) updateTask(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	// 与批量启用/停用共用一把锁，保证授权检查与写库之间任务不会被另一请求改写。
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	task, err := s.Store.GetTask(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.task_not_found")
		return
	}
	var body taskBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireAPIKeyTaskDNSAccess(c, []int64{taskBodyDNSAccountID(&body)}) {
		return
	}
	// 域名未变化的更新（例如暂停/启用）沿用已保存的多账户快照，
	// 避免绑定关系后来调整导致任务被静默改路由。
	if taskBodyDNSAccountID(&body) == 0 && len(task.DNSRoutes) > 0 && sameDomainSet(body.Domains, task.Domains) {
		body.PreservedDNSRoutes = task.DNSRoutes
	}
	if err := s.fillTaskBody(&body); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if !s.requireAPIKeyTaskDNSAccess(c, s.taskBodyDNSAccountIDs(&body)) {
		return
	}
	// 修改任务本身不会立即执行，但禁用任务仍可被手动/批量运行，且后台执行
	// 会重新读取任务。因此能力检查必须与 Enabled 解耦：只要流程包含部署、
	// 命令或私有/custom CA，就必须具备对应 scope。
	if requiresDeployExecutionFor(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if body.Enabled && !s.requireAPIKeyScope(c, apiScopeTasksRun) {
		return
	}
	if s.taskUsesPrivateCA(body.Pipeline, body.CAPoolID) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if s.taskUsesCustomCA(body.Pipeline, body.CA, body.CAPoolID) && !s.requireAPIKeyCARead(c) {
		return
	}
	if s.taskUsesDeployCommand(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	prevEnabled := task.Enabled
	prevAutoRenew := task.AutoRenew
	wasRateLimited := task.Status == "rate_limited"
	task.Name = body.Name
	task.Domains = body.Domains
	task.Challenge = body.Challenge
	task.DNSAccountID = body.DNSAccountID
	task.DNSRoutes = body.DNSRoutes
	task.CA = body.CA
	task.CAPoolID = body.CAPoolID
	task.KeyType = body.KeyType
	task.NoKeyStorage = body.NoKeyStorage
	task.AutoRenew = body.AutoRenew
	task.RenewDays = body.RenewDays
	task.DeployIDs = body.DeployIDs
	task.Pipeline = body.Pipeline
	task.FlowMode = body.FlowMode
	task.TargetFlow = body.TargetFlow
	task.Enabled = body.Enabled
	if err := s.Store.UpdateTask(task); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	resetWaiting := false
	if strings.TrimSpace(task.CA) != "zotrus" || task.CAPoolID != 0 {
		// 任务已不再使用零信，丢弃旧的待验证订单，避免残留私钥与记录。
		_ = s.Manager.ResetZotrusOrder(task.ID)
		resetWaiting = true
	} else if s.Manager.ResetStaleZotrusOrder(task) {
		// 域名或私钥类型变化后旧订单已失效。
		resetWaiting = true
	}
	if resetWaiting && task.Status == "waiting_dns" {
		// 任务回到待执行状态，让用户可以直接重新运行并生成新的验证记录。
		_ = s.Store.UpdateTaskResult(task.ID, "pending", i18n.Encode("task.status.pending", nil), task.LastRunAt, nil, task.CertID)
		task.Status = "pending"
		task.Message = i18n.Encode("task.status.pending", nil)
	}

	// 限流等待依赖自动重试：停止自动续期后“将于某时重试”的承诺已失效，
	// 恢复自动重试时旧的重试时间也不再可信，两种情况都回到待执行状态。
	stopRateLimitWait := wasRateLimited && (!task.Enabled || !task.AutoRenew)
	resumeRateLimitWait := wasRateLimited && task.Enabled && task.AutoRenew && (!prevEnabled || !prevAutoRenew)
	resetRateLimitWait := stopRateLimitWait || resumeRateLimitWait
	if resetRateLimitWait {
		_ = s.Store.UpdateTaskResult(task.ID, "pending", i18n.Encode("task.status.pending", nil), task.LastRunAt, nil, task.CertID)
		task.Status = "pending"
		task.Message = i18n.Encode("task.status.pending", nil)
		task.NextRunAt = nil
	}

	// 根据启用状态和现有证书重新安排下次执行时间。
	var next *time.Time
	switch {
	case stopRateLimitWait:
		// 已停止自动续期，不再安排自动重试。
	case wasRateLimited && !resetRateLimitWait && task.NextRunAt != nil:
		// 仍在等待 CA 配额恢复，保留原重试时间，避免普通编辑把等待推迟到续期窗口。
		next = task.NextRunAt
	default:
		next = s.nextRunForTask(task)
	}
	_ = s.Store.SetTaskNextRun(task.ID, next)

	if task.Enabled && task.CertID == 0 && !stopRateLimitWait && task.Status != "rate_limited" {
		snapshot := *task
		s.goSafe("task-updated", func() {
			_ = s.Manager.RunTaskWithOptions(task.ID, "task.trigger.updated", service.RunOptions{Task: &snapshot})
		})
	}
	task.Message = localize(c, task.Message)
	ok(c, task)
}

func (s *Server) deleteTask(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if err := s.Store.DeleteTask(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("task", id, "warning", "log.task.deleted", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func (s *Server) runTask(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	task, err := s.Store.GetTask(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.task_not_found")
		return
	}
	if task.Status == "running" {
		failKey(c, http.StatusBadRequest, "api.error.task_running")
		return
	}
	pipeline := task.EffectivePipeline()
	if !s.requireAPIKeyTaskDNSAccess(c, s.taskDNSAccountIDs(task)) {
		return
	}
	if s.taskUsesPrivateCA(pipeline, task.CAPoolID) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if s.taskUsesCustomCA(pipeline, task.CA, task.CAPoolID) && !s.requireAPIKeyCARead(c) {
		return
	}
	if requiresDeployExecutionFor(task.FlowMode, pipeline, task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if s.taskUsesDeployCommand(task.FlowMode, pipeline, task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	force := c.Query("force") == "1" || strings.EqualFold(c.Query("force"), "true")
	if !force && c.Request.ContentLength > 0 {
		var body struct {
			Force bool `json:"force"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			failKey(c, http.StatusBadRequest, "api.error.invalid_params")
			return
		}
		force = body.Force
	}
	snapshot := *task
	s.goSafe("task-manual", func() {
		_ = s.Manager.RunTaskWithOptions(id, "task.trigger.manual", service.RunOptions{ForceIssue: force, Task: &snapshot})
	})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.task_started", nil)})
}

// maxBatchTasks 限制单次批量操作的任务数量，避免一次请求触发过多签发。
const maxBatchTasks = 100

type taskBatchBody struct {
	IDs     []int64 `json:"ids"`
	Enabled bool    `json:"enabled"`
}

// normalizeBatchIDs 过滤非法 ID、去重并限制批量操作规模。
// requiredKey 在过滤后为空时返回，tooLargeKey 在超过 max 时返回。
func normalizeBatchIDs(ids []int64, max int, requiredKey, tooLargeKey string) ([]int64, error) {
	if len(ids) == 0 {
		return nil, i18n.E(requiredKey)
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, i18n.E(requiredKey)
	}
	if len(out) > max {
		return nil, i18n.E(tooLargeKey, "max", max)
	}
	return out, nil
}

// normalizeBatchTaskIDs 去重并校验批量任务 ID。
func normalizeBatchTaskIDs(ids []int64) ([]int64, error) {
	return normalizeBatchIDs(ids, maxBatchTasks, "api.error.task_ids_required", "api.error.task_batch_too_large")
}

// requireBatchTaskAPIKeyPermissions 在批量操作前一次性检查 API 密钥权限，
// 避免循环执行到一半才发现缺少 deploy:execute、deploy:command 或 CA 权限。
// requireRunScope 仅在会把任务交给调度器执行时置为 true。
func (s *Server) requireBatchTaskAPIKeyPermissions(c *gin.Context, ids []int64, requireRunScope bool) bool {
	if apiKeyFromContext(c) == nil {
		return true
	}
	if requireRunScope && !s.requireAPIKeyScope(c, apiScopeTasksRun) {
		return false
	}
	needsExecute, needsCommand, needsPrivateCA, needsCARead := false, false, false, false
	for _, id := range ids {
		task, err := s.Store.GetTask(id)
		if err != nil {
			continue
		}
		if !s.requireAPIKeyTaskDNSAccess(c, s.taskDNSAccountIDs(task)) {
			return false
		}
		pipeline := task.EffectivePipeline()
		if requiresDeployExecutionFor(task.FlowMode, pipeline, task.TargetFlow) {
			needsExecute = true
		}
		if s.taskUsesDeployCommand(task.FlowMode, pipeline, task.TargetFlow) {
			needsCommand = true
		}
		if s.taskUsesPrivateCA(pipeline, task.CAPoolID) {
			needsPrivateCA = true
		}
		if s.taskUsesCustomCA(pipeline, task.CA, task.CAPoolID) {
			needsCARead = true
		}
	}
	if needsPrivateCA && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return false
	}
	if needsCARead && !s.requireAPIKeyCARead(c) {
		return false
	}
	if needsExecute && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return false
	}
	if needsCommand && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return false
	}
	return true
}

// batchRunTasks 批量执行任务，跳过正在执行或已不存在的任务。
func (s *Server) batchRunTasks(c *gin.Context) {
	var body taskBatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ids, err := normalizeBatchTaskIDs(body.IDs)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	// API 密钥批量运行前先检查是否包含部署节点或命令，避免在循环中启动
	// 部分任务后才发现缺少 deploy:execute / deploy:command 权限。
	if !s.requireBatchTaskAPIKeyPermissions(c, ids, false) {
		return
	}
	started := 0
	skippedIDs := make([]int64, 0)
	for _, id := range ids {
		task, err := s.Store.GetTask(id)
		if err != nil || task.Status == "running" {
			skippedIDs = append(skippedIDs, id)
			continue
		}
		started++
		taskID := id
		snapshot := *task
		s.goSafe("task-batch", func() {
			_ = s.Manager.RunTaskQueued(taskID, "task.trigger.manual", service.RunOptions{Task: &snapshot})
		})
	}
	message := i18n.T(locale(c), "api.ok.tasks_started", map[string]any{
		"started": started,
		"skipped": len(skippedIDs),
	})
	ok(c, gin.H{
		"started":     started,
		"skipped":     len(skippedIDs),
		"skipped_ids": skippedIDs,
		"message":     message,
	})
}

// batchSetTasksEnabled 批量暂停或恢复任务的自动续期，并重新安排执行时间。
func (s *Server) batchSetTasksEnabled(c *gin.Context) {
	var body taskBatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ids, err := normalizeBatchTaskIDs(body.IDs)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	// 与 updateTask 共用锁，避免检查通过后任务被改成含命令/自定义 CA 的流程。
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	// API 密钥重新启用含部署节点的任务会交给调度器执行，因此同样需要
	// deploy:execute / deploy:command；停用任务不会触发部署，不额外限制。
	if body.Enabled && !s.requireBatchTaskAPIKeyPermissions(c, ids, true) {
		return
	}
	validIDs := make([]int64, 0, len(ids))
	skippedIDs := make([]int64, 0)
	rateLimitedTasks := make([]*model.Task, 0)
	nextRuns := make(map[int64]*time.Time, len(ids))
	for _, id := range ids {
		task, err := s.Store.GetTask(id)
		if err != nil {
			skippedIDs = append(skippedIDs, id)
			continue
		}
		validIDs = append(validIDs, id)
		if task.Status == "rate_limited" {
			rateLimitedTasks = append(rateLimitedTasks, task)
		}
		if !body.Enabled {
			nextRuns[id] = nil
			continue
		}
		copyTask := *task
		copyTask.Enabled = true
		nextRuns[id] = s.nextRunForTask(&copyTask)
	}
	updated, err := s.Store.SetTasksEnabled(validIDs, body.Enabled, nextRuns)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	// 暂停/恢复后自动重试条件已改变，清除旧的限流等待状态，
	// 避免列表继续展示已经失效的重试时间。
	for _, task := range rateLimitedTasks {
		_ = s.Store.UpdateTaskResult(task.ID, "pending", i18n.Encode("task.status.pending", nil), task.LastRunAt, nextRuns[task.ID], task.CertID)
	}
	key := "api.ok.tasks_paused"
	if body.Enabled {
		key = "api.ok.tasks_resumed"
	}
	message := i18n.T(locale(c), key, map[string]any{"count": updated})
	ok(c, gin.H{
		"updated":     updated,
		"skipped":     len(skippedIDs),
		"skipped_ids": skippedIDs,
		"message":     message,
	})
}

func (s *Server) taskLogs(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	taskLogs, _ := s.Store.ListLogsByRef("task", id, 300)
	deployLogs, _ := s.Store.ListLogsByRef("deploy", id, 300)
	all := append(taskLogs, deployLogs...)
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if len(all) > 400 {
		all = all[:400]
	}
	ok(c, localizeLogs(c, all))
}

// clearTaskLogs 清理指定任务的执行日志（任务日志与部署日志）。
// 清理属于破坏性操作，单独限流；审计记录写入 settings 来源，避免再次出现在任务日志中。
func (s *Server) clearTaskLogs(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.TaskLogClear, "tasks:logs:clear:"+strconv.FormatInt(user.ID, 10)); !allowed {
		rateLimited(c, wait)
		return
	}
	task, err := s.Store.GetTask(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.task_not_found")
		return
	}
	deleted, err := s.Store.DeleteTaskLogs(id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if deleted > 0 {
		s.Store.AddLogKey("settings", 0, "warning", "log.settings.task_logs_cleared", map[string]any{
			"count":   deleted,
			"name":    task.Name,
			"task_id": id,
		})
	}
	ok(c, gin.H{"deleted": deleted})
}
