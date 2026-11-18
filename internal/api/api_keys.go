package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

// API 密钥令牌使用固定前缀，便于认证中间件与普通会话 JWT 区分。
const (
	apiKeyTokenPrefix = "lactssl_"
	apiKeyHeader      = "X-API-Key"
	apiKeyContextKey  = "api_key"
	// apiKeyTouchWindow 限制最近使用时间的写库频率，避免每个请求都写 SQLite。
	apiKeyTouchWindow = time.Minute
)

// API 密钥权限。权限按“资源:动作”划分，敏感操作单独授权，
// 避免只读权限意外获得私钥、备份等高风险能力。
const (
	apiScopeAll               = "*"
	apiScopeProfileRead       = "profile:read"
	apiScopeSystemRead        = "system:read"
	apiScopeDashboardRead     = "dashboard:read"
	apiScopeCertificatesRead  = "certificates:read"
	apiScopeCertificatesWrite = "certificates:write"
	apiScopeCertificatesKey   = "certificates:key"
	// apiScopeCertificatesRequest 允许客户端提交 CSR，由服务端向权威 CA 完成
	// 域名验证与签发。该权限会消耗上游 CA 配额并触发 DNS 挑战写入，需单独授权。
	apiScopeCertificatesRequest = "certificates:request"
	apiScopeTasksRead           = "tasks:read"
	apiScopeTasksWrite          = "tasks:write"
	apiScopeTasksRun            = "tasks:run"
	apiScopeMonitorsRead        = "monitors:read"
	apiScopeMonitorsWrite       = "monitors:write"
	apiScopeDNSRead             = "dns_accounts:read"
	apiScopeDNSWrite            = "dns_accounts:write"
	apiScopeDeployAccountsRead  = "deploy_accounts:read"
	apiScopeDeployAccountsWrite = "deploy_accounts:write"
	apiScopeDeploymentsRead     = "deployments:read"
	// apiScopeDeployExecute 允许手动部署证书，或创建/运行包含部署节点的任务。
	// 本地/SSH 部署会执行服务端命令，因此必须与普通的证书/任务写权限区分开。
	apiScopeDeployExecute = "deploy:execute"
	// apiScopeDeployCommand 允许在本地/SSH 部署中执行 reload_command。
	// “执行部署”不应等同于“执行任意命令”，因此把命令执行单独拆出。
	apiScopeDeployCommand   = "deploy:command"
	apiScopeCARead          = "ca:read"
	apiScopeCAWrite         = "ca:write"
	apiScopePrivateCARead   = "private_cas:read"
	apiScopePrivateCAWrite  = "private_cas:write"
	apiScopePrivateCAKey    = "private_cas:key"
	apiScopePluginsRead     = "plugins:read"
	apiScopePluginsWrite    = "plugins:write"
	apiScopeLogsRead        = "logs:read"
	apiScopeSystemLogsRead  = "system_logs:read"
	apiScopeSystemLogsWrite = "system_logs:write"
	apiScopeSettingsRead    = "settings:read"
	apiScopeSettingsWrite   = "settings:write"
	apiScopeBackupRead      = "backup:read"
	apiScopeBackupDownload  = "backup:download"
	apiScopeBackupRestore   = "backup:restore"
	apiScopeUpgradeRead     = "upgrade:read"
	apiScopeUpgradeWrite    = "upgrade:write"
	apiScopeSecurityWrite   = "security:write"
	apiScopeAPIKeysRead     = "api_keys:read"
	apiScopeAPIKeysWrite    = "api_keys:write"
)

type apiKeyScopeDef struct {
	Value     string
	Group     string
	LabelKey  string
	DescKey   string
	Sensitive bool
}

var apiKeyScopeDefs = []apiKeyScopeDef{
	{apiScopeAll, "all", "api.scope.all.label", "api.scope.all.desc", true},
	{apiScopeProfileRead, "account", "api.scope.profile_read.label", "api.scope.profile_read.desc", false},
	{apiScopeSystemRead, "system", "api.scope.system_read.label", "api.scope.system_read.desc", false},
	{apiScopeDashboardRead, "system", "api.scope.dashboard_read.label", "api.scope.dashboard_read.desc", false},
	{apiScopeCertificatesRead, "certificates", "api.scope.certificates_read.label", "api.scope.certificates_read.desc", false},
	{apiScopeCertificatesWrite, "certificates", "api.scope.certificates_write.label", "api.scope.certificates_write.desc", false},
	{apiScopeCertificatesKey, "certificates", "api.scope.certificates_key.label", "api.scope.certificates_key.desc", true},
	{apiScopeCertificatesRequest, "certificates", "api.scope.certificates_request.label", "api.scope.certificates_request.desc", true},
	{apiScopeDeployExecute, "deploy", "api.scope.deploy_execute.label", "api.scope.deploy_execute.desc", true},
	{apiScopeDeployCommand, "deploy", "api.scope.deploy_command.label", "api.scope.deploy_command.desc", true},
	{apiScopeTasksRead, "tasks", "api.scope.tasks_read.label", "api.scope.tasks_read.desc", false},
	{apiScopeTasksWrite, "tasks", "api.scope.tasks_write.label", "api.scope.tasks_write.desc", false},
	{apiScopeTasksRun, "tasks", "api.scope.tasks_run.label", "api.scope.tasks_run.desc", false},
	{apiScopeMonitorsRead, "monitors", "api.scope.monitors_read.label", "api.scope.monitors_read.desc", false},
	{apiScopeMonitorsWrite, "monitors", "api.scope.monitors_write.label", "api.scope.monitors_write.desc", false},
	{apiScopeDNSRead, "credentials", "api.scope.dns_read.label", "api.scope.dns_read.desc", false},
	{apiScopeDNSWrite, "credentials", "api.scope.dns_write.label", "api.scope.dns_write.desc", true},
	{apiScopeDeployAccountsRead, "credentials", "api.scope.deploy_accounts_read.label", "api.scope.deploy_accounts_read.desc", false},
	{apiScopeDeployAccountsWrite, "credentials", "api.scope.deploy_accounts_write.label", "api.scope.deploy_accounts_write.desc", true},
	{apiScopeDeploymentsRead, "credentials", "api.scope.deployments_read.label", "api.scope.deployments_read.desc", false},
	{apiScopeCARead, "ca", "api.scope.ca_read.label", "api.scope.ca_read.desc", false},
	{apiScopeCAWrite, "ca", "api.scope.ca_write.label", "api.scope.ca_write.desc", true},
	{apiScopePrivateCARead, "private_ca", "api.scope.private_ca_read.label", "api.scope.private_ca_read.desc", false},
	{apiScopePrivateCAWrite, "private_ca", "api.scope.private_ca_write.label", "api.scope.private_ca_write.desc", true},
	{apiScopePrivateCAKey, "private_ca", "api.scope.private_ca_key.label", "api.scope.private_ca_key.desc", true},
	{apiScopePluginsRead, "plugins", "api.scope.plugins_read.label", "api.scope.plugins_read.desc", false},
	{apiScopePluginsWrite, "plugins", "api.scope.plugins_write.label", "api.scope.plugins_write.desc", true},
	{apiScopeLogsRead, "logs", "api.scope.logs_read.label", "api.scope.logs_read.desc", false},
	{apiScopeSystemLogsRead, "logs", "api.scope.system_logs_read.label", "api.scope.system_logs_read.desc", false},
	{apiScopeSystemLogsWrite, "logs", "api.scope.system_logs_write.label", "api.scope.system_logs_write.desc", true},
	{apiScopeSettingsRead, "settings", "api.scope.settings_read.label", "api.scope.settings_read.desc", false},
	{apiScopeSettingsWrite, "settings", "api.scope.settings_write.label", "api.scope.settings_write.desc", true},
	{apiScopeBackupRead, "backup", "api.scope.backup_read.label", "api.scope.backup_read.desc", false},
	{apiScopeBackupDownload, "backup", "api.scope.backup_download.label", "api.scope.backup_download.desc", true},
	{apiScopeBackupRestore, "backup", "api.scope.backup_restore.label", "api.scope.backup_restore.desc", true},
	{apiScopeUpgradeRead, "upgrade", "api.scope.upgrade_read.label", "api.scope.upgrade_read.desc", false},
	{apiScopeUpgradeWrite, "upgrade", "api.scope.upgrade_write.label", "api.scope.upgrade_write.desc", true},
	{apiScopeSecurityWrite, "security", "api.scope.security_write.label", "api.scope.security_write.desc", true},
	{apiScopeAPIKeysRead, "api_keys", "api.scope.api_keys_read.label", "api.scope.api_keys_read.desc", false},
	{apiScopeAPIKeysWrite, "api_keys", "api.scope.api_keys_write.label", "api.scope.api_keys_write.desc", true},
}

var apiKeyScopeValues = func() map[string]bool {
	out := make(map[string]bool, len(apiKeyScopeDefs))
	for _, def := range apiKeyScopeDefs {
		out[def.Value] = true
	}
	return out
}()

// apiKeyRoutePolicy 描述一个受保护接口对 API 密钥的开放策略。
// Scope 为空表示该接口只能通过浏览器登录会话调用。
type apiKeyRoutePolicy struct {
	Scope string
}

// apiKeyRoutePolicies 集中声明所有受保护接口的权限。
// 新增受保护路由时必须同时补充此表，TestAPIKeyPoliciesCoverAllRoutes 会强制校验。
var apiKeyRoutePolicies = map[string]apiKeyRoutePolicy{
	"GET /api/auth/me":                        {apiScopeProfileRead},
	"POST /api/auth/change-password":          {},
	"POST /api/auth/change-email":             {},
	"POST /api/auth/totp/setup":               {},
	"POST /api/auth/totp/enable":              {},
	"POST /api/auth/totp/disable":             {},
	"GET /api/auth/passkeys":                  {},
	"POST /api/auth/passkeys/register/begin":  {},
	"POST /api/auth/passkeys/register/finish": {},
	"POST /api/auth/passkeys/:id/delete":      {},

	"GET /api/sessions":                {},
	"DELETE /api/sessions/:id":         {},
	"POST /api/sessions/revoke-others": {},

	"GET /api/meta":      {apiScopeSystemRead},
	"GET /api/status":    {apiScopeSystemRead},
	"GET /api/dashboard": {apiScopeDashboardRead},

	"GET /api/plugins":                           {apiScopePluginsRead},
	"GET /api/plugins/icons":                     {apiScopePluginsRead},
	"POST /api/plugins/rescan":                   {apiScopePluginsWrite},
	"POST /api/plugins/upload":                   {apiScopePluginsWrite},
	"DELETE /api/plugins/files/:kind/:file":      {apiScopePluginsWrite},
	"PUT /api/plugins/files/:kind/:file":         {apiScopePluginsWrite},
	"GET /api/plugins/repo":                      {apiScopePluginsRead},
	"PUT /api/plugins/repo":                      {apiScopePluginsWrite},
	"POST /api/plugins/repo/refresh":             {apiScopePluginsWrite},
	"GET /api/plugins/repo/plugins":              {apiScopePluginsRead},
	"POST /api/plugins/repo/install":             {apiScopePluginsWrite},
	"POST /api/plugins/repo/update":              {apiScopePluginsWrite},
	"DELETE /api/plugins/repo/plugins/:kind/:id": {apiScopePluginsWrite},
	"GET /api/plugins/repo/legacy":               {apiScopePluginsRead},
	"POST /api/plugins/repo/migrate":             {apiScopePluginsWrite},
	"PUT /api/plugins/providers":                 {apiScopePluginsWrite},
	"PUT /api/plugins/providers/:id":             {apiScopePluginsWrite},
	"PUT /api/plugins/deployers":                 {apiScopePluginsWrite},
	"PUT /api/plugins/deployers/:id":             {apiScopePluginsWrite},
	"PUT /api/plugins/notify-channels":           {apiScopePluginsWrite},
	"PUT /api/plugins/notify-channels/:id":       {apiScopePluginsWrite},

	"GET /api/ca-configs":        {apiScopeCARead},
	"PUT /api/ca-configs/:value": {apiScopeCAWrite},
	"GET /api/acme-cas":          {apiScopeCARead},
	"POST /api/acme-cas":         {apiScopeCAWrite},
	"PUT /api/acme-cas/:id":      {apiScopeCAWrite},
	"DELETE /api/acme-cas/:id":   {apiScopeCAWrite},

	"GET /api/private-cas":              {apiScopePrivateCARead},
	"POST /api/private-cas":             {apiScopePrivateCAWrite},
	"POST /api/private-cas/import":      {apiScopePrivateCAWrite},
	"GET /api/private-cas/:id/download": {apiScopePrivateCARead},
	"DELETE /api/private-cas/:id":       {apiScopePrivateCAWrite},

	"GET /api/trusted-cas":         {apiScopeSettingsRead},
	"GET /api/trusted-cas/bundle":  {apiScopeSettingsRead},
	"POST /api/trusted-cas/import": {apiScopeSettingsWrite},
	"DELETE /api/trusted-cas/:id":  {apiScopeSettingsWrite},

	"GET /api/acme-endpoints":                    {apiScopeCARead},
	"POST /api/acme-endpoints":                   {apiScopeCAWrite},
	"PUT /api/acme-endpoints/:id":                {apiScopeCAWrite},
	"DELETE /api/acme-endpoints/:id":             {apiScopeCAWrite},
	"GET /api/acme-endpoints/:id/eab":            {apiScopeCARead},
	"POST /api/acme-endpoints/:id/eab":           {apiScopeCAWrite},
	"DELETE /api/acme-eab/:id":                   {apiScopeCAWrite},
	"GET /api/acme-accounts":                     {apiScopeCARead},
	"GET /api/acme-orders":                       {apiScopeCARead},
	"GET /api/acme-orders/:id":                   {apiScopeCARead},
	"POST /api/acme-diagnostics/cleanup":         {apiScopeCAWrite},
	"POST /api/acme-diagnostics/cleanup/preview": {apiScopeCARead},

	"GET /api/dns-accounts":             {apiScopeDNSRead},
	"GET /api/dns-accounts/:id":         {apiScopeDNSRead},
	"POST /api/dns-accounts":            {apiScopeDNSWrite},
	"POST /api/dns-accounts/verify":     {apiScopeDNSWrite},
	"POST /api/dns-accounts/:id/verify": {apiScopeDNSWrite},
	"PUT /api/dns-accounts/:id":         {apiScopeDNSWrite},
	"DELETE /api/dns-accounts/:id":      {apiScopeDNSWrite},

	"GET /api/deploy-accounts":             {apiScopeDeployAccountsRead},
	"GET /api/deploy-accounts/:id":         {apiScopeDeployAccountsRead},
	"POST /api/deploy-accounts":            {apiScopeDeployAccountsWrite},
	"POST /api/deploy-accounts/verify":     {apiScopeDeployAccountsWrite},
	"POST /api/deploy-accounts/:id/verify": {apiScopeDeployAccountsWrite},
	"PUT /api/deploy-accounts/:id":         {apiScopeDeployAccountsWrite},
	"DELETE /api/deploy-accounts/:id":      {apiScopeDeployAccountsWrite},

	"GET /api/tasks":                {apiScopeTasksRead},
	"POST /api/tasks":               {apiScopeTasksWrite},
	"PUT /api/tasks/:id":            {apiScopeTasksWrite},
	"DELETE /api/tasks/:id":         {apiScopeTasksWrite},
	"POST /api/tasks/:id/run":       {apiScopeTasksRun},
	"POST /api/tasks/batch/run":     {apiScopeTasksRun},
	"POST /api/tasks/batch/enabled": {apiScopeTasksWrite},
	"GET /api/tasks/:id/logs":       {apiScopeTasksRead},

	"GET /api/certificates":                         {apiScopeCertificatesRead},
	"POST /api/certificates/cleanup":                {apiScopeCertificatesWrite},
	"POST /api/certificates/cleanup/preview":        {apiScopeCertificatesRead},
	"POST /api/certificate-requests":                {apiScopeCertificatesRequest},
	"GET /api/certificate-requests":                 {apiScopeCertificatesRequest},
	"GET /api/certificate-requests/:id":             {apiScopeCertificatesRequest},
	"GET /api/certificate-requests/:id/certificate": {apiScopeCertificatesRequest},
	"POST /api/certificate-requests/:id/cancel":     {apiScopeCertificatesRequest},
	"POST /api/certificate-requests/:id/retry":      {apiScopeCertificatesRequest},
	"GET /api/certificates/:id":                     {apiScopeCertificatesRead},
	"GET /api/certificates/:id/analysis":            {apiScopeCertificatesRead},
	"GET /api/certificates/:id/download":            {apiScopeCertificatesRead},
	"GET /api/certificates/:id/pem":                 {apiScopeCertificatesRead},
	"POST /api/certificates/:id/deploy":             {apiScopeDeployExecute},
	"DELETE /api/certificates/:id":                  {apiScopeCertificatesWrite},

	"GET /api/deployments": {apiScopeDeploymentsRead},

	"GET /api/monitors":            {apiScopeMonitorsRead},
	"POST /api/monitors":           {apiScopeMonitorsWrite},
	"PUT /api/monitors/:id":        {apiScopeMonitorsWrite},
	"DELETE /api/monitors/:id":     {apiScopeMonitorsWrite},
	"POST /api/monitors/:id/check": {apiScopeMonitorsWrite},

	"GET /api/logs":               {apiScopeLogsRead},
	"GET /api/logs/export":        {apiScopeLogsRead},
	"GET /api/system-logs":        {apiScopeSystemLogsRead},
	"GET /api/system-logs/export": {apiScopeSystemLogsRead},
	"DELETE /api/system-logs":     {apiScopeSystemLogsWrite},

	"GET /api/settings":                  {apiScopeSettingsRead},
	"PUT /api/settings":                  {apiScopeSettingsWrite},
	"GET /api/upgrade":                   {apiScopeUpgradeRead},
	"POST /api/upgrade/run":              {apiScopeUpgradeWrite},
	"POST /api/upgrade/optimize":         {apiScopeUpgradeWrite},
	"GET /api/upgrade/program":           {apiScopeUpgradeRead},
	"POST /api/upgrade/program/check":    {apiScopeUpgradeRead},
	"POST /api/upgrade/program/apply":    {apiScopeUpgradeWrite},
	"POST /api/upgrade/program/restart":  {apiScopeUpgradeWrite},
	"GET /api/settings/backup":           {apiScopeBackupRead},
	"POST /api/settings/backup/download": {apiScopeBackupDownload},
	"POST /api/settings/backup/restore":  {apiScopeBackupRestore},
	"POST /api/settings/test-mail":       {apiScopeSettingsWrite},
	"POST /api/settings/test-channel":    {apiScopeSettingsWrite},
	"POST /api/security/entry":           {apiScopeSecurityWrite},

	"GET /api/api-keys":             {apiScopeAPIKeysRead},
	"GET /api/api-keys/scopes":      {apiScopeAPIKeysRead},
	"POST /api/api-keys":            {apiScopeAPIKeysWrite},
	"PUT /api/api-keys/:id":         {apiScopeAPIKeysWrite},
	"DELETE /api/api-keys/:id":      {apiScopeAPIKeysWrite},
	"POST /api/api-keys/:id/rotate": {apiScopeAPIKeysWrite},
}

// apiKeyFromContext 返回当前请求使用的 API 密钥；使用登录会话时返回 nil。
func apiKeyFromContext(c *gin.Context) *model.APIKey {
	value, exists := c.Get(apiKeyContextKey)
	if !exists {
		return nil
	}
	key, _ := value.(*model.APIKey)
	return key
}

// looksLikeAPIKey 判断令牌是否使用 API 密钥格式。
func looksLikeAPIKey(token string) bool {
	return strings.HasPrefix(token, apiKeyTokenPrefix)
}

// authenticateAPIKey 校验 API 密钥并写入认证上下文。
// 密钥无效时直接写响应并返回 false。
func (s *Server) authenticateAPIKey(c *gin.Context, token string) bool {
	now := time.Now().UTC()
	key, err := s.Store.GetAPIKeyByTokenHash(util.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		failKey(c, http.StatusUnauthorized, "api.error.api_key_invalid")
		return false
	}
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return false
	}
	if !key.Enabled {
		failKey(c, http.StatusUnauthorized, "api.error.api_key_disabled")
		return false
	}
	if key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
		failKey(c, http.StatusUnauthorized, "api.error.api_key_expired")
		return false
	}
	user, err := s.Store.GetUserByID(key.UserID)
	if err != nil {
		failKey(c, http.StatusUnauthorized, "api.error.api_key_invalid")
		return false
	}
	_ = s.Store.TouchAPIKey(key.ID, now, now.Add(-apiKeyTouchWindow))
	c.Set("user", user)
	c.Set(apiKeyContextKey, key)
	c.Set("session_id", "api-key:"+strconv.FormatInt(key.ID, 10))
	c.Set("session_strong_auth", false)
	return true
}

// authorizeAPIKey 在认证之后按集中路由表校验 API 密钥权限，
// 旧版 /<entry>/api 路径通过 normalizeAPIPath 复用同一份权限表。
func (s *Server) authorizeAPIKey(c *gin.Context) bool {
	key := apiKeyFromContext(c)
	if key == nil {
		return true
	}
	policy, found := apiKeyRoutePolicies[apiRouteKey(c)]
	if !found || policy.Scope == "" {
		failKey(c, http.StatusForbidden, "api.error.api_key_route_forbidden")
		return false
	}
	if !key.HasScope(policy.Scope) {
		failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", policy.Scope)
		return false
	}
	return true
}

// requireAPIKeyScope 用于同一路由内的敏感分支，例如证书下载的私钥类型。
// 登录会话不受影响，由 API 密钥权限模型保护。
func (s *Server) requireAPIKeyScope(c *gin.Context, scope string) bool {
	key := apiKeyFromContext(c)
	if key == nil {
		return true
	}
	if key.HasScope(scope) {
		return true
	}
	failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", scope)
	return false
}

// allowAPIKeyScopeAssignment 防止 API 密钥通过管理接口给自己或新密钥授予
// 超过自身权限的范围。浏览器登录会话代表账户本人，不受此限制。
func (s *Server) allowAPIKeyScopeAssignment(c *gin.Context, scopes []string) bool {
	key := apiKeyFromContext(c)
	if key == nil || key.HasScope(apiScopeAll) {
		return true
	}
	for _, scope := range scopes {
		if !key.HasScope(scope) {
			failKey(c, http.StatusForbidden, "api.error.api_key_scope_escalation", "scope", scope)
			return false
		}
	}
	return true
}

// allowManageAPIKey 防止 API 密钥修改、轮换或删除权限高于自身的密钥。
// 否则仅拥有 api_keys:write 的密钥可以轮换 "*" 密钥并拿到它的新令牌，
// 从而绕过权限范围限制。浏览器登录会话代表账户本人，不受此限制。
func (s *Server) allowManageAPIKey(c *gin.Context, target *model.APIKey) bool {
	if target == nil {
		return true
	}
	key := apiKeyFromContext(c)
	if key == nil || key.HasScope(apiScopeAll) {
		return true
	}
	for _, scope := range target.Scopes {
		if !key.HasScope(scope) {
			failKey(c, http.StatusForbidden, "api.error.api_key_target_forbidden")
			return false
		}
	}
	return true
}

// ---------------------------- API 密钥管理接口 ----------------------------

type apiKeyBody struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	Enabled   *bool    `json:"enabled"`
	ExpiresAt string   `json:"expires_at"`
}

func (s *Server) listAPIKeys(c *gin.Context) {
	user := getUser(c)
	list, err := s.Store.ListAPIKeys(user.ID)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

// apiKeyScopes 返回前端可展示的权限列表，文案按请求语言渲染。
func (s *Server) apiKeyScopes(c *gin.Context) {
	current := locale(c)
	groups := make([]gin.H, 0)
	seen := map[string]bool{}
	for _, def := range apiKeyScopeDefs {
		if !seen[def.Group] {
			seen[def.Group] = true
			groups = append(groups, gin.H{
				"value": def.Group,
				"label": i18n.T(current, "api.scope.group."+def.Group, nil),
			})
		}
	}
	list := make([]gin.H, 0, len(apiKeyScopeDefs))
	for _, def := range apiKeyScopeDefs {
		list = append(list, gin.H{
			"value":       def.Value,
			"group":       def.Group,
			"label":       i18n.T(current, def.LabelKey, nil),
			"description": i18n.T(current, def.DescKey, nil),
			"sensitive":   def.Sensitive,
		})
	}
	ok(c, gin.H{"scopes": list, "groups": groups})
}

func (s *Server) createAPIKey(c *gin.Context) {
	user := getUser(c)
	var body apiKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	name := strings.TrimSpace(body.Name)
	if err := validateAPIKeyName(name); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	scopes, err := normalizeAPIKeyScopes(body.Scopes)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if len(scopes) == 0 {
		failKey(c, http.StatusBadRequest, "api.error.api_key_scopes_required")
		return
	}
	if !s.allowAPIKeyScopeAssignment(c, scopes) {
		return
	}
	expires, err := parseAPIKeyExpiry(body.ExpiresAt, true)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.api_key_expiry_invalid")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	token, err := newAPIKeyToken()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	key := &model.APIKey{
		UserID:    user.ID,
		Name:      name,
		Prefix:    apiKeyPrefixOf(token),
		TokenHash: util.HashToken(token),
		Scopes:    scopes,
		Enabled:   enabled,
		ExpiresAt: expires,
	}
	if err := s.Store.CreateAPIKey(key); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("apikey", key.ID, "info", "log.api_key.created", map[string]any{"name": key.Name})
	ok(c, gin.H{"api_key": key, "token": token})
}

func (s *Server) updateAPIKey(c *gin.Context) {
	user := getUser(c)
	id, okID := parseID(c)
	if !okID {
		return
	}
	current, err := s.Store.GetAPIKey(user.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
		return
	}
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !s.allowManageAPIKey(c, current) {
		return
	}
	var body apiKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	name := strings.TrimSpace(body.Name)
	if err := validateAPIKeyName(name); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	scopes, err := normalizeAPIKeyScopes(body.Scopes)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if len(scopes) == 0 {
		failKey(c, http.StatusBadRequest, "api.error.api_key_scopes_required")
		return
	}
	if !s.allowAPIKeyScopeAssignment(c, scopes) {
		return
	}
	expires, err := parseAPIKeyExpiry(body.ExpiresAt, false)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.api_key_expiry_invalid")
		return
	}
	current.Name = name
	current.Scopes = scopes
	current.ExpiresAt = expires
	if body.Enabled != nil {
		current.Enabled = *body.Enabled
	}
	if err := s.Store.UpdateAPIKey(current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("apikey", current.ID, "info", "log.api_key.updated", map[string]any{"name": current.Name})
	ok(c, current)
}

func (s *Server) deleteAPIKey(c *gin.Context) {
	user := getUser(c)
	id, okID := parseID(c)
	if !okID {
		return
	}
	target, err := s.Store.GetAPIKey(user.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
		return
	}
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !s.allowManageAPIKey(c, target) {
		return
	}
	found, err := s.Store.DeleteAPIKey(user.ID, id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !found {
		failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
		return
	}
	s.Store.AddLogKey("apikey", id, "warning", "log.api_key.deleted", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func (s *Server) rotateAPIKey(c *gin.Context) {
	user := getUser(c)
	id, okID := parseID(c)
	if !okID {
		return
	}
	key, err := s.Store.GetAPIKey(user.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
		return
	}
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !s.allowManageAPIKey(c, key) {
		return
	}
	token, err := newAPIKeyToken()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if err := s.Store.RotateAPIKey(user.ID, id, apiKeyPrefixOf(token), util.HashToken(token)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			failKey(c, http.StatusNotFound, "api.error.api_key_not_found")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	key.Prefix = apiKeyPrefixOf(token)
	key.UpdatedAt = time.Now().UTC()
	s.Store.AddLogKey("apikey", key.ID, "warning", "log.api_key.rotated", map[string]any{"name": key.Name})
	ok(c, gin.H{"api_key": key, "token": token})
}

func newAPIKeyToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return apiKeyTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func apiKeyPrefixOf(token string) string {
	const visible = 8
	start := len(apiKeyTokenPrefix)
	if len(token) <= start+visible {
		return token
	}
	return token[:start+visible]
}

func validateAPIKeyName(name string) error {
	if name == "" {
		return i18n.E("api.error.api_key_name_required")
	}
	if len([]rune(name)) > 64 {
		return i18n.E("api.error.api_key_name_too_long")
	}
	return nil
}

func normalizeAPIKeyScopes(input []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		scope := strings.TrimSpace(raw)
		if scope == "" || seen[scope] {
			continue
		}
		if !apiKeyScopeValues[scope] {
			return nil, i18n.E("api.error.api_key_scope_invalid", "scope", scope)
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out, nil
}

func parseAPIKeyExpiry(value string, requireFuture bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	if requireFuture && !time.Now().Before(parsed) {
		return nil, errors.New("expiry must be in the future")
	}
	return &parsed, nil
}
