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
	Name         string                 `json:"name"`
	Domains      []string               `json:"domains"`
	Challenge    string                 `json:"challenge"`
	DNSAccountID int64                  `json:"dns_account_id"`
	CA           string                 `json:"ca"`
	CAPoolID     int64                  `json:"ca_pool_id"`
	KeyType      string                 `json:"key_type"`
	AutoRenew    bool                   `json:"auto_renew"`
	RenewDays    int                    `json:"renew_days"`
	DeployIDs    []int64                `json:"deploy_ids"`
	Pipeline     []model.PipelineNode   `json:"pipeline"`
	FlowMode     string                 `json:"flow_mode"`
	TargetFlow   model.TargetFlowConfig `json:"target_flow"`
	Enabled      bool                   `json:"enabled"`
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

// requiresDeployExecution 判断任务流程是否会实际执行部署节点。
// 本地/SSH 部署可能运行服务端命令，因此 API 密钥还需要 deploy:execute 权限。
func requiresDeployExecution(nodes []model.PipelineNode) bool {
	for _, node := range nodes {
		if node.Type == model.PipelineDeploy && node.Enabled {
			return true
		}
	}
	return false
}

func requiresDeployExecutionFor(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
	if flowMode == model.FlowTargetBeta {
		return true
	}
	return requiresDeployExecution(nodes)
}

func (s *Server) taskUsesDeployCommand(flowMode string, nodes []model.PipelineNode, targetFlow model.TargetFlowConfig) bool {
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
		if account, err := s.Store.GetDeployAccount(source.DeployAccountID); err == nil && account != nil {
			if strings.TrimSpace(account.Config["reload_command"]) != "" {
				return true
			}
		}
	}
	return false
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
		if body.CAPoolID == 0 {
			body.CAPoolID = cfg.CAPoolID
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
	body.FlowMode = normalizeFlowMode(body.FlowMode)
	if body.FlowMode != model.FlowStandard && body.FlowMode != model.FlowTargetBeta {
		return i18n.E("task.error.flow_mode_unsupported", "mode", body.FlowMode)
	}
	if body.FlowMode == model.FlowTargetBeta {
		if err := s.normalizeTargetFlow(body); err != nil {
			return err
		}
	} else {
		if len(body.Domains) == 0 {
			return i18n.E("task.error.domains_required")
		}
		for _, d := range body.Domains {
			if !util.ValidDomain(d) {
				return i18n.E("task.error.domain_invalid", "domain", d)
			}
		}
		body.TargetFlow = model.TargetFlowConfig{}
	}
	body.Challenge = strings.TrimSpace(body.Challenge)
	if body.Challenge != "" && body.Challenge != "dns01" {
		return i18n.E("task.error.dns01_only")
	}
	body.Challenge = "dns01"
	if body.CA != "" && body.CAPoolID > 0 {
		return i18n.E("task.error.ca_and_pool_conflict")
	}
	if body.CAPoolID > 0 {
		pool, err := s.Store.GetCAPool(body.CAPoolID)
		if err != nil {
			return i18n.E("task.error.ca_pool_not_found")
		}
		if !pool.Enabled {
			return i18n.E("task.error.ca_pool_disabled")
		}
		if len(pool.EnabledItems()) == 0 {
			return i18n.E("task.error.ca_pool_empty")
		}
	} else if body.CA == "" {
		// 新建任务未指定 CA 时优先使用全局默认调度组；
		// 调度组不可用时退回单一 CA，避免默认配置失效导致任务无法创建。
		defaultPoolID := int64(s.settingInt(service.CAPoolSettingKey, 0))
		if defaultPoolID > 0 {
			if pool, err := s.Store.GetCAPool(defaultPoolID); err == nil && pool.Enabled && len(pool.EnabledItems()) > 0 {
				body.CAPoolID = defaultPoolID
			}
		}
		if body.CAPoolID == 0 {
			body.CA = s.setting("acme.ca", "letsencrypt")
		}
	}
	isPrivateCA := false
	if body.CAPoolID == 0 {
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
	}
	if !isPrivateCA {
		if _, err := s.Store.GetDNSAccount(body.DNSAccountID); err != nil {
			return i18n.E("task.error.dns_account_required")
		}
		if body.CAPoolID == 0 {
			if _, err := s.Manager.ResolveCA(body.CA); err != nil {
				return i18n.E("task.error.ca_unsupported")
			}
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
		if body.FlowMode == model.FlowTargetBeta {
			body.Name = i18n.T(i18n.Normalize(s.setting("language", string(i18n.Default))), "task.target_flow.default_name", nil)
		} else {
			body.Name = body.Domains[0]
		}
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

func normalizeFlowMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", model.FlowStandard:
		return model.FlowStandard
	case model.FlowTargetBeta:
		return model.FlowTargetBeta
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

// normalizeTargetFlow 校验目标流程 Beta 的来源、站点选取和证书分配策略。
func (s *Server) normalizeTargetFlow(body *taskBody) error {
	current := i18n.Normalize(s.setting("language", string(i18n.Default)))
	cfg := &body.TargetFlow
	if body.FlowMode != model.FlowTargetBeta {
		return nil
	}
	if len(cfg.Sources) == 0 {
		return i18n.E("task.error.target_flow_source_required")
	}
	if len(cfg.Sources) > 10 {
		return i18n.E("task.error.target_flow_source_too_many", "max", 10)
	}
	seen := make(map[int64]bool, len(cfg.Sources))
	for index := range cfg.Sources {
		source := &cfg.Sources[index]
		if source.DeployAccountID <= 0 {
			return i18n.E("task.error.target_flow_source_required")
		}
		if seen[source.DeployAccountID] {
			return i18n.E("task.error.target_flow_source_duplicate")
		}
		seen[source.DeployAccountID] = true
		account, err := s.Store.GetDeployAccount(source.DeployAccountID)
		if err != nil {
			return i18n.E("api.error.deploy_account_not_found")
		}
		def, found := service.DeployTypeInfo(account.Type)
		if !found {
			return i18n.E("api.error.deploy_type_unsupported")
		}
		merged := service.MergeDeployConfig(def, account.Config, source.DeployConfig)
		deployCfg := service.FilterDeployConfig(def, merged)
		// 部署参数落库前剔除声明为密钥的字段，认证信息只保留在目标凭据中。
		for _, field := range def.DeployFields {
			if field.Secret {
				delete(deployCfg, field.Key)
			}
		}
		if service.DeployObjectIsSingleton(def, deployCfg) {
			return i18n.E("task.error.target_flow_singleton_target", "name", account.Name)
		}
		fieldKey, _, _, ok := service.DeployObjectFieldInfo(def, deployCfg)
		if !ok {
			return i18n.E("task.error.target_flow_plan_unsupported", "name", account.Name)
		}
		if err := validateFieldsExcept(def.DeployFields, deployCfg, map[string]bool{fieldKey: true}, current); err != nil {
			return err
		}
		// 站点字段由运行时按读取结果填充，任务配置中不能固定某一个站点。
		delete(deployCfg, fieldKey)
		source.DeployConfig = deployCfg
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Selection.Mode))
	if mode == "" {
		mode = model.SelectionAll
	}
	switch mode {
	case model.SelectionAll, model.SelectionSelected, model.SelectionSmart:
	default:
		return i18n.E("task.error.target_flow_selection_invalid")
	}
	cfg.Selection.Mode = mode
	cfg.Selection.IncludeDomains = util.NormalizeDomains(cfg.Selection.IncludeDomains)
	cfg.Selection.ExcludeDomains = util.NormalizeDomains(cfg.Selection.ExcludeDomains)
	keys := make([]string, 0, len(cfg.Selection.Keys))
	seenKeys := make(map[string]bool, len(cfg.Selection.Keys))
	for _, key := range cfg.Selection.Keys {
		key = strings.TrimSpace(key)
		if key == "" || seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		keys = append(keys, key)
	}
	if len(keys) > 1000 {
		return i18n.E("task.error.target_flow_too_many_sites", "max", 1000)
	}
	cfg.Selection.Keys = keys

	strategy := strings.ToLower(strings.TrimSpace(cfg.Policy.Strategy))
	if strategy == "" {
		strategy = model.StrategySmart
	}
	switch strategy {
	case model.StrategySmart, model.StrategyPerSite, model.StrategyPerBaseDomain, model.StrategySingle, model.StrategyPerSource, model.StrategyCustom:
	default:
		return i18n.E("task.error.target_flow_strategy_invalid", "strategy", strategy)
	}
	cfg.Policy.Strategy = strategy
	if cfg.Policy.MaxDomainsPerCert <= 0 {
		cfg.Policy.MaxDomainsPerCert = 100
	}
	if cfg.Policy.MaxDomainsPerCert < 1 || cfg.Policy.MaxDomainsPerCert > 500 {
		return i18n.E("task.error.target_flow_max_domains_invalid")
	}
	if len(cfg.Policy.ExtraDomains) > 200 {
		return i18n.E("task.error.target_flow_too_many_groups", "max", 200)
	}
	for key, domains := range cfg.Policy.ExtraDomains {
		normalized := util.NormalizeDomains(domains)
		for _, domain := range normalized {
			if !util.ValidDomain(domain) {
				return i18n.E("task.error.target_flow_domain_invalid", "domain", domain)
			}
		}
		cfg.Policy.ExtraDomains[key] = normalized
	}
	if len(cfg.Overrides) > 100 {
		return i18n.E("task.error.target_flow_too_many_groups", "max", 100)
	}
	for index := range cfg.Overrides {
		override := &cfg.Overrides[index]
		override.Key = strings.TrimSpace(override.Key)
		override.Label = strings.TrimSpace(override.Label)
		override.Sites = normalizeStringList(override.Sites)
		override.Domains = util.NormalizeDomains(override.Domains)
		for _, domain := range override.Domains {
			if !util.ValidDomain(domain) {
				return i18n.E("task.error.target_flow_domain_invalid", "domain", domain)
			}
		}
	}
	cfg.LastResult = nil
	body.TargetFlow = *cfg
	return nil
}

func validateFieldsExcept(fields []service.FieldDef, config map[string]string, skip map[string]bool, current i18n.Locale) error {
	for _, field := range fields {
		if skip[field.Key] || !field.VisibleIn(fields, config) {
			continue
		}
		if field.Required && strings.TrimSpace(config[field.Key]) == "" {
			return i18n.E("api.error.field_required", "field", field.Localized(current).Label)
		}
	}
	return nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
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
			CAPoolID:     body.CAPoolID,
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
	// 任务流程可以包含本地部署命令与部署路径。
	if err := s.fillTaskBody(&body); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if requiresDeployExecutionFor(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if body.Enabled && !s.requireAPIKeyScope(c, apiScopeTasksRun) {
		return
	}
	if body.Enabled && pipelineUsesPrivateCA(body.Pipeline) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if body.Enabled && s.taskUsesDeployCommand(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	task := &model.Task{
		Name:         body.Name,
		Domains:      body.Domains,
		Challenge:    body.Challenge,
		DNSAccountID: body.DNSAccountID,
		CA:           body.CA,
		CAPoolID:     body.CAPoolID,
		KeyType:      body.KeyType,
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
		s.goSafe("task-created", func() { _ = s.Manager.RunTask(task.ID, "task.trigger.created") })
	}
	ok(c, localizeTasks(c, []model.Task{*task})[0])
}

// nextRunForTask 按任务模式计算下次执行时间。目标流程可能关联多张证书，
// 取最早进入续期窗口的一张；没有证书时立即交给调度器发现站点。
func (s *Server) nextRunForTask(task *model.Task) *time.Time {
	if task == nil || !task.Enabled {
		return nil
	}
	if task.FlowMode == model.FlowTargetBeta {
		if !task.AutoRenew {
			return nil
		}
		certs, err := s.Store.ListTaskCertificates(task.ID)
		if err == nil {
			var earliest *time.Time
			for index := range certs {
				cert := &certs[index]
				if cert.Status != "" && cert.Status != "valid" {
					continue
				}
				next := service.NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, true)
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
		if cert, err := s.Store.GetCertificate(task.CertID); err == nil && task.AutoRenew {
			return service.NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, true)
		}
		return nil
	}
	now := time.Now().UTC()
	return &now
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
	// 修改任务本身不会立即执行；只有把含部署节点的任务设为启用时，
	// 调度器才可能在之后执行部署命令，因此这时才要求 deploy:execute。
	if body.Enabled && requiresDeployExecutionFor(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if body.Enabled && !s.requireAPIKeyScope(c, apiScopeTasksRun) {
		return
	}
	if body.Enabled && pipelineUsesPrivateCA(body.Pipeline) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if body.Enabled && s.taskUsesDeployCommand(body.FlowMode, body.Pipeline, body.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
		return
	}
	task.Name = body.Name
	task.Domains = body.Domains
	task.Challenge = body.Challenge
	task.DNSAccountID = body.DNSAccountID
	task.CA = body.CA
	task.CAPoolID = body.CAPoolID
	task.KeyType = body.KeyType
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

	// 根据启用状态和现有证书重新安排下次执行时间。
	next := s.nextRunForTask(task)
	_ = s.Store.SetTaskNextRun(task.ID, next)

	if task.Enabled && task.CertID == 0 {
		s.goSafe("task-updated", func() { _ = s.Manager.RunTask(task.ID, "task.trigger.updated") })
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
	if pipelineUsesPrivateCA(task.EffectivePipeline()) && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}
	if requiresDeployExecutionFor(task.FlowMode, task.EffectivePipeline(), task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	if s.taskUsesDeployCommand(task.FlowMode, task.EffectivePipeline(), task.TargetFlow) && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
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
	s.goSafe("task-manual", func() {
		_ = s.Manager.RunTaskWithOptions(id, "task.trigger.manual", service.RunOptions{ForceIssue: force})
	})
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
	// API 密钥批量运行前先检查是否包含部署节点或命令，避免在循环中启动
	// 部分任务后才发现缺少 deploy:execute / deploy:command 权限。
	if apiKeyFromContext(c) != nil {
		needsExecute, needsCommand, needsPrivateCA := false, false, false
		for _, id := range ids {
			task, err := s.Store.GetTask(id)
			if err != nil {
				continue
			}
			pipeline := task.EffectivePipeline()
			if requiresDeployExecutionFor(task.FlowMode, pipeline, task.TargetFlow) {
				needsExecute = true
			}
			if s.taskUsesDeployCommand(task.FlowMode, pipeline, task.TargetFlow) {
				needsCommand = true
			}
			if pipelineUsesPrivateCA(pipeline) {
				needsPrivateCA = true
			}
		}
		if needsPrivateCA && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
			return
		}
		if needsExecute && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
			return
		}
		if needsCommand && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
			return
		}
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
		s.goSafe("task-batch", func() {
			_ = s.Manager.RunTaskQueued(taskID, "task.trigger.manual")
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
	// API 密钥重新启用含部署节点的任务会交给调度器执行，因此同样需要
	// deploy:execute / deploy:command；停用任务不会触发部署，不额外限制。
	if body.Enabled && apiKeyFromContext(c) != nil {
		if !s.requireAPIKeyScope(c, apiScopeTasksRun) {
			return
		}
		needsExecute, needsCommand, needsPrivateCA := false, false, false
		for _, id := range ids {
			task, err := s.Store.GetTask(id)
			if err != nil {
				continue
			}
			pipeline := task.EffectivePipeline()
			if requiresDeployExecutionFor(task.FlowMode, pipeline, task.TargetFlow) {
				needsExecute = true
			}
			if s.taskUsesDeployCommand(task.FlowMode, pipeline, task.TargetFlow) {
				needsCommand = true
			}
			if pipelineUsesPrivateCA(pipeline) {
				needsPrivateCA = true
			}
		}
		if needsPrivateCA && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
			return
		}
		if needsExecute && !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
			return
		}
		if needsCommand && !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
			return
		}
	}
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
		copyTask := *task
		copyTask.Enabled = true
		nextRuns[id] = s.nextRunForTask(&copyTask)
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
