package service

import (
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func (s *TaskService) normalizeTask(body *TaskInput) error {
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
		if cfg.NoKeyStorage {
			body.NoKeyStorage = true
		}
		if body.RenewDays <= 0 {
			body.RenewDays = cfg.RenewDays
		}
		if cfg.AutoRenew {
			body.AutoRenew = true
		}
	}
	body.Domains = util.NormalizeDomains(body.Domains)
	// 路由必须由服务端按绑定解析，忽略客户端提交的快照，避免绕过账户白名单。
	preservedRoutes := body.PreservedDNSRoutes
	body.DNSRoutes = nil
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
		pool, err := s.store.GetCAPool(body.CAPoolID)
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
		defaultPoolID := int64(s.settingInt(CAPoolSettingKey, 0))
		if defaultPoolID > 0 {
			if pool, err := s.store.GetCAPool(defaultPoolID); err == nil && pool.Enabled && len(pool.EnabledItems()) > 0 {
				body.CAPoolID = defaultPoolID
			}
		}
		if body.CAPoolID == 0 {
			body.CA = s.setting("acme.ca", "letsencrypt")
		}
	}
	isPrivateCA := false
	if body.CAPoolID == 0 {
		if caID, ok := ParsePrivateCAValue(body.CA); ok {
			ca, err := s.store.GetPrivateCA(caID)
			if err != nil {
				return i18n.E("task.error.private_ca_not_found")
			}
			if time.Now().After(ca.NotAfter) {
				return i18n.E("task.error.private_ca_expired")
			}
			isPrivateCA = true
		}
	}
	isZotrusCA := !isPrivateCA && body.CAPoolID == 0 && strings.TrimSpace(body.CA) == "zotrus"
	if isZotrusCA && body.FlowMode == model.FlowTargetBeta {
		return i18n.E("zotrus.beta_unsupported")
	}
	if isZotrusCA {
		// 零信使用手动 DNS，不保存 DNS 账户或路由快照。
		body.DNSAccountID = 0
		body.DNSRoutes = nil
		for _, domain := range body.Domains {
			if strings.HasPrefix(strings.TrimSpace(domain), "*.") {
				return i18n.E("zotrus.wildcard_unsupported")
			}
		}
	}
	if !isPrivateCA {
		if !isZotrusCA {
			if body.FlowMode == model.FlowTargetBeta && len(body.Domains) == 0 {
				// 目标流程的域名在运行时由站点发现结果决定，保存时只校验显式账户。
				if body.DNSAccountID > 0 {
					if _, err := s.store.GetDNSAccount(body.DNSAccountID); err != nil {
						return i18n.E("task.error.dns_account_required")
					}
				}
			} else {
				routes := preservedRoutes
				if body.DNSAccountID > 0 || !DNSRoutesCoverDomains(routes, body.Domains) {
					var err error
					routes, err = s.resolveInputDNSRoutes(body.Domains, body.DNSAccountID)
					if err != nil {
						return err
					}
				}
				body.DNSRoutes = routes
				accountIDs := DNSRouteAccountIDs(routes)
				if len(accountIDs) == 1 {
					body.DNSAccountID = accountIDs[0]
				} else {
					body.DNSAccountID = 0
				}
				for _, accountID := range accountIDs {
					if _, err := s.store.GetDNSAccount(accountID); err != nil {
						return i18n.E("task.error.dns_account_required")
					}
				}
			}
		}
		if body.CAPoolID == 0 {
			if _, err := s.manager.ResolveCA(body.CA); err != nil {
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
		if _, err := s.store.GetDeployAccount(id); err != nil {
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

// resolveTaskDNSRoutes 为任务域名生成 DNS 路由。
// 显式账户保持旧语义；未指定账户时按管理员维护的域名绑定逐域名解析。
func (s *TaskService) resolveInputDNSRoutes(domains []string, explicitID int64) ([]model.DNSRoute, error) {
	if explicitID > 0 {
		return ExplicitDNSRoutesForDomains(domains, explicitID)
	}
	bindings, err := s.store.ListEnabledDNSZoneBindings()
	if err != nil {
		return nil, err
	}
	return ResolveDNSRoutesForDomains(domains, bindings)
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
func (s *TaskService) normalizeTargetFlow(body *TaskInput) error {
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
		account, err := s.store.GetDeployAccount(source.DeployAccountID)
		if err != nil {
			return i18n.E("api.error.deploy_account_not_found")
		}
		def, found := DeployTypeInfo(account.Type)
		if !found {
			return i18n.E("api.error.deploy_type_unsupported")
		}
		merged := MergeDeployConfig(def, account.Config, source.DeployConfig)
		deployCfg := FilterDeployConfig(def, merged)
		// 部署参数落库前剔除声明为密钥的字段，认证信息只保留在目标凭据中。
		for _, field := range def.DeployFields {
			if field.Secret {
				delete(deployCfg, field.Key)
			}
		}
		if DeployObjectIsSingleton(def, deployCfg) {
			return i18n.E("task.error.target_flow_singleton_target", "name", account.Name)
		}
		fieldKey, _, _, ok := DeployObjectFieldInfo(def, deployCfg)
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

func validateFields(fields []FieldDef, config map[string]string, current i18n.Locale) error {
	for _, field := range fields {
		if !field.VisibleIn(fields, config) {
			continue
		}
		if field.Required && strings.TrimSpace(config[field.Key]) == "" {
			return i18n.E("api.error.field_required", "field", field.Localized(current).Label)
		}
	}
	return nil
}

func validateFieldsExcept(fields []FieldDef, config map[string]string, skip map[string]bool, current i18n.Locale) error {
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
func (s *TaskService) normalizePipeline(body *TaskInput) ([]model.PipelineNode, error) {
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
			NoKeyStorage: body.NoKeyStorage,
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
				if name == "" || seenChannels[name] || !IsNotifyChannel(name) {
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
func (s *TaskService) normalizeDeployNode(node *model.PipelineNode, current i18n.Locale) error {
	if node.Config.DeployAccountID <= 0 {
		if node.Enabled {
			return i18n.E("task.error.deploy_node_no_target")
		}
		return nil
	}
	account, err := s.store.GetDeployAccount(node.Config.DeployAccountID)
	if err != nil {
		return i18n.E("task.error.deploy_target_missing")
	}
	def, found := DeployTypeInfo(account.Type)
	if !found {
		return i18n.E("task.error.deploy_target_missing")
	}
	deployCfg := FilterDeployConfig(def, node.Config.DeployConfig)
	// 兼容旧客户端与旧数据：部署节点未提供参数时，从凭据历史配置中补全。
	if len(deployCfg) == 0 {
		deployCfg = ExtractDeployConfig(def, account.Config)
	}
	node.Config.DeployConfig = deployCfg
	if node.Enabled {
		if err := validateFields(def.DeployFields, deployCfg, current); err != nil {
			return err
		}
	}
	return nil
}
