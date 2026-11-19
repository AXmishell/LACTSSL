package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

const maxBatchDeployCertificates = 100

type batchDeployBody struct {
	CertificateIDs []int64            `json:"certificate_ids"`
	Targets        []deployTargetBody `json:"targets"`
}

type preparedDeployTarget struct {
	account *model.DeployAccount
	config  map[string]string
}

type preparedDeployPlan struct {
	cert    *model.Certificate
	targets []preparedDeployTarget
}

// batchDeployCertificates 批量部署多张证书。
//
// 批量部署共享同一组目标凭据，但站点类目标（宝塔、1Panel、AcePanel 等）
// 必须按证书域名逐张解析：显式填写的站点只会保留与当前证书域名匹配的项，
// 留空时由插件回退到证书域名。任何一张证书与站点不匹配，或两张证书会写入
// 同一个站点时，都在执行前直接拒绝，避免后一张证书覆盖前一张。
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

	plans := make([]preparedDeployPlan, 0, len(ids))
	// siteOwners 以“目标凭据 + 实际站点”为键，拦截同一站点被多张证书覆盖。
	siteOwners := make(map[string]string)
	commandChecked := false
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
		taskConfigs := s.taskDeployConfigs(cert)
		// 不同证书所属任务的部署参数可能不同，必须逐张检查命令权限。
		if !commandChecked && s.manualDeployUsesCommand(body.Targets, taskConfigs) {
			if !s.requireAPIKeyScope(c, apiScopeDeployCommand) {
				return
			}
			commandChecked = true
		}
		domains := certificateDomains(cert)
		plan := preparedDeployPlan{cert: cert, targets: make([]preparedDeployTarget, 0, len(body.Targets))}
		for _, target := range body.Targets {
			account, err := s.Store.GetDeployAccount(target.DeployAccountID)
			if err != nil {
				failKey(c, http.StatusBadRequest, "api.error.deploy_account_not_found")
				return
			}
			def, found := service.DeployTypeInfo(account.Type)
			if !found {
				failKey(c, http.StatusBadRequest, "api.error.deploy_type_unsupported")
				return
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
			_, objects, err := service.FilterBatchDeployObjects(def, deployCfg, domains)
			if err != nil {
				failErr(c, http.StatusBadRequest, err)
				return
			}
			if len(objects) > 0 {
				identity := strconv.FormatInt(account.ID, 10) + "|" + strings.Join(objects, ",")
				if owner, exists := siteOwners[identity]; exists {
					failKey(c, http.StatusConflict, "api.error.batch_deploy_site_conflict",
						"sites", strings.Join(objects, ", "),
						"certs", owner+" / "+cert.PrimaryDomain)
					return
				}
				siteOwners[identity] = cert.PrimaryDomain
			}
			plan.targets = append(plan.targets, preparedDeployTarget{account: account, config: deployCfg})
		}
		plans = append(plans, plan)
	}

	results := make([]gin.H, 0, len(plans)*len(body.Targets))
	for _, plan := range plans {
		for _, target := range plan.targets {
			result := s.deployToTarget(c, plan.cert, target.account, target.config)
			result["certificate_id"] = plan.cert.ID
			result["domain"] = plan.cert.PrimaryDomain
			results = append(results, result)
		}
	}
	ok(c, gin.H{"results": results})
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
