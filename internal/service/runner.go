package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// RunOptions 控制一次任务执行时的签发行为。
type RunOptions struct {
	// ForceIssue 强制重新签发，即使已经存在仍在有效期内的证书。
	ForceIssue bool
	// Task 是调用方已完成授权检查的任务快照。非空时 RunTask 直接执行快照，
	// 不再按 ID 重新读取，避免“检查时任务 A、执行时任务 B”的 TOCTOU。
	// 调度器等内部触发不传快照，仍读取最新任务。
	Task *model.Task
}

// RunTask 执行一次证书申请或续期任务。trigger 是 task.trigger.* 翻译 key。
func (m *Manager) RunTask(taskID int64, trigger string) error {
	return m.RunTaskWithOptions(taskID, trigger, RunOptions{})
}

// RunTaskWithOptions 按指定选项执行任务。
func (m *Manager) RunTaskWithOptions(taskID int64, trigger string, opts RunOptions) error {
	if _, loaded := m.running.LoadOrStore(taskID, true); loaded {
		return i18n.E("task.error.running")
	}
	defer m.running.Delete(taskID)

	task := opts.Task
	if task == nil {
		loaded, err := m.Store.GetTask(taskID)
		if err != nil {
			return i18n.E("task.error.not_found", "error", err.Error())
		}
		task = loaded
	} else if task.ID != taskID {
		return i18n.E("task.error.not_found", "error", "task snapshot id mismatch")
	}
	pipeline := task.EffectivePipeline()
	current := m.Locale()
	triggerText := i18n.T(current, trigger, nil)

	start := time.Now().UTC()
	if err := m.Store.UpdateTaskResult(task.ID, "running", i18n.Encode("task.status.running", map[string]any{"trigger": triggerText}), &start, nil, task.CertID); err != nil {
		return i18n.E("task.error.update_status_failed", "error", err.Error())
	}
	m.Store.AddLogKey("task", task.ID, "info", "log.task.started", map[string]any{"name": task.Name, "domains": strings.Join(task.Domains, ", ")})
	logger().Info("任务开始", "task_id", task.ID, "trigger", trigger, "domains", strings.Join(task.Domains, ","))

	err := m.executeTask(task, pipeline, opts)
	if err != nil {
		// 限流不是普通失败：记录恢复时间并推迟任务，避免 6 小时后再次撞墙。
		var limited *RateLimitError
		if errors.As(err, &limited) {
			if handleErr := m.handleRateLimited(task, pipeline, limited, &start); handleErr != nil {
				return handleErr
			}
			return nil
		}
		logger().Error("任务失败", "task_id", task.ID, "error", SafeError(err))
		message := i18n.EncodeError(err)
		if len(message) > 800 {
			message = truncate(i18n.ErrorText(current, err), 800)
		}
		next := (*time.Time)(nil)
		if task.AutoRenew && task.Enabled {
			t := time.Now().UTC().Add(6 * time.Hour)
			next = &t
		}
		_ = m.Store.UpdateTaskResult(task.ID, "failed", message, &start, next, task.CertID)
		m.Store.AddLogKey("task", task.ID, "error", "log.task.failed", map[string]any{"message": i18n.ErrorText(current, err)})
		m.notifyTaskResult(task, pipeline, nil, "failed", message)
		return err
	}
	return nil
}

// RunTaskQueued 以受限并发执行任务，供批量触发使用，避免同时发起过多签发。
func (m *Manager) RunTaskQueued(taskID int64, trigger string, opts RunOptions) error {
	// 排队期间也做去重，避免连续两次批量执行让同一任务重复签发。
	if _, loaded := m.queued.LoadOrStore(taskID, true); loaded {
		return i18n.E("task.error.running")
	}
	defer m.queued.Delete(taskID)
	if m.batchSem != nil {
		m.batchSem <- struct{}{}
		defer func() { <-m.batchSem }()
	}
	return m.RunTaskWithOptions(taskID, trigger, opts)
}

// executeTask 按任务流程顺序执行申请、部署与通知模块。
func (m *Manager) executeTask(task *model.Task, pipeline []model.PipelineNode, opts RunOptions) error {
	if task.FlowMode == model.FlowTargetBeta {
		return m.executeTargetFlow(task, pipeline, opts)
	}

	applyNode, found := findPipelineNode(pipeline, model.PipelineApply)
	if !found {
		return i18n.E("task.error.apply_node_missing")
	}

	cert, issued, err := m.runApplyNode(task, applyNode, opts)
	if err != nil {
		return err
	}

	deployErrors, deployedCount := m.runDeployNodes(task, cert, pipeline)
	if applyNode.Config.NoKeyStorage || task.NoKeyStorage {
		// 部署已经完成，及时丢弃内存中的明文私钥，缩短密钥驻留时间。
		cert.KeyPEM = ""
	}

	status := "success"
	message := i18n.Encode("task.message.success", nil)
	if len(deployErrors) > 0 {
		status = "partial"
		key := "task.message.partial"
		if !issued {
			key = "task.message.reused_partial"
		}
		message = i18n.Encode(key, map[string]any{"message": truncate(strings.Join(deployErrors, "; "), 600)})
	} else if deployedCount > 0 {
		key := "task.message.deployed"
		if !issued {
			key = "task.message.reused_deployed"
		}
		message = i18n.Encode(key, map[string]any{"count": deployedCount})
	} else if !issued {
		message = i18n.Encode("task.message.reused", nil)
	}

	next := NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, task.AutoRenew && task.Enabled)
	now := time.Now().UTC()
	if err := m.Store.UpdateTaskResult(task.ID, status, message, &now, next, cert.ID); err != nil {
		return i18n.E("task.error.update_status_failed", "error", err.Error())
	}
	logger().Info("任务完成", "task_id", task.ID, "status", status, "issued", issued, "deployed", deployedCount)
	m.notifyTaskResult(task, pipeline, cert, status, message)
	return nil
}

func findPipelineNode(nodes []model.PipelineNode, nodeType string) (model.PipelineNode, bool) {
	for _, node := range nodes {
		if node.Type == nodeType && node.Enabled {
			return node, true
		}
	}
	return model.PipelineNode{}, false
}

// runApplyNode 执行申请证书模块。返回的 bool 表示本次是否真的签发了新证书。
func (m *Manager) runApplyNode(task *model.Task, node model.PipelineNode, opts RunOptions) (*model.Certificate, bool, error) {
	return m.runApplyForDomains(task, node, opts, nil)
}

// runApplyForDomains 与 runApplyNode 相同，但允许目标流程 Beta 为每个证书组
// 传入独立的域名集合。nil 表示沿用节点或任务上的域名配置。
func (m *Manager) runApplyForDomains(task *model.Task, node model.PipelineNode, opts RunOptions, override []string) (*model.Certificate, bool, error) {
	cfg := node.Config
	domains := util.NormalizeDomains(override)
	if len(domains) == 0 {
		domains = util.NormalizeDomains(cfg.Domains)
	}
	if len(domains) == 0 {
		domains = util.NormalizeDomains(task.Domains)
	}
	if len(domains) == 0 {
		return nil, false, i18n.E("task.error.domains_empty")
	}
	for _, d := range domains {
		if !util.ValidDomain(d) {
			return nil, false, i18n.E("task.error.domain_invalid", "domain", d)
		}
	}

	caValue := cfg.CA
	if caValue == "" {
		caValue = task.CA
	}
	if caValue == "" {
		caValue, _ = m.Store.GetSetting("acme.ca")
	}
	if caValue == "" {
		caValue = "letsencrypt"
	}
	poolID := m.CAPoolIDForTask(task, cfg)
	_, isPrivateCA := ParsePrivateCAValue(caValue)

	// 私有 CA 不参与公共 ACME 调度组；显式配置的私有 CA 优先。
	var candidates []CACandidate
	var err error
	if !isPrivateCA && poolID > 0 {
		// 先只读加载候选用于证书复用判断；真正签发前再推进轮询游标。
		candidates, err = m.SelectCACandidates(poolID, false)
		if err != nil {
			return nil, false, err
		}
	}

	keyType := cfg.KeyType
	if keyType == "" {
		keyType = task.KeyType
	}
	if keyType == "" {
		keyType, _ = m.Store.GetSetting("acme.key_type")
	}
	if keyType == "" {
		keyType = "ecdsa"
	}
	noKeyStorage := cfg.NoKeyStorage || task.NoKeyStorage

	nodeName := node.Name
	if nodeName == "" {
		nodeName = i18n.T(m.Locale(), "task.node.apply", nil)
	}

	// 证书仍在有效期内且不处于续期窗口时，复用已有证书，只执行后续部署节点。
	// 手动执行、批量执行、重复任务都会经过这里，避免无意中消耗 CA 配额。
	allowedCAs := map[string]bool{}
	if isPrivateCA || len(candidates) == 0 {
		allowedCAs[caValue] = true
	} else {
		for _, candidate := range candidates {
			allowedCAs[candidate.Value] = true
		}
	}
	// 不存储私钥时不能复用历史证书：部署需要私钥，而复用不会重新生成，
	// 因此每次执行都重新签发并在本次运行内临时使用私钥。
	if !opts.ForceIssue && !noKeyStorage {
		if cert, ok := m.findReusableCertificate(task, caValue, allowedCAs, keyType, domains); ok {
			m.Store.AddLogKey("task", task.ID, "info", "log.task.reused", map[string]any{
				"time": cert.NotAfter.Local().Format("2006-01-02 15:04:05"),
			})
			return cert, false, nil
		}
	} else if noKeyStorage {
		m.Store.AddLogKey("task", task.ID, "info", "log.task.no_key_storage", nil)
	} else {
		m.Store.AddLogKey("task", task.ID, "info", "log.task.force_issue", nil)
	}
	if !isPrivateCA && poolID > 0 {
		// 确认需要签发后再推进游标，确保证书复用不会打乱轮询顺序。
		candidates, err = m.SelectCACandidates(poolID, true)
		if err != nil {
			return nil, false, err
		}
	}

	var result *ACMEResult
	usedCA := caValue
	usedLabel := caValue
	if caID, isPrivate := ParsePrivateCAValue(caValue); isPrivate {
		ca, caErr := m.Store.GetPrivateCA(caID)
		if caErr != nil {
			return nil, false, i18n.E("task.error.private_ca_not_found")
		}
		if time.Now().After(ca.NotAfter) {
			return nil, false, i18n.E("task.error.private_ca_expired")
		}
		m.Store.AddLogKey("task", task.ID, "info", "log.task.applying_private", map[string]any{"node": nodeName, "ca": ca.Name})
		result, err = IssuePrivateCertificate(ca, PrivateCertRequest{Domains: domains, KeyType: keyType, ValidDays: 365})
		if err != nil {
			return nil, false, err
		}
		result.FullchainPEM = result.CertPEM + m.privateCAChain(ca)
	} else {
		email, _ := m.Store.GetSetting("acme.email")
		if strings.TrimSpace(email) == "" {
			return nil, false, i18n.E("task.error.acme_email_required")
		}

		dnsRoutes, err := m.resolveTaskDNSRoutes(task, cfg, domains)
		if err != nil {
			return nil, false, err
		}
		if len(dnsRoutes) == 0 {
			return nil, false, i18n.E("task.error.dns_account_required")
		}

		if len(candidates) == 0 {
			label := caValue
			if ca, resolveErr := m.ResolveCA(caValue); resolveErr == nil {
				if localized := ca.Localized(m.Locale()).Label; localized != "" {
					label = localized
				}
			}
			candidates = []CACandidate{{Value: caValue, Label: label}}
		}
		attempts := make([]CAFailure, 0, len(candidates))
		issued := false
		for index, candidate := range candidates {
			eabKid, eabHMAC := m.loadCAEABSettings(candidate.Value)
			m.Store.AddLogKey("task", task.ID, "info", "log.task.applying_ca", map[string]any{"node": nodeName, "ca": candidate.Label})
			current, obtainErr := m.Obtain(ACMERequest{
				Domains:   domains,
				DNSRoutes: dnsRoutes,
				CA:        candidate.Value,
				KeyType:   keyType,
				Email:     email,
				EABKid:    eabKid,
				EABHMAC:   eabHMAC,
			})
			if obtainErr == nil {
				result = current
				usedCA = candidate.Value
				usedLabel = candidate.Label
				issued = true
				break
			}
			if !shouldFailoverCA(obtainErr) {
				return nil, false, obtainErr
			}
			attempts = append(attempts, CAFailure{CA: candidate.Value, Label: candidate.Label, Err: obtainErr})
			if index < len(candidates)-1 {
				m.Store.AddLogKey("task", task.ID, "warning", "log.task.ca_failover", map[string]any{
					"from":  candidate.Label,
					"to":    candidates[index+1].Label,
					"error": i18n.ErrorText(m.Locale(), obtainErr),
				})
			}
		}
		if !issued {
			// 单 CA 任务保持原有错误形态，避免把普通签发失败包装成调度组错误。
			if poolID == 0 && len(attempts) == 1 {
				return nil, false, attempts[0].Err
			}
			return nil, false, newCAPoolError(poolID, m.CAPoolName(poolID), attempts)
		}
		if len(candidates) > 1 {
			m.Store.AddLogKey("task", task.ID, "info", "log.task.ca_used", map[string]any{"ca": usedLabel})
		}
	}
	m.Store.AddLogKey("task", task.ID, "success", "log.task.issued", map[string]any{"time": result.NotAfter.Local().Format("2006-01-02 15:04:05")})

	storedKeyPEM := result.KeyPEM
	if noKeyStorage {
		// 私钥不落库：数据库中只保存证书链和元数据。
		storedKeyPEM = ""
	}
	cert := &model.Certificate{
		TaskID:        task.ID,
		Domains:       domains,
		PrimaryDomain: domains[0],
		Issuer:        result.Issuer,
		CA:            usedCA,
		CertPEM:       result.CertPEM,
		KeyPEM:        storedKeyPEM,
		FullchainPEM:  result.FullchainPEM,
		Serial:        result.Serial,
		NotBefore:     result.NotBefore,
		NotAfter:      result.NotAfter,
		Status:        "valid",
	}
	if err := m.Store.CreateCertificate(cert); err != nil {
		return nil, false, i18n.E("task.error.save_cert_failed", "error", err.Error())
	}
	if noKeyStorage {
		// 落库后再把私钥挂到内存对象上，仅供本次任务流程的部署节点使用。
		// 该对象不会再次写库；任务结束后私钥随对象回收。
		cert.KeyPEM = result.KeyPEM
	}
	return cert, true, nil
}

// findReusableCertificate 查找可以复用的有效证书。
// 优先当前任务的证书，其次是同一 CA 下标识符集合完全相同的证书，
// 这样重复创建的任务也不会反复向上游 CA 下单。
func (m *Manager) findReusableCertificate(task *model.Task, caValue string, allowedCAs map[string]bool, keyType string, domains []string) (*model.Certificate, bool) {
	seen := map[int64]bool{}
	if cert, err := m.Store.LatestTaskCertificate(task.ID); err == nil && cert != nil {
		seen[cert.ID] = true
		if certificateReusable(cert, task.RenewDays, caValue, allowedCAs, keyType, domains) {
			return cert, true
		}
	}
	// 调度组可能包含多个 CA，需要同时列出组内所有 CA 的证书。
	cas := make([]string, 0, len(allowedCAs))
	for ca := range allowedCAs {
		cas = append(cas, ca)
	}
	certs, err := m.Store.ListReusableCertificatesForCAs(cas)
	if err != nil {
		return nil, false
	}
	for i := range certs {
		cert := &certs[i]
		if seen[cert.ID] {
			continue
		}
		if certificateReusable(cert, task.RenewDays, caValue, allowedCAs, keyType, domains) {
			return cert, true
		}
	}
	return nil, false
}

// certificateReusable 判断证书是否可以安全复用：
// 域名集合完全一致、CA 仍属于当前候选、私钥类型一致、未过期且尚未进入续期窗口。
func certificateReusable(cert *model.Certificate, renewDays int, caValue string, allowedCAs map[string]bool, keyType string, domains []string) bool {
	if cert == nil || cert.ID == 0 {
		return false
	}
	if cert.Status != "" && cert.Status != "valid" {
		return false
	}
	if cert.RevokedAt != nil {
		return false
	}
	now := time.Now().UTC()
	if !cert.NotAfter.After(now) {
		return false
	}
	if cert.NotBefore.After(now) {
		return false
	}
	if len(allowedCAs) > 0 {
		if !allowedCAs[cert.CA] {
			return false
		}
	} else if cert.CA != caValue {
		return false
	}
	if !sameDomainSet(cert.Domains, domains) {
		return false
	}
	if strings.TrimSpace(cert.KeyPEM) == "" {
		return false
	}
	if !certificateKeyTypeMatches(cert.KeyPEM, keyType) {
		return false
	}
	if renewalDue(cert.NotBefore, cert.NotAfter, renewDays) {
		return false
	}
	// 数据库记录可能损坏或被手工修改，复用前再校验证书本身覆盖域名且私钥匹配。
	if err := validateACMEResult(domains, &ACMEResult{CertPEM: cert.CertPEM, KeyPEM: cert.KeyPEM}); err != nil {
		return false
	}
	return true
}

// renewalDue 判断证书是否进入续期窗口。
// 使用 NotBefore..NotAfter 的原始寿命计算，避免按“剩余寿命”回推导致
// 短寿命证书永远不进入续期窗口。提前量超过寿命时与 NextRenewTime 一致，
// 改为在寿命的 2/3 处续期。
func renewalDue(notBefore, notAfter time.Time, renewDays int) bool {
	now := time.Now().UTC()
	if !notAfter.After(now) {
		return true
	}
	if renewDays <= 0 {
		renewDays = 30
	}
	lifetime := notAfter.Sub(notBefore)
	if lifetime <= 0 {
		lifetime = notAfter.Sub(now)
	}
	margin := time.Duration(renewDays) * 24 * time.Hour
	if margin >= lifetime {
		margin = lifetime / 3
	}
	return !now.Before(notAfter.Add(-margin))
}

// sameDomainSet 比较两个域名集合是否完全一致，忽略大小写、顺序和末尾的点。
func sameDomainSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, domain := range left {
		counts[normalizeDomain(domain)]++
	}
	for _, domain := range right {
		key := normalizeDomain(domain)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

// certificateKeyTypeMatches 校验已有私钥类型是否与任务当前配置一致。
func certificateKeyTypeMatches(keyPEM, keyType string) bool {
	key, err := certcrypto.ParsePEMPrivateKey([]byte(keyPEM))
	if err != nil {
		return false
	}
	want := certificateKeyType(keyType)
	switch k := key.(type) {
	case *rsa.PrivateKey:
		switch want {
		case certcrypto.RSA2048:
			return k.N.BitLen() == 2048
		case certcrypto.RSA4096:
			return k.N.BitLen() == 4096
		}
	case *ecdsa.PrivateKey:
		switch want {
		case certcrypto.EC256:
			return k.Curve == elliptic.P256()
		case certcrypto.EC384:
			return k.Curve == elliptic.P384()
		}
	}
	return false
}

// handleRateLimited 把限流错误转换为可展示状态，并把下次执行安排到 CA 给出的恢复时间。
func (m *Manager) handleRateLimited(task *model.Task, pipeline []model.PipelineNode, rateErr *RateLimitError, start *time.Time) error {
	now := time.Now().UTC()
	retryAt := rateErr.RetryAt.UTC()
	if !retryAt.After(now) {
		retryAt = now.Add(time.Hour)
	}
	// 留出一点边界余量，避免刚好卡在 CA 计算的重置时刻。
	retryAt = retryAt.Add(time.Minute)

	existing := m.existingTaskCertificate(task)
	params := map[string]any{"time": retryAt.Local().Format("2006-01-02 15:04:05")}
	messageKey := "task.message.rate_limited"
	if existing != nil && existing.NotAfter.After(now) {
		messageKey = "task.message.rate_limited_existing"
		params["expires"] = existing.NotAfter.Local().Format("2006-01-02 15:04:05")
	}
	message := i18n.Encode(messageKey, params)

	var next *time.Time
	if task.AutoRenew && task.Enabled {
		next = &retryAt
	}
	certID := task.CertID
	if existing != nil {
		certID = existing.ID
	}
	if err := m.Store.UpdateTaskResult(task.ID, "rate_limited", message, start, next, certID); err != nil {
		return i18n.E("task.error.update_status_failed", "error", err.Error())
	}
	m.Store.AddLogKey("task", task.ID, "warning", "log.task.rate_limited", map[string]any{
		"time":  params["time"],
		"error": rateErr.Detail,
	})
	logger().Warn("任务因 CA 限流推迟", "task_id", task.ID, "retry_at", retryAt, "error", SafeError(rateErr))
	m.notifyTaskResult(task, pipeline, existing, "rate_limited", message)
	return nil
}

// existingTaskCertificate 返回任务当前可用的证书，用于限流提示中说明现有证书仍有效。
func (m *Manager) existingTaskCertificate(task *model.Task) *model.Certificate {
	if task.CertID > 0 {
		if cert, err := m.Store.GetCertificate(task.CertID); err == nil && cert != nil {
			return cert
		}
	}
	if cert, err := m.Store.LatestTaskCertificate(task.ID); err == nil {
		return cert
	}
	return nil
}

// runDeployNodes 依次执行任务流程中的部署模块，单个失败不会影响证书本身。
func (m *Manager) runDeployNodes(task *model.Task, cert *model.Certificate, pipeline []model.PipelineNode) ([]string, int) {
	deployErrors := []string{}
	deployed := 0
	for _, node := range pipeline {
		if node.Type != model.PipelineDeploy || !node.Enabled {
			continue
		}
		accountID := node.Config.DeployAccountID
		account, err := m.Store.GetDeployAccount(accountID)
		if err != nil {
			deployErrors = append(deployErrors, i18n.Tf(m.Locale(), "deploy.error.config_missing_id", "id", accountID))
			m.Store.AddLogKey("deploy", task.ID, "error", "log.deploy.config_missing", map[string]any{"node": nodeName(node, m.Locale())})
			continue
		}
		ok, deployErr := m.deployOne(task, cert, account, node.Config.DeployConfig, nodeName(node, m.Locale()))
		if ok {
			deployed++
		} else {
			deployErrors = append(deployErrors, account.Name+": "+truncate(i18n.ErrorText(m.Locale(), deployErr), 200))
		}
	}
	return deployErrors, deployed
}

// deployOne 执行一次部署并写入部署记录。普通流程与目标流程 Beta 共用，
// 保证部署记录、日志和失败处理口径一致。
func (m *Manager) deployOne(task *model.Task, cert *model.Certificate, account *model.DeployAccount, deployCfg map[string]string, displayName string) (bool, error) {
	record := &model.DeployRecord{
		CertID:          cert.ID,
		DeployAccountID: account.ID,
		AccountName:     account.Name,
		TaskID:          task.ID,
		Status:          "success",
	}
	err := m.Deploy(cert, account, deployCfg)
	if err != nil {
		record.Status = "failed"
		record.Message = truncate(i18n.EncodeError(err), 800)
		m.Store.AddLogKey("deploy", task.ID, "error", "log.deploy.failed", map[string]any{"node": displayName, "account": account.Name, "message": i18n.ErrorText(m.Locale(), err)})
		logger().Warn("部署失败", "task_id", task.ID, "cert_id", cert.ID, "account", account.Name, "node", displayName, "error", SafeError(err))
	} else {
		record.Message = i18n.Encode("deploy.success", nil)
		m.Store.AddLogKey("deploy", task.ID, "success", "log.deploy.success", map[string]any{"node": displayName, "account": account.Name, "type": account.Type})
	}
	_ = m.Store.CreateDeployRecord(record)
	return err == nil, err
}

// executeTargetFlow 执行目标流程 Beta：
// 读取站点 → 按策略分组 → 逐组申请/复用证书 → 逐站点部署 → 汇总结果。
func (m *Manager) executeTargetFlow(task *model.Task, pipeline []model.PipelineNode, opts RunOptions) error {
	applyNode, found := findPipelineNode(pipeline, model.PipelineApply)
	if !found {
		return i18n.E("task.error.apply_node_missing")
	}
	cfg := task.TargetFlow
	if len(cfg.Sources) == 0 {
		return i18n.E("task.error.target_flow_source_required")
	}
	locale := m.Locale()
	ctx := context.Background()
	discovery, err := m.DiscoverTargetSites(ctx, cfg.Sources, string(locale))
	if err != nil {
		return err
	}
	publicCA := applyNodeUsesPublicCA(task, applyNode)
	applyTargetSelection(discovery.Sites, cfg.Selection, publicCA, locale)
	applyDomainFilters(discovery.Sites, cfg.Selection, locale, &discovery.Warnings)
	recommendation := RecommendCertStrategy(discovery.Sites, cfg.Policy.MaxDomainsPerCert, publicCA, locale)
	policy := cfg.Policy
	if policy.Strategy == "" || policy.Strategy == model.StrategySmart {
		policy.Strategy = recommendation.Strategy
	}
	groups, buildWarnings, err := BuildCertGroups(discovery.Sites, policy, cfg.Overrides, publicCA, locale)
	if err != nil {
		return err
	}
	discovery.Warnings = append(discovery.Warnings, buildWarnings...)
	m.assignReusableCertificates(groups, task.RenewDays, m.targetFlowAllowedCAs(task, applyNode), applyNode.Config.NoKeyStorage || task.NoKeyStorage)

	m.Store.AddLogKey("task", task.ID, "info", "log.task.target_flow_discovered", map[string]any{
		"sites":  countSelectedSites(discovery.Sites),
		"groups": len(groups),
	})
	for _, warning := range discovery.Warnings {
		m.Store.AddLogKey("task", task.ID, "warning", "log.task.target_flow_warning", map[string]any{"message": warning})
	}

	certs := make([]*model.Certificate, 0, len(groups))
	uniqueCerts := make(map[int64]bool, len(groups))
	deployErrors := make([]string, 0)
	deployed, failedSites, failedGroups, issuedCount := 0, 0, 0, 0
	for _, group := range groups {
		m.Store.AddLogKey("task", task.ID, "info", "log.task.target_flow_group", map[string]any{
			"group":   group.Label,
			"domains": strings.Join(group.Domains, ", "),
			"sites":   len(group.Sites),
		})
		var cert *model.Certificate
		var issued bool
		if group.ReuseCertID > 0 {
			reused, getErr := m.Store.GetCertificate(group.ReuseCertID)
			if getErr == nil && reused != nil && certificateCoversDomains(reused.Domains, group.Domains) && !renewalDue(reused.NotBefore, reused.NotAfter, task.RenewDays) {
				cert = reused
				m.Store.AddLogKey("task", task.ID, "info", "log.task.target_flow_reused_cert", map[string]any{"group": group.Label, "cert": reused.ID})
			}
		}
		if cert == nil {
			cert, issued, err = m.runApplyForDomains(task, applyNode, opts, group.Domains)
			if err != nil {
				var limited *RateLimitError
				if errors.As(err, &limited) {
					return err
				}
				failedGroups++
				deployErrors = append(deployErrors, group.Label+": "+truncate(i18n.ErrorText(locale, err), 200))
				m.Store.AddLogKey("task", task.ID, "error", "log.task.target_flow_group_failed", map[string]any{"group": group.Label, "message": i18n.ErrorText(locale, err)})
				continue
			}
			if issued {
				issuedCount++
			}
		}
		certs = append(certs, cert)
		uniqueCerts[cert.ID] = true
		groupDeployed, groupFailed, groupErrors := m.deployTargetGroup(task, cert, group, cfg.Sources, locale)
		if applyNode.Config.NoKeyStorage || task.NoKeyStorage {
			// 该证书组部署结束，释放仅存在于内存中的私钥。
			cert.KeyPEM = ""
		}
		deployed += groupDeployed
		failedSites += groupFailed
		deployErrors = append(deployErrors, groupErrors...)
	}

	now := time.Now().UTC()
	status := "success"
	messageKey := "task.message.target_flow_success"
	if issuedCount == 0 && len(certs) > 0 {
		messageKey = "task.message.target_flow_reused"
	}
	message := i18n.Encode(messageKey, map[string]any{"certs": len(uniqueCerts), "issued": issuedCount, "sites": deployed})
	if failedGroups > 0 || failedSites > 0 || len(certs) == 0 {
		status = "partial"
		if len(certs) == 0 {
			status = "failed"
		}
		detail := strings.Join(deployErrors, "; ")
		if detail == "" {
			detail = i18n.T(locale, "task.target_flow.no_error_detail", nil)
		}
		message = i18n.Encode("task.message.target_flow_partial", map[string]any{
			"certs": len(certs), "sites": deployed, "failed": failedGroups + failedSites, "message": truncate(detail, 600),
		})
	}
	certIDs := make([]int64, 0, len(uniqueCerts))
	for certID := range uniqueCerts {
		certIDs = append(certIDs, certID)
	}
	if err := m.Store.ReplaceTaskCertificateLinks(task.ID, certIDs); err != nil {
		logger().Warn("保存目标流程证书关联失败", "task_id", task.ID, "error", err)
	}
	next := m.targetFlowNextRun(task, certs, status != "success")
	primaryCertID := int64(0)
	if len(certs) > 0 {
		primaryCertID = certs[0].ID
	}
	if err := m.Store.UpdateTaskResult(task.ID, status, message, &now, next, primaryCertID); err != nil {
		return i18n.E("task.error.update_status_failed", "error", err.Error())
	}
	result := &model.TargetFlowResult{
		DiscoveredAt:  now,
		SiteCount:     countSelectedSites(discovery.Sites),
		GroupCount:    len(groups),
		CertCount:     len(uniqueCerts),
		DeployedCount: deployed,
		FailedCount:   failedGroups + failedSites,
		Warnings:      discovery.Warnings,
	}
	task.TargetFlow = cfg
	task.TargetFlow.LastResult = result
	if err := m.Store.UpdateTaskTargetFlow(task.ID, task.TargetFlow); err != nil {
		logger().Warn("保存目标流程执行摘要失败", "task_id", task.ID, "error", err)
	}
	logger().Info("目标流程任务完成", "task_id", task.ID, "status", status, "groups", len(groups), "certs", len(certs), "deployed", deployed)
	var primaryCert *model.Certificate
	if len(certs) > 0 {
		primaryCert = certs[0]
	}
	m.notifyTaskResult(task, pipeline, primaryCert, status, message)
	return nil
}

// deployTargetGroup 将一个证书组部署到组内所有站点。
// 多值字段可以一次提交整组站点；单值字段则逐个站点部署，避免互相覆盖。
func (m *Manager) deployTargetGroup(task *model.Task, cert *model.Certificate, group model.CertGroup, sources []model.TargetSource, locale i18n.Locale) (int, int, []string) {
	byAccount := make(map[int64]map[string]string, len(sources))
	for _, source := range sources {
		byAccount[source.DeployAccountID] = source.DeployConfig
	}
	order := make([]int64, 0)
	bySiteAccount := make(map[int64][]model.TargetSite)
	for _, site := range group.Sites {
		if _, exists := bySiteAccount[site.AccountID]; !exists {
			order = append(order, site.AccountID)
		}
		bySiteAccount[site.AccountID] = append(bySiteAccount[site.AccountID], site)
	}
	deployed, failed := 0, 0
	deployErrors := make([]string, 0)
	for _, accountID := range order {
		sites := bySiteAccount[accountID]
		account, err := m.Store.GetDeployAccount(accountID)
		if err != nil {
			failed += len(sites)
			deployErrors = append(deployErrors, i18n.Tf(locale, "deploy.error.config_missing_id", "id", accountID))
			continue
		}
		def, found := DeployTypeInfo(account.Type)
		if !found {
			failed += len(sites)
			deployErrors = append(deployErrors, account.Name+": "+i18n.T(locale, "api.error.deploy_type_unsupported", nil))
			continue
		}
		baseCfg := MergeDeployConfig(def, account.Config, byAccount[accountID])
		deployCfg := FilterDeployConfig(def, baseCfg)
		fieldKey, multiple, _, ok := DeployObjectFieldInfo(def, deployCfg)
		if !ok {
			failed += len(sites)
			deployErrors = append(deployErrors, account.Name+": "+i18n.T(locale, "task.error.target_flow_plan_unsupported", map[string]any{"name": account.Name}))
			continue
		}
		// plan 返回的 multiple 比 manifest 静态字段更准确。
		for _, site := range sites {
			if site.MultipleValues {
				multiple = true
				break
			}
		}
		if multiple && len(sites) > 1 {
			values := make([]string, 0, len(sites))
			for _, site := range sites {
				if strings.TrimSpace(site.Value) != "" {
					values = append(values, site.Value)
				}
			}
			unitCfg := cloneStringMap(deployCfg)
			unitCfg[fieldKey] = strings.Join(values, ",")
			okDeploy, deployErr := m.deployOne(task, cert, account, unitCfg, i18n.T(locale, "task.target_flow.deploy_node", map[string]any{"site": strings.Join(values, ", ")}))
			if okDeploy {
				deployed += len(sites)
			} else {
				failed += len(sites)
				deployErrors = append(deployErrors, account.Name+": "+truncate(i18n.ErrorText(locale, deployErr), 200))
			}
			continue
		}
		for _, site := range sites {
			unitCfg := cloneStringMap(deployCfg)
			unitCfg[fieldKey] = site.Value
			okDeploy, deployErr := m.deployOne(task, cert, account, unitCfg, i18n.T(locale, "task.target_flow.deploy_node", map[string]any{"site": siteLabel(site)}))
			if okDeploy {
				deployed++
			} else {
				failed++
				deployErrors = append(deployErrors, account.Name+"/"+siteLabel(site)+": "+truncate(i18n.ErrorText(locale, deployErr), 200))
			}
		}
	}
	return deployed, failed, deployErrors
}

// targetFlowAllowedCAs 返回目标流程可复用的 CA 集合，避免任务切换 CA 后
// 仍然复用旧 CA 的证书。
func (m *Manager) targetFlowAllowedCAs(task *model.Task, node model.PipelineNode) map[string]bool {
	caValue := strings.TrimSpace(node.Config.CA)
	if caValue == "" {
		caValue = strings.TrimSpace(task.CA)
	}
	if caValue == "" {
		caValue, _ = m.Store.GetSetting("acme.ca")
	}
	if caValue == "" {
		caValue = "letsencrypt"
	}
	allowed := map[string]bool{caValue: true}
	if _, isPrivate := ParsePrivateCAValue(caValue); isPrivate {
		return allowed
	}
	poolID := m.CAPoolIDForTask(task, node.Config)
	if poolID <= 0 {
		return allowed
	}
	candidates, err := m.SelectCACandidates(poolID, false)
	if err != nil || len(candidates) == 0 {
		return allowed
	}
	for _, candidate := range candidates {
		allowed[candidate.Value] = true
	}
	return allowed
}

func applyNodeUsesPublicCA(task *model.Task, node model.PipelineNode) bool {
	caValue := strings.TrimSpace(node.Config.CA)
	if caValue == "" {
		caValue = strings.TrimSpace(task.CA)
	}
	if caValue == "" {
		return true
	}
	_, isPrivate := ParsePrivateCAValue(caValue)
	return !isPrivate
}

func cloneStringMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src)+1)
	for key, value := range src {
		out[key] = value
	}
	return out
}

func (m *Manager) targetFlowNextRun(task *model.Task, certs []*model.Certificate, failed bool) *time.Time {
	if !task.AutoRenew || !task.Enabled {
		return nil
	}
	var earliest *time.Time
	for _, cert := range certs {
		if cert == nil {
			continue
		}
		next := NextRenewTimeFor(cert.NotBefore, cert.NotAfter, task.RenewDays, true)
		if next == nil {
			continue
		}
		if earliest == nil || next.Before(*earliest) {
			earliest = next
		}
	}
	if failed || earliest == nil {
		retry := time.Now().UTC().Add(6 * time.Hour)
		if earliest == nil || retry.Before(*earliest) {
			earliest = &retry
		}
	}
	return earliest
}

func nodeName(node model.PipelineNode, current i18n.Locale) string {
	if node.Name != "" {
		return node.Name
	}
	switch node.Type {
	case model.PipelineApply:
		return i18n.T(current, "task.node.apply", nil)
	case model.PipelineDeploy:
		return i18n.T(current, "task.node.deploy", nil)
	default:
		return i18n.T(current, "task.node.notify", nil)
	}
}

// notifyTaskResult 根据通知节点的触发条件发送结果通知。
func (m *Manager) notifyTaskResult(task *model.Task, pipeline []model.PipelineNode, cert *model.Certificate, status, message string) {
	for _, node := range pipeline {
		if node.Type != model.PipelineNotify || !node.Enabled {
			continue
		}
		if !notifyMatches(node.Config.NotifyOn, status) {
			continue
		}
		m.sendTaskNotification(task, cert, status, message, node.Config.Subject, node.Config.NotifyChannels)
	}
}

func notifyMatches(rule, status string) bool {
	switch rule {
	case model.NotifySuccess:
		return status == "success"
	case model.NotifyFailure:
		return status == "failed" || status == "partial" || status == "rate_limited"
	default: // always
		return true
	}
}

func (m *Manager) sendTaskNotification(task *model.Task, cert *model.Certificate, status, message, customSubject string, channels []string) {
	current := m.Locale()
	rendered := i18n.Render(current, message)
	if status == "rate_limited" {
		subject := customSubject
		if subject == "" {
			subject = i18n.T(current, "email.task.rate_limited.subject", map[string]any{"name": task.Name})
		}
		body := i18n.Th(current, "email.task.rate_limited.body", map[string]any{
			"task":    task.Name,
			"domains": strings.Join(task.Domains, ", "),
			"message": rendered,
		})
		m.sendNotificationTo(subject, body, channels)
		return
	}
	if cert == nil {
		subject := customSubject
		if subject == "" {
			subject = i18n.T(current, "email.task.failed.subject", map[string]any{"name": task.Name})
		}
		body := i18n.Th(current, "email.task.failed.body", map[string]any{
			"task":    task.Name,
			"domains": strings.Join(task.Domains, ", "),
			"message": rendered,
		})
		m.sendNotificationTo(subject, body, channels)
		return
	}

	subject := customSubject
	if subject == "" {
		subject = i18n.T(current, "email.task.success.subject", map[string]any{"domain": cert.PrimaryDomain})
		if status == "partial" {
			subject = i18n.T(current, "email.task.partial.subject", map[string]any{"domain": cert.PrimaryDomain})
		}
	}
	body := i18n.Th(current, "email.task.success.body", map[string]any{
		"task":    task.Name,
		"domains": strings.Join(cert.Domains, ", "),
		"issuer":  cert.Issuer,
		"expires": cert.NotAfter.Local().Format("2006-01-02 15:04:05"),
		"days":    int(time.Until(cert.NotAfter).Hours() / 24),
		"status":  status,
		"message": rendered,
	})
	m.sendNotificationTo(subject, body, channels)
}

// privateCAChain 返回私有 CA 及其上级 CA 的 PEM 证书链。
func (m *Manager) privateCAChain(ca *model.PrivateCA) string {
	var builder strings.Builder
	current := ca
	visited := map[int64]bool{}
	for current != nil && !visited[current.ID] {
		visited[current.ID] = true
		if text := strings.TrimSpace(current.CertPEM); text != "" {
			// 与 ACME 服务端保持一致：fullchain 不包含自签名根证书。
			include := true
			if cert, err := parseFirstCertificate(text); err == nil && cert.CheckSignatureFrom(cert) == nil {
				include = false
			}
			if include {
				builder.WriteString(text)
				builder.WriteString("\n")
			}
		}
		if current.ParentID <= 0 {
			break
		}
		parent, err := m.Store.GetPrivateCA(current.ParentID)
		if err != nil {
			break
		}
		current = parent
	}
	return builder.String()
}

// loadCAEABSettings 读取指定 CA 独立保存的 EAB 凭据。
// 通用 ACME 上游的凭据保存在独立表中，内置 CA 仍使用设置项。
func (m *Manager) loadCAEABSettings(ca string) (string, string) {
	if id, ok := ParseACMECAValue(ca); ok {
		if record, err := m.Store.GetACMECA(id); err == nil {
			return record.EABKid, record.EABHMAC
		}
	}
	kid, _ := m.Store.GetSetting(CAEABKidSettingKey(ca))
	hmac, _ := m.Store.GetSecret(CAEABHMACSettingKey(ca))
	return kid, hmac
}

// NextRenewTime 计算下一次续期时间。
//
// 默认在到期前 renewDays 天续期。但短寿命证书（例如 CA 只签发 6 天有效期）
// 的寿命可能小于 renewDays，此时若直接按 renewDays 回推，目标时间会落在过去，
// 被钳制到“1 小时后”，从而变成每小时重复签发并迅速触发 CA 速率限制。
// 因此当提前量超过证书寿命时，改为在寿命的 2/3 处续期；同时保留最小 1 分钟的
// 钳制，避免已经过期的证书进入忙循环。
func NextRenewTime(notAfter time.Time, renewDays int, autoRenew bool) *time.Time {
	return NextRenewTimeFor(time.Now().UTC(), notAfter, renewDays, autoRenew)
}

// NextRenewTimeFor 使用证书的原始有效期计算下一次续期时间。
// 复用已有证书时必须使用 NotBefore..NotAfter 的完整寿命，否则每次复用
// 都会按“剩余寿命”重新回推，短寿命证书的续期时间会不断向后漂移。
func NextRenewTimeFor(notBefore, notAfter time.Time, renewDays int, autoRenew bool) *time.Time {
	if !autoRenew {
		return nil
	}
	if renewDays <= 0 {
		renewDays = 30
	}
	now := time.Now().UTC()
	lifetime := notAfter.Sub(notBefore)
	if lifetime <= 0 {
		lifetime = notAfter.Sub(now)
	}
	margin := time.Duration(renewDays) * 24 * time.Hour
	if margin >= lifetime {
		margin = lifetime / 3
	}
	target := notAfter.Add(-margin)
	limit := now.Add(time.Minute)
	if target.Before(limit) {
		target = limit
	}
	return &target
}
