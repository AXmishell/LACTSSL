package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
	pluginproto "lactssl/internal/plugin/proto"
	"lactssl/internal/util"
)

// 验证检查项状态。ok 表示通过；failed 表示确定失败；unsupported 表示
// 当前插件版本不支持这项检查；skipped 表示按条件跳过（例如未提供部署路径）。
const (
	VerifyStatusOK          = "ok"
	VerifyStatusFailed      = "failed"
	VerifyStatusUnsupported = "unsupported"
	VerifyStatusSkipped     = "skipped"
	VerifyStatusError       = "error"
)

// VerifyCheck 是一项可用性检查结果。
type VerifyCheck struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// VerifyResult 是一次凭据可用性验证的汇总结果。
type VerifyResult struct {
	OK         bool          `json:"ok"`
	Mode       string        `json:"mode"`
	Checks     []VerifyCheck `json:"checks"`
	Message    string        `json:"message,omitempty"`
	VerifiedAt time.Time     `json:"verified_at"`
	loc        i18n.Locale
}

func newVerifyResult(mode string, loc i18n.Locale) *VerifyResult {
	return &VerifyResult{Mode: mode, VerifiedAt: time.Now().UTC(), loc: loc}
}

// tlsVerifyErrorMarkers 是 Go 标准库在证书校验失败时输出的稳定片段。
// 插件返回的消息已经过本地化包装，但底层 x509/tls 错误仍是英文原文，
// 因此宿主无法还原结构化错误，只能在展示边界按文本归一化。
//
// 具体原因必须分开提示：导入内部 CA 只能解决“证书不受信任”，
// 无法解决证书过期、尚未生效或主机名/IP 不匹配。
var (
	tlsExpiredMarkers = []string{
		"certificate has expired",
		"certificate is not yet valid",
	}
	tlsHostnameMarkers = []string{
		"certificate is not valid for any names",
		"certificate is valid for",
		"doesn't contain any ip sans",
		"cannot validate certificate for",
	}
	tlsUntrustedMarkers = []string{
		"certificate signed by unknown authority",
		"unknown authority",
		"certificate issuer has been marked as not permitted",
		"certificate is not authorized to sign other certificates",
	}
	tlsGenericMarkers = []string{
		"tls: failed to verify certificate",
		"x509: cannot validate certificate",
	}
)

// tlsVerifyMessageKey 返回匹配到的证书校验失败原因；未匹配时返回空字符串。
func tlsVerifyMessageKey(message string) string {
	text := strings.ToLower(message)
	matches := func(markers []string) bool {
		for _, marker := range markers {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	switch {
	case matches(tlsExpiredMarkers):
		return "service.verify.tls_expired"
	case matches(tlsHostnameMarkers):
		return "service.verify.tls_hostname_mismatch"
	case matches(tlsUntrustedMarkers):
		return "service.verify.tls_untrusted"
	case matches(tlsGenericMarkers):
		return "service.verify.tls_failed"
	default:
		return ""
	}
}

// normalizeVerifyMessage 把冗长的 TLS 证书校验错误收敛为可操作的简短提示，
// 避免把面板地址与 x509 细节直接展示给用户。
func normalizeVerifyMessage(loc i18n.Locale, message string) string {
	if key := tlsVerifyMessageKey(message); key != "" {
		return i18n.T(loc, key, nil)
	}
	return message
}

func (r *VerifyResult) add(key, status, message string) {
	r.Checks = append(r.Checks, VerifyCheck{Key: key, Status: status, Message: normalizeVerifyMessage(r.loc, message)})
}

// finish 汇总结果：全部检查通过（允许 skipped）时才算成功。
// 失败时把第一条可读消息作为整体 message，方便列表页直接展示。
func (r *VerifyResult) finish() {
	r.OK = true
	for _, check := range r.Checks {
		switch check.Status {
		case VerifyStatusFailed, VerifyStatusError, VerifyStatusUnsupported:
			r.OK = false
			if r.Message == "" {
				r.Message = check.Message
			}
		}
	}
}

// VerifyModeNormalize 把接口传入的模式归一化。
// auto 表示优先只读探测，插件不支持时回退到 DNS 写测试；read 只做只读；
// write 直接执行写测试。
func VerifyModeNormalize(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read":
		return "read"
	case "write":
		return "write"
	default:
		return "auto"
	}
}

// NormalizeVerifyDomain 清洗验证用域名：去通配符、去尾点、拒绝 IP 与非法格式。
func NormalizeVerifyDomain(input string) string {
	domains := util.NormalizeDomains([]string{input})
	if len(domains) == 0 {
		return ""
	}
	domain := strings.TrimPrefix(domains[0], "*.")
	if domain == "" || net.ParseIP(domain) != nil || !util.ValidDomain(domain) {
		return ""
	}
	return domain
}

// VerifyDNSAccount 验证 DNS 凭据：优先让 helper 在单个进程内写入随机 TXT
// 并立即删除；旧 helper 未声明 verify-write-v1 时回退到 present/cleanup。
// lockKey 用于按账户串行化，避免两次写测试同时操作同一 zone。
func (m *Manager) VerifyDNSAccount(ctx context.Context, loc i18n.Locale, lockKey, provider string, cfg map[string]string, domain, mode string) (result VerifyResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	mode = VerifyModeNormalize(mode)
	res := newVerifyResult(mode, loc)
	defer func() {
		res.finish()
		result = *res
	}()

	plugin, found := dnsplugin.Lookup(provider)
	if !found {
		res.add("init", VerifyStatusUnsupported, i18n.T(loc, "service.verify.dns_provider_unsupported", map[string]any{"provider": provider}))
		return *res
	}
	domain = NormalizeVerifyDomain(domain)
	if domain == "" {
		// 没有可用测试域名时（例如账户尚未维护域名绑定），不做写入测试，
		// 只确认插件可用、凭据可以初始化，并保留调用权限检查结果。
		// 显式要求 write 模式时仍返回域名错误，避免把“跳过”伪装成通过。
		if mode == "write" {
			res.add("domain", VerifyStatusFailed, i18n.T(loc, "service.verify.domain_invalid", nil))
			return *res
		}
		res.Mode = "scope"
		if _, err := plugin.New(cfg); err != nil {
			res.add("init", VerifyStatusFailed, i18n.T(loc, "service.verify.init_failed", map[string]any{"error": err.Error()}))
			return *res
		}
		res.add("init", VerifyStatusOK, "")
		res.add("domain", VerifyStatusSkipped, i18n.T(loc, "service.verify.domain_skipped", nil))
		res.add("access", VerifyStatusOK, i18n.T(loc, "service.verify.permission_only", nil))
		res.Message = i18n.T(loc, "service.verify.permission_only", nil)
		return *res
	}
	res.add("domain", VerifyStatusOK, domain)

	// 优先使用 helper 的只读 verify；未声明能力的旧插件在 auto 模式回退到
	// 写测试，显式 read 模式则明确返回不支持。
	if mode == "read" || mode == "auto" {
		if verifier, ok := plugin.(dnsplugin.Verifier); ok {
			res.Mode = "read"
			response, err := verifier.Verify(ctx, cfg, domain, "read", string(loc))
			if err == nil {
				// helper 声明了能力但该服务商实际返回 unsupported 时，
				// auto 模式仍回退到写测试，避免把“不支持”当成最终结论。
				if !onlyUnsupportedChecks(response.Checks) || mode == "read" {
					appendVerifyChecks(res, response.Checks, response.OK)
					return *res
				}
			} else if !errors.Is(err, dnsplugin.ErrVerifyUnsupported) {
				res.add("read", VerifyStatusFailed, i18n.T(loc, "service.verify.read_failed", map[string]any{"error": err.Error()}))
				return *res
			}
		}
		if mode == "read" {
			res.add("read", VerifyStatusUnsupported, i18n.T(loc, "service.verify.read_unsupported", nil))
			return *res
		}
	}

	// auto 回退或显式 write：优先让 helper 在同一个进程内完成写入与清理，
	// 复用 provider 实例、鉴权结果与连接。旧 helper 未声明该能力时返回
	// ErrVerifyUnsupported，继续走下面的 present/cleanup 兼容路径。
	if verifier, ok := plugin.(dnsplugin.Verifier); ok {
		if err := ctx.Err(); err != nil {
			res.add("write", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
			return *res
		}
		res.Mode = "write"
		var response pluginproto.VerifyResponse
		var err error
		// 与兼容写测试共用同一把账户锁，避免并发验证同时操作同一 zone。
		func() {
			if lockKey != "" {
				unlock := m.dnsLocks.Lock(lockKey)
				defer unlock()
			}
			response, err = verifier.Verify(ctx, cfg, domain, "write", string(loc))
		}()
		if err == nil && !onlyUnsupportedChecks(response.Checks) {
			appendVerifyChecks(res, response.Checks, response.OK)
			return *res
		}
		if err != nil && !errors.Is(err, dnsplugin.ErrVerifyUnsupported) {
			res.add("write", VerifyStatusFailed, i18n.T(loc, "service.verify.write_failed", map[string]any{"error": err.Error()}))
			return *res
		}
		// err 为 ErrVerifyUnsupported，或 helper 声明能力但实际只返回
		// unsupported 检查项时，继续走下面的兼容写测试。
	}

	res.Mode = "write"
	instance, err := plugin.New(cfg)
	if err != nil {
		res.add("init", VerifyStatusFailed, i18n.T(loc, "service.verify.init_failed", map[string]any{"error": err.Error()}))
		return *res
	}
	res.add("init", VerifyStatusOK, "")

	if err := ctx.Err(); err != nil {
		res.add("write", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
		return *res
	}
	if lockKey != "" {
		unlock := m.dnsLocks.Lock(lockKey)
		defer unlock()
	}

	token, keyAuth, err := util.VerificationToken()
	if err != nil {
		res.add("write", VerifyStatusError, i18n.T(loc, "service.verify.token_failed", map[string]any{"error": err.Error()}))
		return *res
	}
	if err := instance.Present(domain, token, keyAuth); err != nil {
		res.add("write", VerifyStatusFailed, i18n.T(loc, "service.verify.write_failed", map[string]any{"error": err.Error()}))
		return *res
	}
	res.add("write", VerifyStatusOK, "")

	// 清理必须使用独立于请求的上下文：客户端断开后仍然要删除临时记录。
	cleanupErr := instance.CleanUp(domain, token, keyAuth)
	if cleanupErr != nil {
		res.add("cleanup", VerifyStatusFailed, i18n.T(loc, "service.verify.cleanup_failed", map[string]any{"error": cleanupErr.Error(), "domain": domain}))
		return *res
	}
	res.add("cleanup", VerifyStatusOK, "")
	return *res
}

// appendVerifyChecks 把 helper 返回的检查项写入结果。
// helper 明确返回 ok=false 但没有任何失败项时补一条汇总失败，避免误判为通过。
// onlyUnsupportedChecks 判断 helper 是否只返回了 unsupported/skipped 检查项。
func onlyUnsupportedChecks(checks []pluginproto.VerifyCheck) bool {
	unsupported := false
	for _, check := range checks {
		switch check.Status {
		case VerifyStatusUnsupported:
			unsupported = true
		case VerifyStatusSkipped:
		default:
			return false
		}
	}
	return unsupported
}

func appendVerifyChecks(res *VerifyResult, checks []pluginproto.VerifyCheck, ok bool) {
	for _, check := range checks {
		res.add(check.Key, check.Status, check.Message)
	}
	if len(checks) == 0 {
		status := VerifyStatusOK
		if !ok {
			status = VerifyStatusFailed
		}
		res.add("verify", status, "")
	}
}

// VerifyDeployAccount 验证目标凭据。deployCfg 为空时只做凭据级检查；
// 传入部署节点参数时，会额外检查该站点/目录/桶的权限（阶段 2 的 L2 验证）。
func (m *Manager) VerifyDeployAccount(ctx context.Context, loc i18n.Locale, accountType string, auth, deployCfg map[string]string, mode string, account *model.DeployAccount) (result VerifyResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	mode = VerifyModeNormalize(mode)
	res := newVerifyResult(mode, loc)
	defer func() {
		res.finish()
		result = *res
	}()

	def, found := DeployTypeInfo(accountType)
	if !found {
		res.add("init", VerifyStatusUnsupported, i18n.T(loc, "service.verify.deploy_type_unsupported", map[string]any{"type": accountType}))
		return *res
	}
	// 与真实部署保持一致：认证字段 + 部署节点参数合并后再判断。
	merged := MergeDeployConfig(def, auth, deployCfg)
	res.add("init", VerifyStatusOK, "")

	hasScope := deployCfg != nil && len(deployCfg) > 0
	if hasScope {
		if err := validateRequiredDeployFields(def, merged); err != nil {
			res.add("config", VerifyStatusFailed, i18n.ErrorText(loc, err))
			return *res
		}
		res.add("config", VerifyStatusOK, "")
	}

	if err := ctx.Err(); err != nil {
		res.add("connect", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
		return *res
	}

	switch strings.ToLower(strings.TrimSpace(def.Value)) {
	case "local":
		verifyLocalDeploy(m, loc, res, merged)
	case "ssh":
		verifySSHDeploy(ctx, m, loc, res, merged, account)
	default:
		// 与 DeployContext 保持一致：插件只接收过滤后的认证字段，
		// 部署节点参数单独放在 Config 中。
		verifyPluginDeploy(ctx, loc, res, def.Value, accountType, FilterAuthConfig(def, auth), merged, account)
	}
	return *res
}

func validateRequiredDeployFields(def ProviderDef, cfg map[string]string) error {
	for _, field := range def.DeployFields {
		if !field.VisibleIn(def.DeployFields, cfg) {
			continue
		}
		if field.Required && strings.TrimSpace(cfg[field.Key]) == "" {
			return i18n.E("service.verify.field_required", "field", field.Label)
		}
	}
	return nil
}

// verifyLocalDeploy 检查本地目录是否可写：在目标目录或最近的已存在父目录
// 创建并删除一个临时文件。不写入证书路径本身，也不执行 reload_command。
func verifyLocalDeploy(m *Manager, loc i18n.Locale, res *VerifyResult, cfg map[string]string) {
	certPath := strings.TrimSpace(cfg["cert_path"])
	keyPath := strings.TrimSpace(cfg["key_path"])
	if certPath == "" && keyPath == "" {
		// 本地部署没有独立凭据；未提供部署路径时只说明无法做目录检查，
		// 真正的路径权限留给任务部署节点的 L2 验证。
		res.add("scope", VerifyStatusSkipped, i18n.T(loc, "service.verify.local_no_paths", nil))
		return
	}
	if certPath == "" || keyPath == "" {
		res.add("config", VerifyStatusFailed, i18n.T(loc, "service.verify.paths_required", nil))
		return
	}
	if filepath.Clean(certPath) == filepath.Clean(keyPath) {
		res.add("config", VerifyStatusFailed, i18n.T(loc, "service.verify.paths_conflict", nil))
		return
	}
	if err := validateLocalDeployPaths(m, certPath, keyPath); err != nil {
		res.add("config", VerifyStatusFailed, i18n.ErrorText(loc, err))
		return
	}
	res.Mode = "write"
	res.add("scope", VerifyStatusOK, "")
	checked := map[string]bool{}
	for _, target := range []string{certPath, keyPath} {
		dir := filepath.Dir(target)
		if checked[dir] {
			continue
		}
		checked[dir] = true
		existing, created := nearestExistingDir(dir)
		if existing == "" {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.dir_unavailable", map[string]any{"dir": dir}))
			continue
		}
		file, err := os.CreateTemp(existing, ".lactssl-verify-*")
		if err != nil {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.dir_unwritable", map[string]any{"dir": dir, "error": err.Error()}))
			continue
		}
		name := file.Name()
		_ = file.Close()
		if err := os.Remove(name); err != nil {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.temp_cleanup_failed", map[string]any{"file": name, "error": err.Error()}))
			continue
		}
		message := ""
		if created {
			message = i18n.T(loc, "service.verify.dir_will_create", map[string]any{"dir": dir})
		}
		res.add("permission", VerifyStatusOK, message)
	}
}

// nearestExistingDir 返回最近的已存在目录；布尔值表示目标目录本身是否缺失。
func nearestExistingDir(dir string) (string, bool) {
	original := dir
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return "", dir != original
			}
			return dir, dir != original
		}
		if !os.IsNotExist(err) {
			return "", dir != original
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", dir != original
		}
		dir = parent
	}
}

// verifySSHDeploy 连接并认证 SSH，再用 SFTP 在目标目录创建/删除临时文件。
// 全程不执行远程 shell 命令，避免把验证变成命令执行入口。
func verifySSHDeploy(ctx context.Context, m *Manager, loc i18n.Locale, res *VerifyResult, cfg map[string]string, account *model.DeployAccount) {
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := dialSSHDeploy(m, cfg, account)
	if err != nil {
		res.add("connect", VerifyStatusFailed, i18n.ErrorText(loc, err))
		return
	}
	defer client.Close()
	res.add("connect", VerifyStatusOK, "")
	res.add("auth", VerifyStatusOK, "")

	clientSFTP, err := sftp.NewClient(client)
	if err != nil {
		res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.sftp_failed", map[string]any{"error": err.Error()}))
		return
	}
	defer clientSFTP.Close()

	certPath := strings.TrimSpace(cfg["cert_path"])
	keyPath := strings.TrimSpace(cfg["key_path"])
	if certPath == "" && keyPath == "" {
		// 凭据级验证：认证通过即可，部署路径留到任务节点再做 L2 检查。
		res.add("scope", VerifyStatusSkipped, i18n.T(loc, "service.verify.scope_skipped", nil))
		return
	}
	if certPath == "" || keyPath == "" {
		res.add("config", VerifyStatusFailed, i18n.T(loc, "service.verify.paths_required", nil))
		return
	}
	if path.Clean(certPath) == path.Clean(keyPath) {
		res.add("config", VerifyStatusFailed, i18n.T(loc, "service.verify.paths_conflict", nil))
		return
	}
	res.Mode = "write"
	checked := map[string]bool{}
	for _, target := range []string{certPath, keyPath} {
		dir := path.Dir(target)
		if checked[dir] {
			continue
		}
		checked[dir] = true
		existing, created := nearestExistingRemoteDir(clientSFTP, dir)
		if existing == "" {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.dir_unavailable", map[string]any{"dir": dir}))
			continue
		}
		if err := ctx.Err(); err != nil {
			res.add("permission", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
			continue
		}
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			res.add("permission", VerifyStatusError, i18n.T(loc, "service.verify.token_failed", map[string]any{"error": err.Error()}))
			continue
		}
		tmp := path.Join(existing, ".lactssl-verify-"+hex.EncodeToString(suffix[:]))
		file, err := clientSFTP.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.dir_unwritable", map[string]any{"dir": dir, "error": err.Error()}))
			continue
		}
		_ = file.Close()
		if err := clientSFTP.Remove(tmp); err != nil {
			res.add("permission", VerifyStatusFailed, i18n.T(loc, "service.verify.temp_cleanup_failed", map[string]any{"file": tmp, "error": err.Error()}))
			continue
		}
		message := ""
		if created {
			message = i18n.T(loc, "service.verify.dir_will_create", map[string]any{"dir": dir})
		}
		res.add("permission", VerifyStatusOK, message)
	}
}

func nearestExistingRemoteDir(client *sftp.Client, dir string) (string, bool) {
	original := path.Clean(dir)
	current := original
	for {
		info, err := client.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", current != original
			}
			return current, current != original
		}
		// 只有“不存在”才向上找父目录；权限不足等错误必须如实报错，
		// 否则会把不可访问的目录误判为“部署时会自动创建”。
		var status *sftp.StatusError
		if !errors.As(err, &status) || status.FxCode() != sftp.ErrSSHFxNoSuchFile {
			return "", current != original
		}
		parent := path.Dir(current)
		if parent == current || parent == "." {
			return "", current != original
		}
		current = parent
	}
}

// verifyPluginDeploy 调用插件可选实现的 Verifier；当前官方 helper 尚未实现，
// 返回 unsupported 让用户明确知道“不是权限失败，而是插件版本不支持”。
func verifyPluginDeploy(ctx context.Context, loc i18n.Locale, res *VerifyResult, canonical, accountType string, auth, merged map[string]string, account *model.DeployAccount) {
	deployer, found := deployplugin.Lookup(canonical)
	if !found && accountType != canonical {
		deployer, found = deployplugin.Lookup(accountType)
	}
	if !found {
		res.add("init", VerifyStatusUnsupported, i18n.T(loc, "service.verify.deploy_type_unsupported", map[string]any{"type": accountType}))
		return
	}
	verifier, ok := deployer.(deployplugin.Verifier)
	if !ok {
		res.add("verify", VerifyStatusUnsupported, i18n.T(loc, "service.verify.deploy_unsupported", nil))
		return
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	response, err := verifier.Verify(ctx, deployplugin.Request{
		AccountID: accountID,
		Auth:      auth,
		Config:    merged,
		Locale:    string(loc),
	})
	if err != nil {
		if errors.Is(err, deployplugin.ErrVerifyUnsupported) {
			res.add("verify", VerifyStatusUnsupported, i18n.T(loc, "service.verify.deploy_unsupported", nil))
			return
		}
		res.add("verify", VerifyStatusFailed, i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}))
		return
	}
	appendVerifyChecks(res, response.Checks, response.OK)
}

// VerifyUnsupportedError 供插件在实现 Verifier 后表示“该目标不支持此模式”。
type VerifyUnsupportedError struct {
	Message string
}

func (e *VerifyUnsupportedError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return "verification unsupported"
	}
	return e.Message
}

// IsVerifyUnsupported 判断错误是否表示插件不支持验证。
func IsVerifyUnsupported(err error) bool {
	var target *VerifyUnsupportedError
	return errors.As(err, &target)
}
