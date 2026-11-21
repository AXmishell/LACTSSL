package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
)

// apiKeyAllowsDNSAccount 判断当前 API 密钥是否可以使用指定 DNS 账户。
// 浏览器会话与非受限密钥始终返回 true。
func apiKeyAllowsDNSAccount(c *gin.Context, accountID int64) bool {
	key := apiKeyFromContext(c)
	return key == nil || key.AllowsDNSAccount(accountID)
}

// requireAPIKeyDNSAccountUse 在签发、任务运行等“实际使用账户”的入口执行收紧。
// 拒绝时返回 403，并写审计日志；调用方无法通过枚举 ID 判断账户是否存在。
func (s *Server) requireAPIKeyDNSAccountUse(c *gin.Context, accountID int64) bool {
	if accountID <= 0 || apiKeyAllowsDNSAccount(c, accountID) {
		return true
	}
	s.logAPIKeyDNSDenied(c, accountID)
	failKey(c, http.StatusForbidden, "api.error.api_key_dns_account_denied", "account_id", accountID)
	return false
}

// requireAPIKeyDNSAccountsUse 对多账户证书的每个账户执行同样的范围校验。
func (s *Server) requireAPIKeyDNSAccountsUse(c *gin.Context, accountIDs []int64) bool {
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if !s.requireAPIKeyDNSAccountUse(c, accountID) {
			return false
		}
	}
	return true
}

// denyIfAPIKeyDNSAccountHidden 用于 DNS 账户管理接口：不允许的账户统一按
// 不存在处理，避免受限密钥通过 403/404 差异枚举管理员保存的账户。
func (s *Server) denyIfAPIKeyDNSAccountHidden(c *gin.Context, accountID int64) bool {
	if apiKeyAllowsDNSAccount(c, accountID) {
		return false
	}
	s.logAPIKeyDNSDenied(c, accountID)
	failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
	return true
}

// filterDNSBindingsForAPIKey 把自动解析范围限制在密钥允许的账户内。
// 这样受限密钥既看不到其他账户的 zone，也不会被解析到无权使用的账户。
func filterDNSBindingsForAPIKey(c *gin.Context, bindings []model.DNSZoneBinding) []model.DNSZoneBinding {
	key := apiKeyFromContext(c)
	if key == nil || !key.DNSAccountRestricted() {
		return bindings
	}
	out := make([]model.DNSZoneBinding, 0, len(bindings))
	for _, binding := range bindings {
		if key.AllowsDNSAccount(binding.DNSAccountID) {
			out = append(out, binding)
		}
	}
	return out
}

// filterDNSAccountsForAPIKey 让受限密钥的账户列表只显示自己可用的账户。
func filterDNSAccountsForAPIKey(c *gin.Context, accounts []model.DNSAccount) []model.DNSAccount {
	key := apiKeyFromContext(c)
	if key == nil || !key.DNSAccountRestricted() {
		return accounts
	}
	out := make([]model.DNSAccount, 0, len(accounts))
	for _, account := range accounts {
		if key.AllowsDNSAccount(account.ID) {
			out = append(out, account)
		}
	}
	return out
}

func (s *Server) logAPIKeyDNSDenied(c *gin.Context, accountID int64) {
	key := apiKeyFromContext(c)
	if key == nil {
		return
	}
	if allowed, _ := s.allow(ratelimit.APIKeyDNSDenied, "apikey-dns-denied:"+strconv.FormatInt(key.ID, 10)); !allowed {
		return
	}
	path := c.FullPath()
	if path == "" {
		path = s.logPath(c)
	}
	s.Store.AddLogKey("apikey", key.ID, "warning", "log.api_key.dns_denied", map[string]any{
		"account_id": accountID,
	})
	s.logger().Warn("API 密钥尝试使用未授权的 DNS 账户",
		"key_id", key.ID,
		"account_id", accountID,
		"path", path,
		"request_id", c.GetString(requestIDKey),
	)
}
