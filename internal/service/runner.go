package service

import (
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// RunTask 执行一次证书申请或续期任务。trigger 是 task.trigger.* 翻译 key。
func (m *Manager) RunTask(taskID int64, trigger string) error {
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
	_ = m.Store.UpdateTaskResult(task.ID, "running", i18n.Encode("task.status.running", map[string]any{"trigger": triggerText}), &start, nil, task.CertID)
	m.Store.AddLogKey("task", task.ID, "info", "log.task.started", map[string]any{"name": task.Name, "domains": strings.Join(task.Domains, ", ")})

	err = m.executeTask(task, pipeline)
	if err != nil {
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
func (m *Manager) executeTask(task *model.Task, pipeline []model.PipelineNode) error {
	applyNode, found := findPipelineNode(pipeline, model.PipelineApply)
	if !found {
		return i18n.E("task.error.apply_node_missing")
	}

	cert, err := m.runApplyNode(task, applyNode)
	if err != nil {
		return err
	}

	deployErrors, deployedCount := m.runDeployNodes(task, cert, pipeline)

	status := "success"
	message := i18n.Encode("task.message.success", nil)
	if len(deployErrors) > 0 {
		status = "partial"
		message = i18n.Encode("task.message.partial", map[string]any{"message": truncate(strings.Join(deployErrors, "; "), 600)})
	} else if deployedCount > 0 {
		message = i18n.Encode("task.message.deployed", map[string]any{"count": deployedCount})
	}

	next := NextRenewTime(cert.NotAfter, task.RenewDays, task.AutoRenew && task.Enabled)
	now := time.Now().UTC()
	if err := m.Store.UpdateTaskResult(task.ID, status, message, &now, next, cert.ID); err != nil {
		return i18n.E("task.error.update_status_failed", "error", err.Error())
	}
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

// runApplyNode 执行申请证书模块。
func (m *Manager) runApplyNode(task *model.Task, node model.PipelineNode) (*model.Certificate, error) {
	cfg := node.Config
	domains := util.NormalizeDomains(cfg.Domains)
	if len(domains) == 0 {
		domains = util.NormalizeDomains(task.Domains)
	}
	if len(domains) == 0 {
		return nil, i18n.E("task.error.domains_empty")
	}
	for _, d := range domains {
		if !util.ValidDomain(d) {
			return nil, i18n.E("task.error.domain_invalid", "domain", d)
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

	var result *ACMEResult
	var err error
	if caID, isPrivate := ParsePrivateCAValue(caValue); isPrivate {
		ca, caErr := m.Store.GetPrivateCA(caID)
		if caErr != nil {
			return nil, i18n.E("task.error.private_ca_not_found")
		}
		if time.Now().After(ca.NotAfter) {
			return nil, i18n.E("task.error.private_ca_expired")
		}
		m.Store.AddLogKey("task", task.ID, "info", "log.task.applying_private", map[string]any{"node": nodeName, "ca": ca.Name})
		result, err = IssuePrivateCertificate(ca, PrivateCertRequest{Domains: domains, KeyType: keyType, ValidDays: 365})
		if err != nil {
			return nil, err
		}
		result.FullchainPEM = result.CertPEM + m.privateCAChain(ca)
	} else {
		email, _ := m.Store.GetSetting("acme.email")
		if strings.TrimSpace(email) == "" {
			return nil, i18n.E("task.error.acme_email_required")
		}
		eabKid, eabHMAC := m.loadCAEABSettings(caValue)

		dnsAccountID := cfg.DNSAccountID
		if dnsAccountID == 0 {
			dnsAccountID = task.DNSAccountID
		}
		if dnsAccountID == 0 {
			return nil, i18n.E("task.error.dns_account_required")
		}
		dnsAccount, err := m.Store.GetDNSAccount(dnsAccountID)
		if err != nil {
			return nil, i18n.E("task.error.dns_account_not_found", "error", err.Error())
		}

		m.Store.AddLogKey("task", task.ID, "info", "log.task.applying", map[string]any{"node": nodeName})
		result, err = m.Obtain(ACMERequest{
			Domains:    domains,
			DNSAccount: dnsAccount,
			CA:         caValue,
			KeyType:    keyType,
			Email:      email,
			EABKid:     eabKid,
			EABHMAC:    eabHMAC,
		})
		if err != nil {
			return nil, err
		}
	}
	m.Store.AddLogKey("task", task.ID, "success", "log.task.issued", map[string]any{"time": result.NotAfter.Local().Format("2006-01-02 15:04:05")})

	cert := &model.Certificate{
		TaskID:        task.ID,
		Domains:       domains,
		PrimaryDomain: domains[0],
		Issuer:        result.Issuer,
		CA:            caValue,
		CertPEM:       result.CertPEM,
		KeyPEM:        result.KeyPEM,
		FullchainPEM:  result.FullchainPEM,
		Serial:        result.Serial,
		NotBefore:     result.NotBefore,
		NotAfter:      result.NotAfter,
		Status:        "valid",
	}
	if err := m.Store.CreateCertificate(cert); err != nil {
		return nil, i18n.E("task.error.save_cert_failed", "error", err.Error())
	}
	return cert, nil
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
		return status == "failed" || status == "partial"
	default: // always
		return true
	}
}

func (m *Manager) sendTaskNotification(task *model.Task, cert *model.Certificate, status, message, customSubject string, channels []string) {
	current := m.Locale()
	rendered := i18n.Render(current, message)
	if cert == nil {
		subject := customSubject
		if subject == "" {
			subject = i18n.T(current, "email.task.failed.subject", map[string]any{"name": task.Name})
		}
		body := i18n.T(current, "email.task.failed.body", map[string]any{
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
	body := i18n.T(current, "email.task.success.body", map[string]any{
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
			builder.WriteString(text)
			builder.WriteString("\n")
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
func (m *Manager) loadCAEABSettings(ca string) (string, string) {
	kid, _ := m.Store.GetSetting(CAEABKidSettingKey(ca))
	hmac, _ := m.Store.GetSecret(CAEABHMACSettingKey(ca))
	return kid, hmac
}

func NextRenewTime(notAfter time.Time, renewDays int, autoRenew bool) *time.Time {
	if !autoRenew {
		return nil
	}
	if renewDays <= 0 {
		renewDays = 30
	}
	target := notAfter.AddDate(0, 0, -renewDays)
	limit := time.Now().UTC().Add(time.Hour)
	if target.Before(limit) {
		target = limit
	}
	return &target
}
