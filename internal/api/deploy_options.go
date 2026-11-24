package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	deployplugin "lactssl/internal/plugin/host/deploy"
	"lactssl/internal/service"
)

type deployOptionsBody struct {
	Field  string            `json:"field"`
	Query  string            `json:"query"`
	Config map[string]string `json:"config"`
}

// listDeployAccountOptions 使用已保存的目标凭据读取远端可选项。
// 典型场景是宝塔面板：先读取网站或 Docker 网站列表，再由界面下拉选择，
// 避免用户手工输入网站名称。
func (s *Server) listDeployAccountOptions(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	item, err := s.Store.GetDeployAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.deploy_account_not_found")
		return
	}
	var body deployOptionsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	body.Field = strings.TrimSpace(body.Field)
	body.Query = strings.TrimSpace(body.Query)
	if body.Field == "" || len(body.Query) > 128 {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}

	def, found := service.DeployTypeInfo(item.Type)
	if !found {
		failKey(c, http.StatusBadRequest, "api.error.deploy_type_unsupported")
		return
	}
	field, found := deployOptionField(def, body.Field)
	if !found || field.OptionsSource == "" {
		failKey(c, http.StatusBadRequest, "api.error.deploy_option_field_invalid")
		return
	}

	auth := service.FilterAuthConfig(def, item.Config)
	config := service.MergeDeployConfig(def, item.Config, body.Config)
	options, err := deployplugin.ListOptions(c.Request.Context(), item.Type, deployplugin.OptionsRequest{
		Field:  field.Key,
		Source: field.OptionsSource,
		Query:  body.Query,
		Auth:   auth,
		Config: config,
	})
	if errors.Is(err, deployplugin.ErrOptionsUnsupported) {
		failKey(c, http.StatusBadRequest, "api.error.deploy_options_unsupported")
		return
	}
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.deploy_options_failed", "error", service.SanitizeErrorText(err.Error()))
		return
	}
	if options == nil {
		options = []deployplugin.ManifestOption{}
	}
	ok(c, gin.H{"options": options})
}

func deployOptionField(def service.ProviderDef, key string) (service.FieldDef, bool) {
	for _, field := range def.DeployFields {
		if field.Key == key {
			return field, true
		}
	}
	for _, field := range def.Fields {
		if field.Key == key {
			return field, true
		}
	}
	return service.FieldDef{}, false
}
