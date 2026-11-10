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

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		failKey(c, http.StatusBadRequest, "api.error.invalid_id")
		return 0, false
	}
	return id, true
}

func validateFields(fields []service.FieldDef, config map[string]string, current i18n.Locale) error {
	for _, f := range fields {
		if f.ShowIf != nil && config[f.ShowIf.Key] != f.ShowIf.Value {
			continue
		}
		if f.Required && strings.TrimSpace(config[f.Key]) == "" {
			return i18n.E("api.error.field_required", "field", f.Localized(current).Label)
		}
	}
	return nil
}

// ---------------------------- DNS 账户 ----------------------------

func (s *Server) listDNSAccounts(c *gin.Context) {
	list, err := s.Store.ListDNSAccounts()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) getDNSAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	item, err := s.Store.GetDNSAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	ok(c, item)
}

type dnsAccountBody struct {
	Name     string            `json:"name"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config"`
	Remark   string            `json:"remark"`
}

func (b *dnsAccountBody) validate(current i18n.Locale) error {
	b.Name = strings.TrimSpace(b.Name)
	b.Provider = strings.TrimSpace(b.Provider)
	if b.Name == "" {
		return i18n.E("api.error.dns_name_required")
	}
	def, found := service.DNSProviderInfo(b.Provider)
	if !found {
		return i18n.E("api.error.dns_provider_unsupported")
	}
	if b.Config == nil {
		b.Config = map[string]string{}
	}
	return validateFields(def.Fields, b.Config, current)
}

func (s *Server) createDNSAccount(c *gin.Context) {
	var body dnsAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.validate(locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	item := &model.DNSAccount{Name: body.Name, Provider: body.Provider, Config: body.Config, Remark: body.Remark}
	if err := s.Store.CreateDNSAccount(item); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("dns", item.ID, "info", "log.dns.created", map[string]any{"name": item.Name})
	ok(c, item)
}

func (s *Server) updateDNSAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var body dnsAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.validate(locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	item, err := s.Store.GetDNSAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	item.Name = body.Name
	item.Provider = body.Provider
	item.Config = body.Config
	item.Remark = body.Remark
	if err := s.Store.UpdateDNSAccount(item); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, item)
}

func (s *Server) deleteDNSAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	tasks, _ := s.Store.ListTasks()
	for _, t := range tasks {
		if t.DNSAccountID == id {
			failKey(c, http.StatusBadRequest, "api.error.dns_in_use", "name", t.Name)
			return
		}
	}
	if err := s.Store.DeleteDNSAccount(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

// ---------------------------- 目标凭据 ----------------------------

func (s *Server) listDeployAccounts(c *gin.Context) {
	list, err := s.Store.ListDeployAccounts()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) getDeployAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	item, err := s.Store.GetDeployAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.deploy_account_not_found")
		return
	}
	ok(c, item)
}

type deployAccountBody struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
	Remark string            `json:"remark"`
}

func (b *deployAccountBody) validate(current i18n.Locale) error {
	b.Name = strings.TrimSpace(b.Name)
	b.Type = strings.TrimSpace(b.Type)
	if b.Name == "" {
		return i18n.E("api.error.deploy_name_required")
	}
	def, found := service.DeployTypeInfo(b.Type)
	if !found {
		return i18n.E("api.error.deploy_type_unsupported")
	}
	if b.Config == nil {
		b.Config = map[string]string{}
	}
	if err := validateFields(def.Fields, b.Config, current); err != nil {
		return err
	}
	// 凭据只保存认证与连接信息；部署路径、站点、域名等参数由任务流程的部署节点保存。
	b.Config = service.FilterAuthConfig(def, b.Config)
	return nil
}

func (s *Server) createDeployAccount(c *gin.Context) {
	var body deployAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.validate(locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	item := &model.DeployAccount{Name: body.Name, Type: body.Type, Config: body.Config, Remark: body.Remark}
	if err := s.Store.CreateDeployAccount(item); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("deploy", item.ID, "info", "log.deploy.created", map[string]any{"name": item.Name})
	ok(c, item)
}

func (s *Server) updateDeployAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var body deployAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if err := body.validate(locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	item, err := s.Store.GetDeployAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.deploy_account_not_found")
		return
	}
	item.Name = body.Name
	item.Type = body.Type
	item.Config = body.Config
	item.Remark = body.Remark
	if err := s.Store.UpdateDeployAccount(item); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, item)
}

func (s *Server) deleteDeployAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	tasks, _ := s.Store.ListTasks()
	for _, t := range tasks {
		for _, node := range t.EffectivePipeline() {
			if node.Type == model.PipelineDeploy && node.Config.DeployAccountID == id {
				failKey(c, http.StatusBadRequest, "api.error.deploy_in_use", "name", t.Name)
				return
			}
		}
	}
	if err := s.Store.DeleteDeployAccount(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}
