package service

import (
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

	task, err := m.Store.GetTask(taskID)
	if err != nil {
		return i18n.E("task.error.not_found", "error", err.Error())
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

	err = m.executeTask(task, pipeline, opts)
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
func (m *Manager) RunTaskQueued(taskID int64, trigger string) error {
	// 排队期间也做去重，避免连续两次批量执行让同一任务重复签发。
	if _, loaded := m.queued.LoadOrStore(taskID, true); loaded {
		return i18n.E("task.error.running")
	}
	defer m.queued.Delete(taskID)
	if m.batchSem != nil {
		m.batchSem <- struct{}{}
		defer func() { <-m.batchSem }()
	}
	return m.RunTask(taskID, trigger)
}

// executeTask 按任务流程顺序执行申请、部署与通知模块。
func (m *Manager) executeTask(task *model.Task, pipeline []model.PipelineNode, opts RunOptions) error {
	applyNode, found := findPipelineNode(pipeline, model.PipelineApply)
	if !found {
		return i18n.E("task.error.apply_node_missing")
	}

	cert, issued, err := m.runApplyNode(task, applyNode, opts)
	if err != nil {
		return err
	}

	deployErrors, deployedCount := m.runDeployNodes(task, cert, pipeline)

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
	cfg := node.Config
	domains := util.NormalizeDomains(cfg.Domains)
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
	if !opts.ForceIssue {
		if cert, ok := m.findReusableCertificate(task, caValue, allowedCAs, keyType, domains); ok {
			m.Store.AddLogKey("task", task.ID, "info", "log.task.reused", map[string]any{
				"time": cert.NotAfter.Local().Format("2006-01-02 15:04:05"),
			})
			return cert, false, nil
		}
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

		dnsAccountID := cfg.DNSAccountID
		if dnsAccountID == 0 {
			dnsAccountID = task.DNSAccountID
		}
		if dnsAccountID == 0 {
			return nil, false, i18n.E("task.error.dns_account_required")
		}
		dnsAccount, err := m.Store.GetDNSAccount(dnsAccountID)
		if err != nil {
			return nil, false, i18n.E("task.error.dns_account_not_found", "error", err.Error())
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
				Domains:    domains,
				DNSAccount: dnsAccount,
				CA:         candidate.Value,
				KeyType:    keyType,
				Email:      email,
				EABKid:     eabKid,
				EABHMAC:    eabHMAC,
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

	cert := &model.Certificate{
		TaskID:        task.ID,
		Domains:       domains,
		PrimaryDomain: domains[0],
		Issuer:        result.Issuer,
		CA:            usedCA,
		CertPEM:       result.CertPEM,
		KeyPEM:        result.KeyPEM,
		FullchainPEM:  result.FullchainPEM,
		Serial:        result.Serial,
		NotBefore:     result.NotBefore,
		NotAfter:      result.NotAfter,
		Status:        "valid",
	}
	if err := m.Store.CreateCertificate(cert); err != nil {
		return nil, false, i18n.E("task.error.save_cert_failed", "error", err.Error())
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
		record := &model.DeployRecord{
			CertID:          cert.ID,
			DeployAccountID: account.ID,
			AccountName:     account.Name,
			TaskID:          task.ID,
			Status:          "success",
		}
		if err := m.Deploy(cert, account, node.Config.DeployConfig); err != nil {
			record.Status = "failed"
			record.Message = truncate(i18n.EncodeError(err), 800)
			deployErrors = append(deployErrors, account.Name+": "+truncate(i18n.ErrorText(m.Locale(), err), 200))
			m.Store.AddLogKey("deploy", task.ID, "error", "log.deploy.failed", map[string]any{"node": nodeName(node, m.Locale()), "account": account.Name, "message": i18n.ErrorText(m.Locale(), err)})
			logger().Warn("部署失败", "task_id", task.ID, "cert_id", cert.ID, "account", account.Name, "node", nodeName(node, m.Locale()), "error", SafeError(err))
		} else {
			deployed++
			record.Message = i18n.Encode("deploy.success", nil)
			m.Store.AddLogKey("deploy", task.ID, "success", "log.deploy.success", map[string]any{"node": nodeName(node, m.Locale()), "account": account.Name, "type": account.Type})
		}
		_ = m.Store.CreateDeployRecord(record)
	}
	return deployErrors, deployed
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
