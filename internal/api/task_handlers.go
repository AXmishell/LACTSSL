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
	"lactssl/internal/service"
	"lactssl/internal/util"
)

type taskBody struct {
	Name         string               `json:"name"`
	Domains      []string             `json:"domains"`
	Challenge    string               `json:"challenge"`
	DNSAccountID int64                `json:"dns_account_id"`
	CA           string               `json:"ca"`
	KeyType      string               `json:"key_type"`
	AutoRenew    bool                 `json:"auto_renew"`
	RenewDays    int                  `json:"renew_days"`
	DeployIDs    []int64              `json:"deploy_ids"`
	Pipeline     []model.PipelineNode `json:"pipeline"`
	Enabled      bool                 `json:"enabled"`
}

// applyNodeConfig 返回任务流程中申请证书节点的配置。
func applyNodeConfig(nodes []model.PipelineNode) (model.PipelineConfig, bool) {
	for _, node := range nodes {
		if node.Type == model.PipelineApply {
			return node.Config, true
		}
	}
	return model.PipelineConfig{}, false
}

// fillTaskBody 校验任务配置，并把旧版扁平字段与任务流程节点对齐。
func (s *Server) fillTaskBody(body *taskBody) error {
	// 兼容只提交任务流程的客户端：用申请节点补全顶层字段。
	if cfg, found := applyNodeConfig(body.Pipeline); found {
		if len(body.Domains) == 0 {
			body.Domains = cfg.Domains
		}
		if body.DNSAccountID == 0 {
			body.DNSAccountID = cfg.DNSAccountID
		}
		if body.CA == "" {
			body.CA = cfg.CA
		}
		if body.KeyType == "" {
			body.KeyType = cfg.KeyType
		}
		if body.RenewDays <= 0 {
			body.RenewDays = cfg.RenewDays
		}
		if cfg.AutoRenew {
			body.AutoRenew = true
		}
	}
	body.Domains = util.NormalizeDomains(body.Domains)
	if len(body.Domains) == 0 {
		return i18n.E("task.error.domains_required")
	}
	for _, d := range body.Domains {
		if !util.ValidDomain(d) {
			return i18n.E("task.error.domain_invalid", "domain", d)
		}
	}
	body.Challenge = strings.TrimSpace(body.Challenge)
	if body.Challenge != "" && body.Challenge != "dns01" {
		return i18n.E("task.error.dns01_only")
	}
	body.Challenge = "dns01"
	if body.CA == "" {
		body.CA = s.setting("acme.ca", "letsencrypt")
	}
	isPrivateCA := false
	if caID, ok := service.ParsePrivateCAValue(body.CA); ok {
		ca, err := s.Store.GetPrivateCA(caID)
		if err != nil {
			return i18n.E("task.error.private_ca_not_found")
		}
		if time.Now().After(ca.NotAfter) {
			return i18n.E("task.error.private_ca_expired")
		}
		isPrivateCA = true
	}
	if !isPrivateCA {
		if _, err := s.Store.GetDNSAccount(body.DNSAccountID); err != nil {
			return i18n.E("task.error.dns_account_required")
		}
		if _, found := service.CAInfo(body.CA); !found {
			return i18n.E("task.error.ca_unsupported")
		}
	}
	if body.KeyType == "" {
		body.KeyType = s.setting("acme.key_type", "ecdsa")
	}
	switch body.KeyType {
	case "ecdsa", "ecdsa384", "rsa2048", "rsa4096":
	default:
		return i18n.E("task.error.key_type_unsupported")
	}
	if body.RenewDays <= 0 {
		body.RenewDays = s.settingInt("acme.renew_days", 30)
	}
	if body.RenewDays < 1 || body.RenewDays > 60 {
		return i18n.E("task.error.renew_days_range")
	}
	if body.Name = strings.TrimSpace(body.Name); body.Name == "" {
		body.Name = body.Domains[0]
	}
	for _, id := range body.DeployIDs {
		if _, err := s.Store.GetDeployAccount(id); err != nil {
			return i18n.E("task.error.deploy_target_missing")
		}
	}
	pipeline, err := s.normalizePipeline(body)
	if err != nil {
		return err
	}
	body.Pipeline = pipeline
	body.DeployIDs = make([]int64, 0)
	for _, node := range pipeline {
		if node.Type == model.PipelineDeploy && node.Enabled {
			body.DeployIDs = append(body.DeployIDs, node.Config.DeployAccountID)
		}
	}
	return nil
}

// normalizePipeline 校验任务流程节点并生成兼容旧字段的默认任务流程。
func (s *Server) normalizePipeline(body *taskBody) ([]model.PipelineNode, error) {
	current := i18n.Normalize(s.setting("language", string(i18n.Default)))
	nodes := make([]model.PipelineNode, 0, len(body.Pipeline)+2)
	seenApply := false
	deploySeq, notifySeq := 0, 0
	usedIDs := map[string]bool{}
	pickID := func(current, fallback string) string {
		id := strings.TrimSpace(current)
		if id == "" {
			id = fallback
		}
		base := id
		for suffix := 2; usedIDs[id]; suffix++ {
			id = base + "-" + strconv.Itoa(suffix)
		}
		usedIDs[id] = true
		return id
	}

	// 默认节点名留空，前端会按节点类型显示当前语言。
	applyNode := model.PipelineNode{
		ID:      "apply",
		Type:    model.PipelineApply,
		Enabled: true,
		Config: model.PipelineConfig{
			Domains:      body.Domains,
			DNSAccountID: body.DNSAccountID,
			CA:           body.CA,
			KeyType:      body.KeyType,
			AutoRenew:    body.AutoRenew,
			RenewDays:    body.RenewDays,
		},
	}

	for index, node := range body.Pipeline {
		node.Type = strings.ToLower(strings.TrimSpace(node.Type))
		switch node.Type {
		case model.PipelineApply:
			if seenApply {
				return nil, i18n.E("task.error.apply_node_single")
			}
			if index != 0 {
				return nil, i18n.E("task.error.apply_node_first")
			}
			seenApply = true
			applyNode.ID = pickID(node.ID, "apply")
			if node.Name = strings.TrimSpace(node.Name); node.Name != "" {
				applyNode.Name = node.Name
			}
			// 申请节点的配置以顶层任务字段为准。
			nodes = append(nodes, applyNode)
		case model.PipelineDeploy:
			if err := s.normalizeDeployNode(&node, current); err != nil {
				return nil, err
			}
			deploySeq++
			node.ID = pickID(node.ID, "deploy-"+strconv.Itoa(deploySeq))
			node.Name = strings.TrimSpace(node.Name)
			nodes = append(nodes, node)
		case model.PipelineNotify:
			notifySeq++
			node.ID = pickID(node.ID, "notify-"+strconv.Itoa(notifySeq))
			node.Name = strings.TrimSpace(node.Name)
			// 通知渠道由任务自行选择，保留旧字段用于兼容历史任务。
			node.Config.NotifyChannel = model.NotifyAllChannels
			channels := make([]string, 0, len(node.Config.NotifyChannels))
			seenChannels := make(map[string]bool, len(node.Config.NotifyChannels))
			for _, channel := range node.Config.NotifyChannels {
				name := strings.TrimSpace(channel)
				if name == "" || seenChannels[name] || !service.IsNotifyChannel(name) {
					continue
				}
				seenChannels[name] = true
				channels = append(channels, name)
			}
			node.Config.NotifyChannels = channels
			switch node.Config.NotifyOn {
			case "", model.NotifyAlways:
				node.Config.NotifyOn = model.NotifyAlways
			case model.NotifySuccess, model.NotifyFailure:
			default:
				return nil, i18n.E("task.error.notify_when_invalid")
			}
			node.Config.Subject = strings.TrimSpace(node.Config.Subject)
			if len([]rune(node.Config.Subject)) > 80 {
				return nil, i18n.E("task.error.subject_too_long")
			}
			nodes = append(nodes, node)
		default:
			return nil, i18n.E("task.error.node_type_unsupported", "type", node.Type)
		}
	}

	if !seenApply {
		nodes = append([]model.PipelineNode{applyNode}, nodes...)
	}
	if len(body.Pipeline) == 0 {
		// 兼容旧版客户端：未提供任务流程时沿用「部署后通知」的行为。
		for i, id := range body.DeployIDs {
			node := model.PipelineNode{
				ID:      "deploy-" + strconv.Itoa(i+1),
				Type:    model.PipelineDeploy,
				Enabled: true,
				Config:  model.PipelineConfig{DeployAccountID: id},
			}
			if err := s.normalizeDeployNode(&node, current); err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
		}
		nodes = append(nodes, model.PipelineNode{
			ID:      "notify-1",
			Type:    model.PipelineNotify,
			Enabled: true,
			Config:  model.PipelineConfig{NotifyChannel: model.NotifyAllChannels, NotifyOn: model.NotifyAlways},
		})
	}
	return nodes, nil
}

// normalizeDeployNode 校验部署节点并整理部署参数。
func (s *Server) normalizeDeployNode(node *model.PipelineNode, current i18n.Locale) error {
	if node.Config.DeployAccountID <= 0 {
		if node.Enabled {
			return i18n.E("task.error.deploy_node_no_target")
		}
		return nil
	}
	account, err := s.Store.GetDeployAccount(node.Config.DeployAccountID)
	if err != nil {
		return i18n.E("task.error.deploy_target_missing")
	}
	def, found := service.DeployTypeInfo(account.Type)
	if !found {
		return i18n.E("task.error.deploy_target_missing")
	}
	deployCfg := service.FilterDeployConfig(def, node.Config.DeployConfig)
	// 兼容旧客户端与旧数据：部署节点未提供参数时，从凭据历史配置中补全。
	if len(deployCfg) == 0 {
		deployCfg = service.ExtractDeployConfig(def, account.Config)
	}
	node.Config.DeployConfig = deployCfg
	if node.Enabled {
		if err := validateFields(def.DeployFields, deployCfg, current); err != nil {
			return err
		}
	}
	return nil
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
	if err := s.fillTaskBody(&body); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	task := &model.Task{
		Name:         body.Name,
		Domains:      body.Domains,
		Challenge:    body.Challenge,
		DNSAccountID: body.DNSAccountID,
		CA:           body.CA,
		KeyType:      body.KeyType,
		AutoRenew:    body.AutoRenew,
		RenewDays:    body.RenewDays,
		DeployIDs:    body.DeployIDs,
		Pipeline:     body.Pipeline,
		Enabled:      body.Enabled,
		Status:       "pending",
		Message:      i18n.Encode("task.status.pending", nil),
	}
	if err := s.Store.CreateTask(task); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("task", task.ID, "info", "log.task.created", map[string]any{"name": task.Name})
	// 新建任务立即在后台执行一次签发。
	go func() { _ = s.Manager.RunTask(task.ID, "task.trigger.created") }()
	ok(c, localizeTasks(c, []model.Task{*task})[0])
}

func (s *Server) updateTask(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
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
	if err := s.fillTaskBody(&body); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	task.Name = body.Name
	task.Domains = body.Domains
	task.Challenge = body.Challenge
	task.DNSAccountID = body.DNSAccountID
	task.CA = body.CA
	task.KeyType = body.KeyType
	task.AutoRenew = body.AutoRenew
	task.RenewDays = body.RenewDays
	task.DeployIDs = body.DeployIDs
	task.Pipeline = body.Pipeline
	task.Enabled = body.Enabled
	if err := s.Store.UpdateTask(task); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	// 根据启用状态和现有证书重新安排下次执行时间。
	var next *time.Time
	if task.Enabled {
		if task.CertID > 0 {
			if cert, err := s.Store.GetCertificate(task.CertID); err == nil && task.AutoRenew {
				next = service.NextRenewTime(cert.NotAfter, task.RenewDays, true)
			} else if !task.AutoRenew {
				next = nil
			}
		} else {
			now := time.Now().UTC()
			next = &now
		}
	}
	_ = s.Store.SetTaskNextRun(task.ID, next)

	if task.Enabled && task.CertID == 0 {
		go func() { _ = s.Manager.RunTask(task.ID, "task.trigger.updated") }()
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
	go func() { _ = s.Manager.RunTask(id, "task.trigger.manual") }()
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.task_started", nil)})
}

// maxBatchTasks 限制单次批量操作的任务数量，避免一次请求触发过多签发。
const maxBatchTasks = 100

type taskBatchBody struct {
	IDs     []int64 `json:"ids"`
	Enabled bool    `json:"enabled"`
}

// normalizeBatchTaskIDs 去重并校验批量任务 ID。
func normalizeBatchTaskIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, i18n.E("api.error.task_ids_required")
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
		return nil, i18n.E("api.error.task_ids_required")
	}
	if len(out) > maxBatchTasks {
		return nil, i18n.E("api.error.task_batch_too_large", "max", maxBatchTasks)
	}
	return out, nil
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
	started := 0
	skippedIDs := make([]int64, 0)
	for _, id := range ids {
		task, err := s.Store.GetTask(id)
		if err != nil || task.Status == "running" {
			skippedIDs = append(skippedIDs, id)
			continue
		}
		started++
		go func(taskID int64) {
			_ = s.Manager.RunTaskQueued(taskID, "task.trigger.manual")
		}(id)
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
	now := time.Now().UTC()
	validIDs := make([]int64, 0, len(ids))
	skippedIDs := make([]int64, 0)
	nextRuns := make(map[int64]*time.Time, len(ids))
	for _, id := range ids {
		task, err := s.Store.GetTask(id)
		if err != nil {
			skippedIDs = append(skippedIDs, id)
			continue
		}
		validIDs = append(validIDs, id)
		if !body.Enabled {
			nextRuns[id] = nil
			continue
		}
		if task.CertID > 0 {
			// 已有证书时按到期时间重排续期；未开启自动续期则保持未安排。
			if cert, certErr := s.Store.GetCertificate(task.CertID); certErr == nil && task.AutoRenew {
				nextRuns[id] = service.NextRenewTime(cert.NotAfter, task.RenewDays, true)
				continue
			}
			nextRuns[id] = nil
			continue
		}
		// 尚无证书的任务交给调度器拾取，避免批量恢复时瞬间发起大量签发。
		next := now
		nextRuns[id] = &next
	}
	updated, err := s.Store.SetTasksEnabled(validIDs, body.Enabled, nextRuns)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
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
