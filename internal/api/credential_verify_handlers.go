package api

import (
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

// ---------------------------- 凭据可用性验证 ----------------------------

type verifyDNSBody struct {
	// 已保存账户验证时只使用数据库中的凭据，Provider/Config 会被忽略；
	// 未保存配置验证时要求显式提交完整凭据（包括密钥字段）。
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config"`
	Domain   string            `json:"domain"`
	Mode     string            `json:"mode"`
	Password string            `json:"password"`
	TOTP     string            `json:"totp"`
}

type verifyDeployBody struct {
	Type         string            `json:"type"`
	Config       map[string]string `json:"config"`
	DeployConfig map[string]string `json:"deploy_config"`
	Mode         string            `json:"mode"`
	Password     string            `json:"password"`
	TOTP         string            `json:"totp"`
}

// verifyGuard 统一处理验证接口的权限：API Key 必须有对应写权限，
// 浏览器会话必须再认证。验证会使用已保存的密钥发起出站请求，
// 与 test-mail 属于同一类敏感操作。
func (s *Server) verifyGuard(c *gin.Context, password, totp, scope string) bool {
	if !s.requireAPIKeyScope(c, scope) {
		return false
	}
	if apiKeyFromContext(c) != nil {
		return true
	}
	return s.requireReauth(c, getUser(c), password, totp)
}

func (s *Server) verifyRateLimit(c *gin.Context, key string) bool {
	if allowed, wait := s.limiter.Allow(key, 5, time.Minute); !allowed {
		rateLimited(c, wait)
		return false
	}
	if allowed, wait := s.limiter.Allow("verify:client:"+clientRateKey(c), 20, time.Minute); !allowed {
		rateLimited(c, wait)
		return false
	}
	return true
}

// requireExplicitSecrets 阻止“未提交密钥 + 复用已保存密钥”的验证方式。
// 攻击者若能修改 endpoint/host 并借用已存密钥，就能把凭据发到自己的服务器；
// 允许验证未保存配置的前提是调用方本来就知道这些密钥。
func requireExplicitSecrets(def service.ProviderDef, cfg map[string]string, loc i18n.Locale) error {
	for _, field := range def.Fields {
		if !field.IsSecret() || !field.VisibleIn(def.Fields, cfg) {
			continue
		}
		if strings.TrimSpace(cfg[field.Key]) == "" {
			return i18n.E("api.error.verify_secret_required", "field", field.Label)
		}
	}
	return nil
}

func verifyStatusOf(result service.VerifyResult) string {
	unsupported := false
	for _, check := range result.Checks {
		switch check.Status {
		case service.VerifyStatusFailed, service.VerifyStatusError:
			return "failed"
		case service.VerifyStatusUnsupported:
			unsupported = true
		}
	}
	if result.OK {
		return "ok"
	}
	if unsupported {
		return "unsupported"
	}
	return "failed"
}

// defaultDNSVerifyDomain 在用户未填写测试域名时，取引用该账户的第一个任务域名。
func (s *Server) defaultDNSVerifyDomain(accountID int64) string {
	tasks, err := s.Store.ListTasks()
	if err != nil {
		return ""
	}
	for _, task := range tasks {
		if task.DNSAccountID != accountID {
			continue
		}
		if len(task.Domains) > 0 {
			return task.Domains[0]
		}
	}
	return ""
}

func (s *Server) verifyDNSAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var body verifyDNSBody
	if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyGuard(c, body.Password, body.TOTP, apiScopeDNSWrite) {
		return
	}
	if !s.verifyRateLimit(c, "verify:dns:"+strconv.FormatInt(id, 10)) {
		return
	}
	account, err := s.Store.GetDNSAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
		return
	}
	domain := strings.TrimSpace(body.Domain)
	if domain == "" {
		domain = s.defaultDNSVerifyDomain(account.ID)
	}
	result := s.Manager.VerifyDNSAccount(c.Request.Context(), locale(c), strconv.FormatInt(account.ID, 10), account.Provider, account.Config, domain, body.Mode)
	s.persistDNSVerify(account, result)
	ok(c, result)
}

func (s *Server) verifyDNSAccountConfig(c *gin.Context) {
	var body verifyDNSBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyGuard(c, body.Password, body.TOTP, apiScopeDNSWrite) {
		return
	}
	provider := strings.TrimSpace(body.Provider)
	if !s.verifyRateLimit(c, "verify:dns:new:"+provider+":"+clientRateKey(c)) {
		return
	}
	def, found := service.DNSProviderInfo(provider)
	if !found {
		failKey(c, http.StatusBadRequest, "api.error.dns_provider_unsupported")
		return
	}
	cfg := body.Config
	if cfg == nil {
		cfg = map[string]string{}
	}
	merged := service.MergeProviderConfig(def, nil, cfg)
	if err := requireExplicitSecrets(def, merged, locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if err := validateFields(def.Fields, merged, locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	filtered := service.FilterAuthConfig(def, merged)
	result := s.Manager.VerifyDNSAccount(c.Request.Context(), locale(c), "", provider, filtered, body.Domain, body.Mode)
	ok(c, result)
}

func (s *Server) persistDNSVerify(account *model.DNSAccount, result service.VerifyResult) {
	status := verifyStatusOf(result)
	var at *time.Time
	if !result.VerifiedAt.IsZero() {
		verifiedAt := result.VerifiedAt
		at = &verifiedAt
	}
	message := truncateText(result.Message, 800)
	if err := s.Store.SetDNSAccountVerification(account.ID, status, message, result.Mode, at); err != nil {
		s.logger().Warn("保存 DNS 账户验证结果失败", "account", account.ID, "error", err)
	}
	if result.OK {
		s.Store.AddLogKey("dns", account.ID, "info", "log.dns.verify_success", map[string]any{"name": account.Name, "mode": result.Mode})
		return
	}
	s.Store.AddLogKey("dns", account.ID, "warning", "log.dns.verify_failed", map[string]any{"name": account.Name, "message": message})
}

func (s *Server) verifyDeployAccount(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var body verifyDeployBody
	if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyGuard(c, body.Password, body.TOTP, apiScopeDeployAccountsWrite) {
		return
	}
	if !s.verifyRateLimit(c, "verify:deploy:"+strconv.FormatInt(id, 10)) {
		return
	}
	account, err := s.Store.GetDeployAccount(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.deploy_account_not_found")
		return
	}
	deployCfg := body.DeployConfig
	if def, found := service.DeployTypeInfo(account.Type); found {
		deployCfg = service.FilterDeployConfig(def, deployCfg)
	}
	result := s.Manager.VerifyDeployAccount(c.Request.Context(), locale(c), account.Type, account.Config, deployCfg, body.Mode, account)
	s.persistDeployVerify(account, result)
	ok(c, result)
}

func (s *Server) verifyDeployAccountConfig(c *gin.Context) {
	var body verifyDeployBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.verifyGuard(c, body.Password, body.TOTP, apiScopeDeployAccountsWrite) {
		return
	}
	accountType := strings.TrimSpace(body.Type)
	if !s.verifyRateLimit(c, "verify:deploy:new:"+accountType+":"+clientRateKey(c)) {
		return
	}
	def, found := service.DeployTypeInfo(accountType)
	if !found {
		failKey(c, http.StatusBadRequest, "api.error.deploy_type_unsupported")
		return
	}
	cfg := body.Config
	if cfg == nil {
		cfg = map[string]string{}
	}
	merged := service.MergeProviderConfig(def, nil, cfg)
	if err := requireExplicitSecrets(def, merged, locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if err := validateFields(def.Fields, merged, locale(c)); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	filtered := service.FilterAuthConfig(def, merged)
	deployCfg := service.FilterDeployConfig(def, body.DeployConfig)
	result := s.Manager.VerifyDeployAccount(c.Request.Context(), locale(c), accountType, filtered, deployCfg, body.Mode, nil)
	ok(c, result)
}

func (s *Server) persistDeployVerify(account *model.DeployAccount, result service.VerifyResult) {
	status := verifyStatusOf(result)
	var at *time.Time
	if !result.VerifiedAt.IsZero() {
		verifiedAt := result.VerifiedAt
		at = &verifiedAt
	}
	message := truncateText(result.Message, 800)
	if err := s.Store.SetDeployAccountVerification(account.ID, status, message, result.Mode, at); err != nil {
		s.logger().Warn("保存目标凭据验证结果失败", "account", account.ID, "error", err)
	}
	if result.OK {
		s.Store.AddLogKey("deploy", account.ID, "info", "log.deploy.verify_success", map[string]any{"name": account.Name, "mode": result.Mode})
		return
	}
	s.Store.AddLogKey("deploy", account.ID, "warning", "log.deploy.verify_failed", map[string]any{"name": account.Name, "message": message})
}

// 验证结果会写入账户表；更新配置时清空旧结论，避免“验证通过”指向旧凭据。
func resetDNSVerifyIfChanged(item *model.DNSAccount, provider string, cfg map[string]string) {
	if item.Provider == provider && maps.Equal(item.Config, cfg) {
		return
	}
	item.VerifyStatus = ""
	item.VerifyMessage = ""
	item.VerifyMode = ""
	item.VerifiedAt = nil
}

func resetDeployVerifyIfChanged(item *model.DeployAccount, accountType string, cfg map[string]string) {
	if item.Type == accountType && maps.Equal(item.Config, cfg) {
		return
	}
	item.VerifyStatus = ""
	item.VerifyMessage = ""
	item.VerifyMode = ""
	item.VerifiedAt = nil
}
