package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/util"
)

// API 密钥令牌是 64 字节随机数的无填充 Base64URL 编码，共 86 个字符。
// 不携带固定前缀，认证中间件通过长度与字符集与会话 JWT 区分。
const (
	apiKeyTokenBytes  = 64
	apiKeyTokenLength = 86
	// legacyAPIKeyPrefix 仅为兼容旧版本已签发的密钥而保留；新密钥不再使用前缀。
	legacyAPIKeyPrefix = "lactssl_"
	apiKeyHeader       = "X-API-Key" // #nosec G101 -- API 协议头名称，非凭据
	apiKeyContextKey   = "api_key"
	// apiKeyTouchWindow 限制最近使用时间的写库频率，避免每个请求都写 SQLite。
	apiKeyTouchWindow = time.Minute
	// apiKeyMaxAllowedIPs / apiKeyMaxAllowedIPLen 限制白名单规模，避免单条密钥拖垮认证路径。
	apiKeyMaxAllowedIPs   = 64
	apiKeyMaxAllowedIPLen = 64
	// apiKeyMaxAllowedDNSAccounts 限制单个密钥可绑定的 DNS 账户数量。
	apiKeyMaxAllowedDNSAccounts = 200
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
	// apiScopeCertificatesRequest 允许客户端提交 CSR，由服务端向公共 CA 完成
	// 域名验证与签发。该权限会消耗上游 CA 配额并触发 DNS 挑战写入，需单独授权。
	apiScopeCertificatesRequest = "certificates:request"
	apiScopeTasksRead           = "tasks:read"
	apiScopeTasksWrite          = "tasks:write"
	apiScopeTasksRun            = "tasks:run"
	apiScopeMonitorsRead        = "monitors:read"
	apiScopeMonitorsWrite       = "monitors:write"
	apiScopeMonitorsInternal    = "monitors:internal"
	apiScopeDNSRead             = "dns_accounts:read"
	apiScopeDNSWrite            = "dns_accounts:write"
	apiScopeDeployAccountsRead  = "deploy_accounts:read"
	apiScopeDeployAccountsWrite = "deploy_accounts:write"
	apiScopeDeploymentsRead     = "deployments:read"
	apiScopeDeploymentsWrite    = "deployments:write"
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
	apiScopeLogsWrite       = "logs:write"
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
	apiScopeAPIKeysRead     = "api_keys:read" // #nosec G101 -- 权限范围常量，非凭据
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
	{apiScopeTasksWrite, "tasks", "api.scope.tasks_write.label", "api.scope.tasks_write.desc", true},
	{apiScopeTasksRun, "tasks", "api.scope.tasks_run.label", "api.scope.tasks_run.desc", true},
	{apiScopeMonitorsRead, "monitors", "api.scope.monitors_read.label", "api.scope.monitors_read.desc", false},
	{apiScopeMonitorsWrite, "monitors", "api.scope.monitors_write.label", "api.scope.monitors_write.desc", false},
	{apiScopeMonitorsInternal, "monitors", "api.scope.monitors_internal.label", "api.scope.monitors_internal.desc", true},
	{apiScopeDNSRead, "credentials", "api.scope.dns_read.label", "api.scope.dns_read.desc", false},
	{apiScopeDNSWrite, "credentials", "api.scope.dns_write.label", "api.scope.dns_write.desc", true},
	{apiScopeDeployAccountsRead, "credentials", "api.scope.deploy_accounts_read.label", "api.scope.deploy_accounts_read.desc", false},
	{apiScopeDeployAccountsWrite, "credentials", "api.scope.deploy_accounts_write.label", "api.scope.deploy_accounts_write.desc", true},
	{apiScopeDeploymentsRead, "credentials", "api.scope.deployments_read.label", "api.scope.deployments_read.desc", false},
	{apiScopeDeploymentsWrite, "credentials", "api.scope.deployments_write.label", "api.scope.deployments_write.desc", true},
	{apiScopeCARead, "ca", "api.scope.ca_read.label", "api.scope.ca_read.desc", false},
	{apiScopeCAWrite, "ca", "api.scope.ca_write.label", "api.scope.ca_write.desc", true},
	{apiScopePrivateCARead, "private_ca", "api.scope.private_ca_read.label", "api.scope.private_ca_read.desc", false},
	{apiScopePrivateCAWrite, "private_ca", "api.scope.private_ca_write.label", "api.scope.private_ca_write.desc", true},
	{apiScopePrivateCAKey, "private_ca", "api.scope.private_ca_key.label", "api.scope.private_ca_key.desc", true},
	{apiScopePluginsRead, "plugins", "api.scope.plugins_read.label", "api.scope.plugins_read.desc", false},
	{apiScopePluginsWrite, "plugins", "api.scope.plugins_write.label", "api.scope.plugins_write.desc", true},
	{apiScopeLogsRead, "logs", "api.scope.logs_read.label", "api.scope.logs_read.desc", false},
	{apiScopeLogsWrite, "logs", "api.scope.logs_write.label", "api.scope.logs_write.desc", true},
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
	"GET /api/ca-pools":          {apiScopeCARead},
	"POST /api/ca-pools":         {apiScopeCAWrite},
	"PUT /api/ca-pools/:id":      {apiScopeCAWrite},
	"DELETE /api/ca-pools/:id":   {apiScopeCAWrite},

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

	"GET /api/dns-accounts":                  {apiScopeDNSRead},
	"POST /api/dns-accounts/resolve":         {apiScopeDNSRead},
	"GET /api/dns-accounts/:id":              {apiScopeDNSRead},
	"GET /api/dns-accounts/:id/zones":        {apiScopeDNSRead},
	"POST /api/dns-accounts":                 {apiScopeDNSWrite},
	"POST /api/dns-accounts/verify":          {apiScopeDNSWrite},
	"POST /api/dns-accounts/:id/verify":      {apiScopeDNSWrite},
	"PUT /api/dns-accounts/:id/zones":        {apiScopeDNSWrite},
	"POST /api/dns-accounts/:id/zones/probe": {apiScopeDNSWrite},
	"PUT /api/dns-accounts/:id":              {apiScopeDNSWrite},
	"DELETE /api/dns-accounts/:id":           {apiScopeDNSWrite},

	"GET /api/deploy-accounts":              {apiScopeDeployAccountsRead},
	"GET /api/deploy-accounts/:id":          {apiScopeDeployAccountsRead},
	"POST /api/deploy-accounts":             {apiScopeDeployAccountsWrite},
	"POST /api/deploy-accounts/verify":      {apiScopeDeployAccountsWrite},
	"POST /api/deploy-accounts/:id/verify":  {apiScopeDeployAccountsWrite},
	"POST /api/deploy-accounts/:id/options": {apiScopeDeployAccountsWrite},
	"PUT /api/deploy-accounts/:id":          {apiScopeDeployAccountsWrite},
	"DELETE /api/deploy-accounts/:id":       {apiScopeDeployAccountsWrite},

	"GET /api/tasks":                      {apiScopeTasksRead},
	"POST /api/tasks":                     {apiScopeTasksWrite},
	"POST /api/tasks/target-flow/preview": {apiScopeDeployExecute},
	"PUT /api/tasks/:id":                  {apiScopeTasksWrite},
	"DELETE /api/tasks/:id":               {apiScopeTasksWrite},
	"POST /api/tasks/:id/run":             {apiScopeTasksRun},
	"POST /api/tasks/batch/run":           {apiScopeTasksRun},
	"POST /api/tasks/batch/enabled":       {apiScopeTasksWrite},
	"GET /api/tasks/:id/logs":             {apiScopeTasksRead},
	"DELETE /api/tasks/:id/logs":          {apiScopeTasksWrite},

	"GET /api/certificates":                         {apiScopeCertificatesRead},
	"POST /api/certificates/cleanup":                {apiScopeCertificatesWrite},
	"POST /api/certificates/cleanup/preview":        {apiScopeCertificatesRead},
	"POST /api/certificates/batch/deploy":           {apiScopeDeployExecute},
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

	"GET /api/deployments":    {apiScopeDeploymentsRead},
	"DELETE /api/deployments": {apiScopeDeploymentsWrite},

	"GET /api/monitors":            {apiScopeMonitorsRead},
	"POST /api/monitors":           {apiScopeMonitorsWrite},
	"PUT /api/monitors/:id":        {apiScopeMonitorsWrite},
	"DELETE /api/monitors/:id":     {apiScopeMonitorsWrite},
	"POST /api/monitors/:id/check": {apiScopeMonitorsWrite},

	"GET /api/logs":                      {apiScopeLogsRead},
	"GET /api/logs/export":               {apiScopeLogsRead},
	"DELETE /api/logs":                   {apiScopeLogsWrite},
	"GET /api/system-logs":               {apiScopeSystemLogsRead},
	"GET /api/system-logs/export":        {apiScopeSystemLogsRead},
	"DELETE /api/system-logs":            {apiScopeSystemLogsWrite},
	"GET /api/log-archives":              {},
	"GET /api/log-archives/:id":          {},
	"GET /api/log-archives/:id/download": {},
	"DELETE /api/log-archives/:id":       {},

	"GET /api/settings":          {apiScopeSettingsRead},
	"PUT /api/settings":          {apiScopeSettingsWrite},
	"GET /api/upgrade":           {apiScopeUpgradeRead},
	"POST /api/upgrade/run":      {apiScopeUpgradeWrite},
	"POST /api/upgrade/optimize": {apiScopeUpgradeWrite},
	"GET /api/upgrade/database":  {apiScopeUpgradeRead},
	// 数据库连通性测试会让服务端主动连接任意 PostgreSQL/MySQL DSN，属于出站操作，
	// 不能由只读 scope 触发。
	"POST /api/upgrade/database/test":                {apiScopeUpgradeWrite},
	"POST /api/upgrade/database/preflight":           {apiScopeUpgradeWrite},
	"POST /api/upgrade/database/convert":             {apiScopeUpgradeWrite},
	"GET /api/upgrade/database/convert":              {apiScopeUpgradeRead},
	"POST /api/upgrade/database/convert/cancel":      {apiScopeUpgradeWrite},
	"POST /api/upgrade/database/rollback":            {apiScopeUpgradeWrite},
	"GET /api/upgrade/program":                       {apiScopeUpgradeRead},
	"POST /api/upgrade/program/check":                {apiScopeUpgradeRead},
	"POST /api/upgrade/program/apply":                {apiScopeUpgradeWrite},
	"POST /api/upgrade/program/restart":              {apiScopeUpgradeWrite},
	"GET /api/settings/backup":                       {apiScopeBackupRead},
	"POST /api/settings/backup/download":             {apiScopeBackupDownload},
	"POST /api/settings/backup/restore":              {apiScopeBackupRestore},
	"GET /api/settings/config":                       {apiScopeSettingsRead},
	"PUT /api/settings/config":                       {},
	"GET /api/settings/config/proxy-hints":           {apiScopeSettingsRead},
	"GET /api/settings/config/backups":               {apiScopeSettingsRead},
	"POST /api/settings/config/backups":              {},
	"POST /api/settings/config/backups/:id/download": {},
	"POST /api/settings/config/backups/:id/restore":  {},
	"DELETE /api/settings/config/backups/:id":        {},
	"POST /api/settings/config/restart":              {},
	"POST /api/settings/test-mail":                   {apiScopeSettingsWrite},
	"POST /api/settings/test-channel":                {apiScopeSettingsWrite},
	"POST /api/settings/test-security-notify":        {apiScopeSettingsWrite},
	"GET /api/security/events":                       {},
	"POST /api/security/entry":                       {apiScopeSecurityWrite},

	"GET /api/api-keys":             {apiScopeAPIKeysRead},
	"GET /api/api-keys/scopes":      {apiScopeAPIKeysRead},
	"POST /api/api-keys":            {apiScopeAPIKeysWrite},
	"PUT /api/api-keys/:id":         {apiScopeAPIKeysWrite},
	"DELETE /api/api-keys/:id":      {apiScopeAPIKeysWrite},
	"POST /api/api-keys/:id/rotate": {apiScopeAPIKeysWrite},
}

// apiKeyAnyScopeRoutes 表示一个路由允许任意一个列出的 scope。
// 用于日志归档这类同时服务于操作日志与系统日志的分类资源。
var apiKeyAnyScopeRoutes = map[string][]string{
	"GET /api/log-archives":              {apiScopeLogsRead, apiScopeSystemLogsRead},
	"GET /api/log-archives/:id":          {apiScopeLogsRead, apiScopeSystemLogsRead},
	"GET /api/log-archives/:id/download": {apiScopeLogsRead, apiScopeSystemLogsRead},
	"DELETE /api/log-archives/:id":       {apiScopeLogsWrite, apiScopeSystemLogsWrite},
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
// 新密钥为 86 位 Base64URL 随机串；旧版本带 lactssl_ 前缀的密钥继续接受。
func looksLikeAPIKey(token string) bool {
	if strings.HasPrefix(token, legacyAPIKeyPrefix) {
		return true
	}
	if len(token) != apiKeyTokenLength {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil
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
	// IP 白名单为空表示不限制；非空时必须能识别客户端 IP，且至少命中一条规则。
	// 校验放在 Touch 之前，避免被拒绝的请求污染 last_used_at / last_used_ip。
	clientIP, hasClientIP := parseAPIKeyClientIP(s.clientIP(c))
	if len(key.AllowedIPs) > 0 {
		if !hasClientIP {
			failKey(c, http.StatusForbidden, "api.error.api_key_ip_unavailable")
			return false
		}
		if !apiKeyIPAllowed(key, clientIP) {
			s.logAPIKeyIPDenied(c, key, clientIP)
			failKey(c, http.StatusForbidden, "api.error.api_key_ip_denied")
			return false
		}
	}
	lastUsedIP := ""
	if hasClientIP {
		lastUsedIP = clientIP.String()
	}
	_ = s.Store.TouchAPIKey(key.ID, now, now.Add(-apiKeyTouchWindow), lastUsedIP)
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
	route := apiRouteKey(c)
	if scopes, ok := apiKeyAnyScopeRoutes[route]; ok {
		for _, scope := range scopes {
			if key.HasScope(scope) {
				return true
			}
		}
		failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", strings.Join(scopes, "|"))
		return false
	}
	policy, found := apiKeyRoutePolicies[route]
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

// allowAPIKeyIPAssignment 防止 API 密钥给自己或新密钥授予比自身更宽的 IP 白名单。
// 浏览器登录会话代表账户本人，不受此限制。
func (s *Server) allowAPIKeyIPAssignment(c *gin.Context, allowedIPs []string) bool {
	actor := apiKeyFromContext(c)
	if actor == nil || len(actor.AllowedIPs) == 0 {
		return true
	}
	if !apiKeyIPPolicySubset(actor.AllowedIPs, allowedIPs) {
		failKey(c, http.StatusForbidden, "api.error.api_key_ip_escalation")
		return false
	}
	return true
}

// allowManageAPIKeyIP 防止受 IP 限制的密钥管理或轮换 IP 范围更宽的密钥。
// 尤其是轮换会直接返回目标密钥的新令牌，必须阻止受限密钥借它绕过白名单。
func (s *Server) allowManageAPIKeyIP(c *gin.Context, target *model.APIKey) bool {
	if target == nil {
		return true
	}
	actor := apiKeyFromContext(c)
	if actor == nil || len(actor.AllowedIPs) == 0 {
		return true
	}
	if !apiKeyIPPolicySubset(actor.AllowedIPs, target.AllowedIPs) {
		failKey(c, http.StatusForbidden, "api.error.api_key_ip_target_forbidden")
		return false
	}
	return true
}

// allowAPIKeyDNSAccountAssignment 防止受限密钥把 DNS 账户范围扩大到超出自身。
// 空 actor 集合表示不限制；空 target 集合表示不限制，受限 actor 不能创建无限制密钥。
func (s *Server) allowAPIKeyDNSAccountAssignment(c *gin.Context, ids []int64) bool {
	actor := apiKeyFromContext(c)
	if actor == nil || !actor.DNSAccountRestricted() {
		return true
	}
	if len(ids) == 0 {
		failKey(c, http.StatusForbidden, "api.error.api_key_dns_escalation")
		return false
	}
	for _, id := range ids {
		if !actor.AllowsDNSAccount(id) {
			failKey(c, http.StatusForbidden, "api.error.api_key_dns_escalation")
			return false
		}
	}
	return true
}

// allowManageAPIKeyDNS 防止受限密钥管理、轮换或删除 DNS 范围更宽的密钥。
func (s *Server) allowManageAPIKeyDNS(c *gin.Context, target *model.APIKey) bool {
	if target == nil {
		return true
	}
	actor := apiKeyFromContext(c)
	if actor == nil || !actor.DNSAccountRestricted() {
		return true
	}
	if !target.DNSAccountRestricted() {
		failKey(c, http.StatusForbidden, "api.error.api_key_dns_target_forbidden")
		return false
	}
	for _, id := range target.AllowedDNSAccountIDs {
		if !actor.AllowsDNSAccount(id) {
			failKey(c, http.StatusForbidden, "api.error.api_key_dns_target_forbidden")
			return false
		}
	}
	return true
}

// apiKeyIPPolicySubset 判断 target 白名单是否完全落在 actor 白名单内。
// 空 actor 表示不限制，可以管理任意策略；空 target 表示不限制，不能被受限 actor 管理。
func apiKeyIPPolicySubset(actor, target []string) bool {
	if len(actor) == 0 {
		return true
	}
	if len(target) == 0 {
		return false
	}
	actorPrefixes := make([]netip.Prefix, 0, len(actor))
	for _, rule := range actor {
		if prefix, ok := apiKeyRulePrefix(rule); ok {
			actorPrefixes = append(actorPrefixes, prefix)
		}
	}
	if len(actorPrefixes) == 0 {
		return false
	}
	for _, rule := range target {
		targetPrefix, ok := apiKeyRulePrefix(rule)
		if !ok {
			return false
		}
		contained := false
		for _, actorPrefix := range actorPrefixes {
			if prefixContainsPrefix(actorPrefix, targetPrefix) {
				contained = true
				break
			}
		}
		if !contained {
			return false
		}
	}
	return true
}

// prefixContainsPrefix 判断 outer 是否完整包含 inner；不同地址族不互相包含。
func prefixContainsPrefix(outer, inner netip.Prefix) bool {
	if outer.Addr().Is4() != inner.Addr().Is4() {
		return false
	}
	return outer.Bits() <= inner.Bits() && outer.Masked().Contains(inner.Masked().Addr())
}

// apiKeyRulePrefix 把单 IP 或 CIDR 规则统一成可比较的前缀。
func apiKeyRulePrefix(rule string) (netip.Prefix, bool) {
	value := strings.TrimSpace(rule)
	if value == "" {
		return netip.Prefix{}, false
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, false
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return netip.Prefix{}, false
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96).Masked()
		}
		return prefix, true
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, false
	}
	if addr.Zone() != "" {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	bits := 128
	if addr.Is4() {
		bits = 32
	}
	return netip.PrefixFrom(addr, bits), true
}

// normalizeAPIKeyAllowedIPs 校验并规范化管理接口提交的白名单。
// 单个 IP 与 CIDR 都接受；空列表表示不限制，重复项会被去掉。
func normalizeAPIKeyAllowedIPs(input []string) ([]string, error) {
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if len(value) > apiKeyMaxAllowedIPLen {
			return nil, i18n.E("api.error.api_key_ip_invalid", "value", value)
		}
		var normalized string
		if strings.Contains(value, "/") {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, i18n.E("api.error.api_key_ip_invalid", "value", value)
			}
			prefix = prefix.Masked()
			if prefix.Addr().Is4In6() {
				if prefix.Bits() < 96 {
					return nil, i18n.E("api.error.api_key_ip_invalid", "value", value)
				}
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96).Masked()
			}
			// 明确拒绝“全放行”前缀，避免用户误以为配置了限制；需要不限制时留空列表。
			if prefix.Bits() == 0 {
				return nil, i18n.E("api.error.api_key_ip_all_disallowed")
			}
			normalized = prefix.String()
		} else {
			addr, err := netip.ParseAddr(value)
			if err != nil || addr.Zone() != "" {
				return nil, i18n.E("api.error.api_key_ip_invalid", "value", value)
			}
			normalized = addr.Unmap().String()
		}
		if len(normalized) > apiKeyMaxAllowedIPLen {
			return nil, i18n.E("api.error.api_key_ip_invalid", "value", value)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	if len(out) > apiKeyMaxAllowedIPs {
		return nil, i18n.E("api.error.api_key_ip_limit", "max", apiKeyMaxAllowedIPs)
	}
	sort.Strings(out)
	return out, nil
}

// apiKeyIPAllowed 判断客户端 IP 是否命中密钥白名单；空白名单表示不限制。
func apiKeyIPAllowed(key *model.APIKey, addr netip.Addr) bool {
	if key == nil || len(key.AllowedIPs) == 0 {
		return true
	}
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, rule := range key.AllowedIPs {
		prefix, ok := apiKeyRulePrefix(rule)
		if !ok {
			continue
		}
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseAPIKeyClientIP 兼容 host:port、纯 IPv6、[IPv6]:port 与带 zone 的地址。
func parseAPIKeyClientIP(raw string) (netip.Addr, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if index := strings.LastIndexByte(value, '%'); index >= 0 {
		value = value[:index]
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// effectiveClientIP 返回用于界面展示的客户端 IP，解析失败时保留原始值。
func (s *Server) effectiveClientIP(c *gin.Context) string {
	raw := strings.TrimSpace(s.clientIP(c))
	if addr, ok := parseAPIKeyClientIP(raw); ok {
		return addr.String()
	}
	return raw
}

// logAPIKeyIPDenied 写入被拒绝的来源 IP 审计日志；同一密钥限频，避免日志洪水。
func (s *Server) logAPIKeyIPDenied(c *gin.Context, key *model.APIKey, addr netip.Addr) {
	if key == nil {
		return
	}
	if allowed, _ := s.allow(ratelimit.APIKeyIPDenied, "apikey-ip-denied:"+strconv.FormatInt(key.ID, 10)); !allowed {
		return
	}
	path := c.FullPath()
	if path == "" {
		path = s.logPath(c)
	}
	s.Store.AddLogKey("apikey", key.ID, "warning", "log.api_key.ip_denied", map[string]any{
		"ip": addr.String(),
	})
	result := s.clientIPResult(c)
	s.logger().Warn("API 密钥来源 IP 被拒绝",
		"key_id", key.ID,
		"ip", addr.String(),
		"ip_source", string(result.Source),
		"ip_skipped", result.Skipped,
		"path", path,
		"request_id", c.GetString(requestIDKey),
	)
}

// ---------------------------- API 密钥管理接口 ----------------------------

type apiKeyBody struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	Enabled   *bool    `json:"enabled"`
	ExpiresAt string   `json:"expires_at"`
	// AllowedIPs 使用指针区分“未提供”（保持原值）与“显式提交空数组”（取消限制）。
	AllowedIPs *[]string `json:"allowed_ips"`
	// AllowedDNSAccountIDs 同理：未提供时继承调用者策略或保持原值。
	AllowedDNSAccountIDs *[]int64 `json:"allowed_dns_account_ids"`
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
	result := s.clientIPResult(c)
	peer := requestPeerIP(c.Request.RemoteAddr)
	peerValue := ""
	peerTrusted := false
	if peer.IsValid() {
		peerValue = peer.String()
		peerTrusted = s.trustedProxies().Contains(peer)
	}
	xff := splitHeaderList(c.GetHeader("X-Forwarded-For"), 8, 64)
	xRealIP := truncateHeader(c.GetHeader("X-Real-IP"), 64)
	hasForwarded := len(xff) > 0 || xRealIP != "" ||
		strings.TrimSpace(c.GetHeader("Forwarded")) != "" ||
		strings.TrimSpace(c.GetHeader("CF-Connecting-IP")) != ""
	warnings := result.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	ok(c, gin.H{
		"scopes":             list,
		"groups":             groups,
		"client_ip":          s.effectiveClientIP(c),
		"client_ip_source":   string(result.Source),
		"client_ip_warnings": warnings,
		"peer_ip":            peerValue,
		"peer_trusted":       peerTrusted,
		"forwarded_for":      xff,
		"x_real_ip":          xRealIP,
		"forwarded_proto":    truncateHeader(c.GetHeader("X-Forwarded-Proto"), 32),
		"has_forwarded":      hasForwarded,
	})
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
	// 未显式提供白名单时：浏览器会话默认不限制；API 密钥调用继承调用者策略，
	// 避免受限密钥通过“创建新密钥”得到一个无 IP 限制的令牌。
	rawAllowedIPs := []string{}
	if body.AllowedIPs != nil {
		rawAllowedIPs = *body.AllowedIPs
	} else if actor := apiKeyFromContext(c); actor != nil {
		rawAllowedIPs = actor.AllowedIPs
	}
	allowedIPs, err := normalizeAPIKeyAllowedIPs(rawAllowedIPs)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if !s.allowAPIKeyIPAssignment(c, allowedIPs) {
		return
	}
	// 未显式提供时：浏览器会话默认不限制；受限 API 密钥继承调用者集合，
	// 避免受限密钥通过创建新密钥获得更大的 DNS 账户范围。
	rawDNSAccounts := []int64{}
	if body.AllowedDNSAccountIDs != nil {
		rawDNSAccounts = *body.AllowedDNSAccountIDs
	} else if actor := apiKeyFromContext(c); actor != nil {
		rawDNSAccounts = actor.AllowedDNSAccountIDs
	}
	allowedDNSAccounts, err := normalizeAPIKeyDNSAccountIDs(rawDNSAccounts)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if body.AllowedDNSAccountIDs != nil {
		if err := s.validateAPIKeyDNSAccountIDs(allowedDNSAccounts); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
	}
	if !s.allowAPIKeyDNSAccountAssignment(c, allowedDNSAccounts) {
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
		UserID:               user.ID,
		Name:                 name,
		Prefix:               apiKeyPrefixOf(token),
		TokenHash:            util.HashToken(token),
		Scopes:               scopes,
		AllowedIPs:           allowedIPs,
		AllowedDNSAccountIDs: allowedDNSAccounts,
		Enabled:              enabled,
		ExpiresAt:            expires,
	}
	if err := s.Store.CreateAPIKey(key); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("apikey", key.ID, "info", "log.api_key.created", map[string]any{"name": key.Name})
	s.notifySecurityEvent(c, user.ID, model.SecurityEventAPIKeyCreated, map[string]string{
		"name":   key.Name,
		"prefix": key.Prefix,
	})
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
	if !s.allowManageAPIKeyIP(c, current) {
		return
	}
	if !s.allowManageAPIKeyDNS(c, current) {
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
	// 未提供 allowed_ips 时保留原值，显式空数组表示取消限制。
	allowedIPs := current.AllowedIPs
	if body.AllowedIPs != nil {
		normalized, err := normalizeAPIKeyAllowedIPs(*body.AllowedIPs)
		if err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if !s.allowAPIKeyIPAssignment(c, normalized) {
			return
		}
		allowedIPs = normalized
	}
	allowedDNSAccounts := current.AllowedDNSAccountIDs
	if body.AllowedDNSAccountIDs != nil {
		normalized, err := normalizeAPIKeyDNSAccountIDs(*body.AllowedDNSAccountIDs)
		if err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if err := s.validateAPIKeyDNSAccountIDs(normalized); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if !s.allowAPIKeyDNSAccountAssignment(c, normalized) {
			return
		}
		allowedDNSAccounts = normalized
	}
	expires, err := parseAPIKeyExpiry(body.ExpiresAt, false)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.api_key_expiry_invalid")
		return
	}
	current.Name = name
	current.Scopes = scopes
	current.AllowedIPs = allowedIPs
	current.AllowedDNSAccountIDs = allowedDNSAccounts
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
	s.notifySecurityEvent(c, user.ID, model.SecurityEventAPIKeyUpdated, map[string]string{
		"name":   current.Name,
		"prefix": current.Prefix,
	})
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
	if !s.allowManageAPIKeyIP(c, target) {
		return
	}
	if !s.allowManageAPIKeyDNS(c, target) {
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
	s.notifySecurityEvent(c, user.ID, model.SecurityEventAPIKeyDeleted, map[string]string{
		"name":   target.Name,
		"prefix": target.Prefix,
	})
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
	if !s.allowManageAPIKeyIP(c, key) {
		return
	}
	if !s.allowManageAPIKeyDNS(c, key) {
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
	s.notifySecurityEvent(c, user.ID, model.SecurityEventAPIKeyRotated, map[string]string{
		"name":   key.Name,
		"prefix": key.Prefix,
	})
	ok(c, gin.H{"api_key": key, "token": token})
}

func newAPIKeyToken() (string, error) {
	buf := make([]byte, apiKeyTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func apiKeyPrefixOf(token string) string {
	const visible = 8
	if len(token) <= visible {
		return token
	}
	return token[:visible]
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

// normalizeAPIKeyDNSAccountIDs 去重、排序并限制 DNS 账户集合规模。
func normalizeAPIKeyDNSAccountIDs(input []int64) ([]int64, error) {
	seen := make(map[int64]bool, len(input))
	out := make([]int64, 0, len(input))
	for _, id := range input {
		if id <= 0 {
			return nil, i18n.E("api.error.api_key_dns_account_invalid", "id", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > apiKeyMaxAllowedDNSAccounts {
		return nil, i18n.E("api.error.api_key_dns_account_too_many", "max", apiKeyMaxAllowedDNSAccounts)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// validateAPIKeyDNSAccountIDs 在保存时确认账户存在，尽早发现拼写或过期 ID。
func (s *Server) validateAPIKeyDNSAccountIDs(ids []int64) error {
	for _, id := range ids {
		if _, err := s.Store.GetDNSAccount(id); err != nil {
			return i18n.E("api.error.api_key_dns_account_invalid", "id", id)
		}
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
