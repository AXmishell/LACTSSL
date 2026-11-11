package api

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
)

func (s *Server) listCertificates(c *gin.Context) {
	list, err := s.Store.ListCertificates()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) getCertificate(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	cert, err := s.Store.GetCertificate(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	ok(c, cert)
}

func (s *Server) downloadCertificate(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	cert, err := s.Store.GetCertificate(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	kind := c.DefaultQuery("type", "fullchain")
	if kind == "key" && strings.TrimSpace(cert.KeyPEM) == "" {
		failKey(c, http.StatusBadRequest, "api.error.cert_key_unavailable")
		return
	}
	var content, suffix string
	switch kind {
	case "key":
		content, suffix = cert.KeyPEM, "key"
	case "cert":
		content, suffix = cert.CertPEM, "crt"
	default:
		content, suffix = cert.FullchainPEM, "fullchain.crt"
	}
	filename := fmt.Sprintf("%s-%s.%s", cert.PrimaryDomain, cert.NotAfter.Format("20060102"), suffix)
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-pem-file; charset=utf-8", []byte(content))
}

// getCertificatePEM 按需返回证书/私钥 PEM，避免详情接口默认携带私钥。
func (s *Server) getCertificatePEM(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	cert, err := s.Store.GetCertificate(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	kind := c.DefaultQuery("type", "fullchain")
	var content string
	switch kind {
	case "key":
		content = cert.KeyPEM
		if strings.TrimSpace(content) == "" {
			failKey(c, http.StatusBadRequest, "api.error.cert_key_unavailable")
			return
		}
	case "cert":
		content = cert.CertPEM
	default:
		kind = "fullchain"
		content = cert.FullchainPEM
	}
	c.Header("Cache-Control", "no-store")
	ok(c, gin.H{"type": kind, "content": content})
}

func (s *Server) deleteCertificate(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if err := s.Store.DeleteCertificate(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("cert", id, "warning", "log.cert.deleted", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

// certCleanupBody 描述一键清理证书的请求条件。
type certCleanupBody struct {
	Scope             string `json:"scope"`
	Status            string `json:"status"`
	ExpiredDays       int    `json:"expired_days"`
	RevokedDays       int    `json:"revoked_days"`
	OlderThanDays     int    `json:"older_than_days"`
	Domain            string `json:"domain"`
	Keyword           string `json:"keyword"`
	CA                string `json:"ca"`
	Issuer            string `json:"issuer"`
	Source            string `json:"source"`
	KeepLatestPerTask bool   `json:"keep_latest_per_task"`
	KeepLatest        bool   `json:"keep_latest"`
	DryRun            bool   `json:"dry_run"`
}

// filter 校验请求条件并转换为数据层筛选条件。
func (b certCleanupBody) filter() (store.CertCleanupFilter, bool) {
	scope := strings.ToLower(strings.TrimSpace(b.Scope))
	if scope == "" {
		scope = strings.ToLower(strings.TrimSpace(b.Status))
	}
	if scope == "" {
		scope = "expired"
	}
	switch scope {
	case "expired", "revoked", "valid", "all":
	default:
		return store.CertCleanupFilter{}, false
	}
	domain := strings.TrimSpace(b.Domain)
	if domain == "" {
		domain = strings.TrimSpace(b.Keyword)
	}
	ca := strings.TrimSpace(b.CA)
	if ca == "" {
		ca = strings.TrimSpace(b.Issuer)
	}
	return store.CertCleanupFilter{
		Scope:             scope,
		ExpiredDays:       max(b.ExpiredDays, 0),
		RevokedDays:       max(b.RevokedDays, 0),
		OlderThanDays:     max(b.OlderThanDays, 0),
		Domain:            domain,
		CA:                ca,
		Source:            strings.TrimSpace(b.Source),
		KeepLatestPerTask: b.KeepLatestPerTask || b.KeepLatest,
	}, true
}

// parseCertCleanupBody 解析清理请求，空请求体按默认条件处理。
func parseCertCleanupBody(c *gin.Context) (certCleanupBody, bool) {
	var body certCleanupBody
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return body, false
	}
	return body, true
}

// cleanupCertificates 一键清理证书。默认仅删除已过期证书，可叠加时间、域名、CA、来源等条件。
func (s *Server) cleanupCertificates(c *gin.Context) {
	body, okBody := parseCertCleanupBody(c)
	if !okBody {
		return
	}
	filter, okFilter := body.filter()
	if !okFilter {
		failKey(c, http.StatusBadRequest, "api.error.invalid_cleanup_scope")
		return
	}

	now := time.Now()
	if body.DryRun {
		count, err := s.Store.CountCertificatesForCleanup(filter, now)
		if err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, gin.H{"count": count, "deleted": 0, "preview": true})
		return
	}

	deleted, err := s.Store.CleanupCertificates(filter, now)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if deleted > 0 {
		s.Store.AddLogKey("cert", 0, "warning", "log.cert.cleaned", map[string]any{"count": deleted})
	}
	ok(c, gin.H{
		"deleted": deleted,
		"count":   deleted,
		"message": i18n.T(locale(c), "api.ok.certs_cleaned", map[string]any{"count": deleted}),
	})
}

// previewCleanupCertificates 返回符合清理条件的证书数量，不实际删除。
func (s *Server) previewCleanupCertificates(c *gin.Context) {
	body, okBody := parseCertCleanupBody(c)
	if !okBody {
		return
	}
	filter, okFilter := body.filter()
	if !okFilter {
		failKey(c, http.StatusBadRequest, "api.error.invalid_cleanup_scope")
		return
	}
	count, err := s.Store.CountCertificatesForCleanup(filter, time.Now())
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"count": count})
}

func (s *Server) listDeployments(c *gin.Context) {
	list, err := s.Store.ListDeployRecords(200)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	for index := range list {
		list[index].Message = localize(c, list[index].Message)
	}
	ok(c, list)
}

type deployTargetBody struct {
	DeployAccountID int64             `json:"deploy_account_id"`
	DeployConfig    map[string]string `json:"deploy_config"`
}

func (s *Server) deployCertificate(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	cert, err := s.Store.GetCertificate(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	var body struct {
		DeployIDs []int64            `json:"deploy_ids"`
		Targets   []deployTargetBody `json:"targets"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	targets := body.Targets
	if len(targets) == 0 && len(body.DeployIDs) > 0 {
		// 兼容只提交目标凭据 ID 的旧客户端：从证书所属任务的部署节点补全参数。
		targets = s.deployTargetsFromTask(cert, body.DeployIDs)
	}
	if len(targets) == 0 {
		failKey(c, http.StatusBadRequest, "api.error.deploy_target_required")
		return
	}
	taskConfigs := s.taskDeployConfigs(cert)
	results := make([]gin.H, 0, len(targets))
	for _, target := range targets {
		deployID := target.DeployAccountID
		account, err := s.Store.GetDeployAccount(deployID)
		if err != nil {
			results = append(results, gin.H{"deploy_account_id": deployID, "name": i18n.T(locale(c), "deploy.unknown_target", nil), "success": false, "message": i18n.T(locale(c), "deploy.config_missing", nil)})
			continue
		}
		def, found := service.DeployTypeInfo(account.Type)
		if !found {
			results = append(results, gin.H{"deploy_account_id": deployID, "name": account.Name, "success": false, "message": i18n.T(locale(c), "deploy.config_missing", nil)})
			continue
		}
		deployCfg := target.DeployConfig
		if len(deployCfg) == 0 {
			deployCfg = taskConfigs[deployID]
		}
		deployCfg = service.FilterDeployConfig(def, deployCfg)
		// 兼容旧数据：凭据中仍保留部署参数时作为兜底。
		if len(deployCfg) == 0 {
			deployCfg = service.ExtractDeployConfig(def, account.Config)
		}
		result := gin.H{"deploy_account_id": account.ID, "name": account.Name, "success": true, "message": i18n.T(locale(c), "deploy.success", nil)}
		record := &model.DeployRecord{
			CertID:          cert.ID,
			DeployAccountID: account.ID,
			AccountName:     account.Name,
			TaskID:          cert.TaskID,
			Status:          "success",
			Message:         i18n.Encode("deploy.success_manual", nil),
		}
		if err := s.Manager.Deploy(cert, account, deployCfg); err != nil {
			result["success"] = false
			result["message"] = i18n.ErrorText(locale(c), err)
			record.Status = "failed"
			record.Message = truncateText(i18n.EncodeError(err), 800)
			s.Store.AddLogKey("deploy", cert.TaskID, "error", "log.deploy.manual_failed", map[string]any{"account": account.Name, "message": i18n.ErrorText(locale(c), err)})
		} else {
			s.Store.AddLogKey("deploy", cert.TaskID, "success", "log.deploy.manual_success", map[string]any{"account": account.Name})
		}
		_ = s.Store.CreateDeployRecord(record)
		results = append(results, result)
	}
	ok(c, gin.H{"results": results})
}

// taskDeployConfigs 返回证书所属任务中按目标凭据索引的部署参数。
func (s *Server) taskDeployConfigs(cert *model.Certificate) map[int64]map[string]string {
	out := make(map[int64]map[string]string)
	if cert.TaskID <= 0 {
		return out
	}
	task, err := s.Store.GetTask(cert.TaskID)
	if err != nil {
		return out
	}
	for _, node := range task.EffectivePipeline() {
		if node.Type == model.PipelineDeploy && node.Config.DeployAccountID > 0 {
			out[node.Config.DeployAccountID] = node.Config.DeployConfig
		}
	}
	return out
}

// deployTargetsFromTask 从证书所属任务流程中提取部署节点参数。
func (s *Server) deployTargetsFromTask(cert *model.Certificate, ids []int64) []deployTargetBody {
	byID := s.taskDeployConfigs(cert)
	targets := make([]deployTargetBody, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, deployTargetBody{DeployAccountID: id, DeployConfig: byID[id]})
	}
	return targets
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// ---------------------------- 证书分析 ----------------------------

type chainItem struct {
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	IsCA         bool      `json:"is_ca"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	Signature    string    `json:"signature_algorithm"`
	PublicKey    string    `json:"public_key"`
	SerialNumber string    `json:"serial_number"`
}

type certAnalysis struct {
	Subject            string      `json:"subject"`
	Issuer             string      `json:"issuer"`
	Serial             string      `json:"serial"`
	SignatureAlgorithm string      `json:"signature_algorithm"`
	PublicKeyAlgorithm string      `json:"public_key_algorithm"`
	KeyBits            int         `json:"key_bits"`
	NotBefore          time.Time   `json:"not_before"`
	NotAfter           time.Time   `json:"not_after"`
	DaysLeft           int         `json:"days_left"`
	DNSNames           []string    `json:"dns_names"`
	IsWildcard         bool        `json:"is_wildcard"`
	Chain              []chainItem `json:"chain"`
	Issues             []string    `json:"issues"`
	Level              string      `json:"level"`
	Score              int         `json:"score"`
	ValidityDays       int         `json:"validity_days"`
}

func (s *Server) analyzeCertificate(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	cert, err := s.Store.GetCertificate(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	analysis, err := analyzePEM(cert.FullchainPEM, locale(c))
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ok(c, analysis)
}

func analyzePEM(fullchain string, locales ...i18n.Locale) (*certAnalysis, error) {
	current := i18n.Default
	if len(locales) > 0 {
		current = locales[0]
	}
	certs, err := parsePEMChain(fullchain)
	if err != nil {
		return nil, err
	}
	var leaf *x509.Certificate
	for _, c := range certs {
		if !c.IsCA {
			leaf = c
			break
		}
	}
	if leaf == nil {
		leaf = certs[0]
	}
	analysis := &certAnalysis{
		Subject:            leaf.Subject.String(),
		Issuer:             leaf.Issuer.String(),
		Serial:             strings.ToUpper(leaf.SerialNumber.Text(16)),
		SignatureAlgorithm: leaf.SignatureAlgorithm.String(),
		PublicKeyAlgorithm: leaf.PublicKeyAlgorithm.String(),
		NotBefore:          leaf.NotBefore,
		NotAfter:           leaf.NotAfter,
		DNSNames:           leaf.DNSNames,
		DaysLeft:           int(time.Until(leaf.NotAfter).Hours() / 24),
		ValidityDays:       int(leaf.NotAfter.Sub(leaf.NotBefore).Hours() / 24),
	}
	if analysis.DNSNames == nil {
		analysis.DNSNames = []string{}
	}
	for _, name := range leaf.DNSNames {
		if strings.HasPrefix(name, "*.") {
			analysis.IsWildcard = true
			break
		}
	}
	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		analysis.KeyBits = pub.N.BitLen()
	case *ecdsa.PublicKey:
		analysis.KeyBits = pub.Curve.Params().BitSize
	}

	type issueDef struct {
		key     string
		params  map[string]any
		penalty int
	}
	issues := []issueDef{}
	if time.Now().After(leaf.NotAfter) {
		issues = append(issues, issueDef{key: "cert.issue.expired", penalty: 50})
	} else if analysis.DaysLeft <= 30 {
		issues = append(issues, issueDef{key: "cert.issue.expiring", params: map[string]any{"days": analysis.DaysLeft}, penalty: 15})
	}
	if leaf.Subject.String() == leaf.Issuer.String() {
		issues = append(issues, issueDef{key: "cert.issue.self_signed", penalty: 20})
	}
	if strings.Contains(leaf.SignatureAlgorithm.String(), "SHA1") {
		issues = append(issues, issueDef{key: "cert.issue.sha1", penalty: 20})
	}
	if analysis.PublicKeyAlgorithm == "RSA" && analysis.KeyBits < 2048 {
		issues = append(issues, issueDef{key: "cert.issue.rsa_small", penalty: 20})
	}
	if analysis.ValidityDays > 398 {
		issues = append(issues, issueDef{key: "cert.issue.validity", penalty: 10})
	}
	if len(leaf.DNSNames) == 0 {
		issues = append(issues, issueDef{key: "cert.issue.no_san", penalty: 10})
	}
	if len(certs) > 1 && leaf.Issuer.String() != certs[1].Subject.String() {
		issues = append(issues, issueDef{key: "cert.issue.chain", penalty: 10})
	}

	score := 100
	for _, issue := range issues {
		score -= issue.penalty
	}
	if score < 0 {
		score = 0
	}
	analysis.Score = score
	switch {
	case score >= 90:
		analysis.Level = "excellent"
	case score >= 70:
		analysis.Level = "good"
	case score >= 50:
		analysis.Level = "warning"
	default:
		analysis.Level = "danger"
	}
	analysis.Issues = make([]string, len(issues))
	for index, issue := range issues {
		analysis.Issues[index] = i18n.T(current, issue.key, issue.params)
	}

	for _, c := range certs {
		item := chainItem{
			Subject:      c.Subject.String(),
			Issuer:       c.Issuer.String(),
			IsCA:         c.IsCA,
			NotBefore:    c.NotBefore,
			NotAfter:     c.NotAfter,
			Signature:    c.SignatureAlgorithm.String(),
			SerialNumber: strings.ToUpper(c.SerialNumber.Text(16)),
		}
		switch pub := c.PublicKey.(type) {
		case *rsa.PublicKey:
			item.PublicKey = fmt.Sprintf("RSA %d", pub.N.BitLen())
		case *ecdsa.PublicKey:
			item.PublicKey = fmt.Sprintf("ECDSA %d", pub.Curve.Params().BitSize)
		default:
			item.PublicKey = c.PublicKeyAlgorithm.String()
		}
		analysis.Chain = append(analysis.Chain, item)
	}
	return analysis, nil
}

func parsePEMChain(data string) ([]*x509.Certificate, error) {
	rest := []byte(data)
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, i18n.E("api.error.parse_cert_failed", "error", err.Error())
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, i18n.E("api.error.no_valid_cert")
	}
	return certs, nil
}
