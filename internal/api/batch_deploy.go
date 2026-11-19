package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/deploy"
	"lactssl/internal/service"
)

const maxBatchDeployCertificates = 100

const (
	batchStatusReady    = "ready"
	batchStatusDeployed = "deployed"
	batchStatusSkipped  = "skipped"
	batchStatusConflict = "conflict"
	batchStatusFailed   = "failed"
)

type batchDeployBody struct {
	CertificateIDs []int64            `json:"certificate_ids"`
	Targets        []deployTargetBody `json:"targets"`
}

type batchDeployEntry struct {
	cert    *model.Certificate
	account *model.DeployAccount
	config  map[string]string
	status  string
	message string
	site    string
}

// batchDeployCertificates 批量部署多张证书。
//
// 站点类目标会先通过部署插件的 plan 能力读取真实网站清单，再按证书域名
// 逐张匹配：匹配不到或与其他证书冲突时不部署，只在结果中标记 skipped /
// conflict。每个目标可单独开启强制部署，跳过匹配直接下发。
func (s *Server) batchDeployCertificates(c *gin.Context) {
	if !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	var body batchDeployBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ids, err := normalizeBatchDeployIDs(body.CertificateIDs)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if len(body.Targets) == 0 {
		failKey(c, http.StatusBadRequest, "api.error.deploy_target_required")
		return
	}

	certs := make([]*model.Certificate, 0, len(ids))
	for _, id := range ids {
		cert, err := s.Store.GetCertificate(id)
		if err != nil {
			failKey(c, http.StatusNotFound, "api.error.cert_not_found")
			return
		}
		if !cert.KeyAvailable {
			failKey(c, http.StatusBadRequest, "api.error.cert_key_unavailable")
			return
		}
		certs = append(certs, cert)
	}

	entries := make([]batchDeployEntry, 0, len(certs)*len(body.Targets))
	owners := make(map[string]string)
	planCache := make(map[string]service.DeployObjectPlan)
	planErrors := make(map[string]error)
	commandChecked := false

	for _, cert := range certs {
		taskConfigs := s.taskDeployConfigs(cert)
		if !commandChecked && s.manualDeployUsesCommand(body.Targets, taskConfigs) {
			if !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
				return
			}
			commandChecked = true
		}
		domains := service.NormalizeDeployDomains(certificateDomains(cert))
		for _, target := range body.Targets {
			account, def, deployCfg, err := s.resolveBatchDeployTarget(target, taskConfigs)
			if err != nil {
				failErr(c, http.StatusBadRequest, err)
				return
			}
			certEntries, err := s.planBatchDeployEntries(c, cert, target, account, def, deployCfg, domains, owners, planCache, planErrors)
			if err != nil {
				if errors.Is(err, deployplugin.ErrPlanUnsupported) {
					failKey(c, http.StatusBadRequest, "api.error.batch_deploy_plan_unsupported")
					return
				}
				failKey(c, http.StatusBadRequest, "api.error.batch_deploy_plan_failed", "error", service.SanitizeErrorText(err.Error()))
				return
			}
			entries = append(entries, certEntries...)
		}
	}

	results := make([]gin.H, 0, len(entries))
	summary := gin.H{"deployed": 0, "skipped": 0, "conflict": 0, "failed": 0}
	for _, entry := range entries {
		if entry.status != batchStatusReady {
			result := gin.H{
				"certificate_id":    entry.cert.ID,
				"domain":            entry.cert.PrimaryDomain,
				"deploy_account_id": entry.account.ID,
				"name":              entry.account.Name,
				"success":           false,
				"status":            entry.status,
				"message":           entry.message,
			}
			if entry.site != "" {
				result["site"] = entry.site
			}
			results = append(results, result)
			summary[entry.status] = summary[entry.status].(int) + 1
			continue
		}
		result := s.deployToTarget(c, entry.cert, entry.account, entry.config)
		result["certificate_id"] = entry.cert.ID
		result["domain"] = entry.cert.PrimaryDomain
		if entry.site != "" {
			result["site"] = entry.site
		}
		if success, _ := result["success"].(bool); success {
			result["status"] = batchStatusDeployed
			summary["deployed"] = summary["deployed"].(int) + 1
		} else {
			result["status"] = batchStatusFailed
			summary["failed"] = summary["failed"].(int) + 1
		}
		results = append(results, result)
	}
	ok(c, gin.H{"results": results, "summary": summary})
}

// planBatchDeployEntries 为一张证书生成每个部署目标的执行或跳过记录。
func (s *Server) planBatchDeployEntries(
	c *gin.Context,
	cert *model.Certificate,
	target deployTargetBody,
	account *model.DeployAccount,
	def service.ProviderDef,
	deployCfg map[string]string,
	domains []string,
	owners map[string]string,
	planCache map[string]service.DeployObjectPlan,
	planErrors map[string]error,
) ([]batchDeployEntry, error) {
	base := batchDeployEntry{cert: cert, account: account}
	if service.DeployObjectIsSingleton(def, deployCfg) {
		identity := strconv.FormatInt(account.ID, 10) + "|@panel"
		if owner, exists := owners[identity]; exists {
			base.status = batchStatusConflict
			base.message = i18n.T(locale(c), "api.batch_deploy.skipped_panel_conflict", map[string]any{
				"certs": owner + " / " + cert.PrimaryDomain,
			})
			return []batchDeployEntry{base}, nil
		}
		owners[identity] = cert.PrimaryDomain
		base.status = batchStatusReady
		base.config = deployCfg
		return []batchDeployEntry{base}, nil
	}

	fieldKey, multiple, _, ok := service.DeployObjectFieldInfo(def, deployCfg)
	if !ok {
		base.status = batchStatusReady
		base.config = deployCfg
		return []batchDeployEntry{base}, nil
	}
	selected := splitDeployValues(deployCfg[fieldKey])
	var values []string
	if target.Force {
		values = service.ForceDeployValues(deployCfg, fieldKey, domains)
	} else {
		plan, err := s.loadBatchDeployPlan(c, account, deployCfg, fieldKey, planCache, planErrors)
		if err != nil {
			return nil, err
		}
		values = service.DeployObjectValues(service.MatchDeployObjects(plan.Objects, selected, domains))
	}
	if len(values) == 0 {
		base.status = batchStatusSkipped
		if target.Force {
			base.message = i18n.T(locale(c), "api.batch_deploy.skipped_no_target", nil)
		} else {
			base.message = i18n.T(locale(c), "api.batch_deploy.skipped_no_site", nil)
		}
		return []batchDeployEntry{base}, nil
	}
	if multiple {
		deployCfg[fieldKey] = strings.Join(values, ",")
		return []batchDeployEntry{s.claimBatchDeployEntry(c, base, deployCfg, values, owners)}, nil
	}
	out := make([]batchDeployEntry, 0, len(values))
	for _, value := range values {
		unitCfg := cloneDeployConfig(deployCfg)
		unitCfg[fieldKey] = value
		out = append(out, s.claimBatchDeployEntry(c, base, unitCfg, []string{value}, owners))
	}
	return out, nil
}

func (s *Server) claimBatchDeployEntry(c *gin.Context, base batchDeployEntry, cfg map[string]string, values []string, owners map[string]string) batchDeployEntry {
	identity := strconv.FormatInt(base.account.ID, 10) + "|" + strings.Join(values, ",")
	if owner, exists := owners[identity]; exists {
		base.status = batchStatusConflict
		base.site = strings.Join(values, ", ")
		base.message = i18n.T(locale(c), "api.batch_deploy.skipped_conflict", map[string]any{
			"sites": base.site, "certs": owner + " / " + base.cert.PrimaryDomain,
		})
		return base
	}
	owners[identity] = base.cert.PrimaryDomain
	base.status = batchStatusReady
	base.config = cfg
	base.site = strings.Join(values, ", ")
	return base
}

func (s *Server) loadBatchDeployPlan(
	c *gin.Context,
	account *model.DeployAccount,
	cfg map[string]string,
	fieldKey string,
	cache map[string]service.DeployObjectPlan,
	cacheErrors map[string]error,
) (service.DeployObjectPlan, error) {
	key := planCacheKey(account, cfg, fieldKey)
	if result, ok := cache[key]; ok {
		return result, nil
	}
	if err, ok := cacheErrors[key]; ok {
		return service.DeployObjectPlan{}, err
	}
	result, err := s.Manager.PlanDeployObjects(c.Request.Context(), account, cfg, string(locale(c)))
	if err != nil {
		cacheErrors[key] = err
		return service.DeployObjectPlan{}, err
	}
	cache[key] = result
	return result, nil
}

func planCacheKey(account *model.DeployAccount, cfg map[string]string, fieldKey string) string {
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		if key == fieldKey {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(strconv.FormatInt(account.ID, 10))
	builder.WriteByte('|')
	builder.WriteString(account.Type)
	for _, key := range keys {
		builder.WriteByte('|')
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(cfg[key])
	}
	return builder.String()
}

func (s *Server) resolveBatchDeployTarget(target deployTargetBody, taskConfigs map[int64]map[string]string) (*model.DeployAccount, service.ProviderDef, map[string]string, error) {
	account, err := s.Store.GetDeployAccount(target.DeployAccountID)
	if err != nil {
		return nil, service.ProviderDef{}, nil, i18n.E("api.error.deploy_account_not_found")
	}
	def, found := service.DeployTypeInfo(account.Type)
	if !found {
		return account, service.ProviderDef{}, nil, i18n.E("api.error.deploy_type_unsupported")
	}
	deployCfg := target.DeployConfig
	if len(deployCfg) == 0 {
		deployCfg = taskConfigs[target.DeployAccountID]
	}
	deployCfg = service.FilterDeployConfig(def, deployCfg)
	// 兼容旧数据：凭据中仍保留部署参数时作为兜底。
	if len(deployCfg) == 0 {
		deployCfg = service.ExtractDeployConfig(def, account.Config)
	}
	return account, def, deployCfg, nil
}

func splitDeployValues(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneDeployConfig(cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg)+1)
	for key, value := range cfg {
		out[key] = value
	}
	return out
}

// normalizeBatchDeployIDs 去重并校验批量部署的证书 ID。
func normalizeBatchDeployIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, i18n.E("api.error.batch_deploy_ids_required")
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
		return nil, i18n.E("api.error.batch_deploy_ids_required")
	}
	if len(out) > maxBatchDeployCertificates {
		return nil, i18n.E("api.error.batch_deploy_too_large", "max", maxBatchDeployCertificates)
	}
	return out, nil
}

func certificateDomains(cert *model.Certificate) []string {
	if cert == nil {
		return nil
	}
	out := make([]string, 0, len(cert.Domains)+1)
	out = append(out, cert.Domains...)
	if strings.TrimSpace(cert.PrimaryDomain) != "" {
		out = append(out, cert.PrimaryDomain)
	}
	return out
}
