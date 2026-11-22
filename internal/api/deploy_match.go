package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/deploy"
	"lactssl/internal/service"
)

type deployMatchBody struct {
	Domains []string          `json:"domains"`
	Config  map[string]string `json:"config"`
}

// matchDeployAccountObjects 按证书域名匹配目标系统中的网站等部署对象。
// 任务流程的部署节点用它自动选中与申请域名相同的网站名称；
// 匹配规则与批量部署共用，避免界面和实际部署使用两套判断。
func (s *Server) matchDeployAccountObjects(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	item, err := s.Store.GetDeployAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.deploy_account_not_found")
		return
	}
	var body deployMatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	domains := service.NormalizeDeployDomains(body.Domains)
	if len(domains) == 0 || len(domains) > 100 {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}

	def, found := service.DeployTypeInfo(item.Type)
	if !found {
		failKey(c, http.StatusBadRequest, "api.error.deploy_type_unsupported")
		return
	}
	deployConfig := service.FilterDeployConfig(def, service.MergeDeployConfig(def, item.Config, body.Config))
	if service.DeployObjectIsSingleton(def, deployConfig) {
		ok(c, gin.H{"field": "", "multiple": false, "values": []string{}})
		return
	}
	fieldKey, multiple, _, found := service.DeployObjectFieldInfo(def, deployConfig)
	if !found {
		ok(c, gin.H{"field": "", "multiple": false, "values": []string{}})
		return
	}

	plan, err := s.Manager.PlanDeployObjects(c.Request.Context(), item, deployConfig, string(locale(c)))
	if err != nil {
		if !errors.Is(err, deployplugin.ErrPlanUnsupported) {
			failKey(c, http.StatusBadRequest, "api.error.batch_deploy_plan_failed", "error", service.SanitizeErrorText(err.Error()))
			return
		}
		// 旧版插件可能只实现 options。网站名称通常就是域名，可直接按名称匹配。
		objects, err := s.deployObjectsFromOptions(c, item, def, fieldKey, deployConfig)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.deploy_options_failed", "error", service.SanitizeErrorText(err.Error()))
			return
		}
		plan.Objects = objects
	} else if plan.Multiple {
		multiple = true
	}

	values := service.DeployObjectValues(service.MatchDeployObjects(plan.Objects, nil, domains))
	if values == nil {
		values = []string{}
	}
	ok(c, gin.H{"field": fieldKey, "multiple": multiple, "values": values})
}

// deployObjectsFromOptions 把动态选项当作只有名称的部署对象。
// 仅用于没有 plan 能力的旧插件；新插件通过 plan 返回真实域名绑定。
func (s *Server) deployObjectsFromOptions(
	c *gin.Context,
	item *model.DeployAccount,
	def service.ProviderDef,
	fieldKey string,
	config map[string]string,
) ([]service.DeployObject, error) {
	field, found := deployOptionField(def, fieldKey)
	if !found || field.OptionsSource == "" {
		return []service.DeployObject{}, nil
	}
	options, err := deployplugin.ListOptions(c.Request.Context(), item.Type, deployplugin.OptionsRequest{
		Field:  field.Key,
		Source: field.OptionsSource,
		Auth:   service.FilterAuthConfig(def, item.Config),
		Config: config,
	})
	if err != nil {
		if errors.Is(err, deployplugin.ErrOptionsUnsupported) {
			return []service.DeployObject{}, nil
		}
		return nil, err
	}
	objects := make([]service.DeployObject, 0, len(options))
	for _, option := range options {
		objects = append(objects, service.DeployObject{Value: option.Value, Name: option.Label})
	}
	return objects, nil
}
