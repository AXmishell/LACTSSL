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
	deployplugin "lactssl/internal/plugin/deploy"
	dnsplugin "lactssl/internal/plugin/dns"
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
}

func newVerifyResult(mode string) *VerifyResult {
	return &VerifyResult{Mode: mode, VerifiedAt: time.Now().UTC()}
}

func (r *VerifyResult) add(key, status, message string) {
	r.Checks = append(r.Checks, VerifyCheck{Key: key, Status: status, Message: message})
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

// VerifyModeNormalize 把接口传入的模式归一化。auto 在阶段 0 等同 write：
// 旧插件没有只读探测能力，写一条随机 TXT 再删除是唯一通用的验证方式。
func VerifyModeNormalize(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read":
		return "read"
	case "write", "auto", "":
		return "write"
	default:
		return "write"
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

// VerifyDNSAccount 验证 DNS 凭据：构造 provider、写入随机 TXT、再删除。
// lockKey 用于按账户串行化，避免两次写测试同时操作同一 zone。
func (m *Manager) VerifyDNSAccount(ctx context.Context, loc i18n.Locale, lockKey, provider string, cfg map[string]string, domain, mode string) VerifyResult {
	mode = VerifyModeNormalize(mode)
	res := newVerifyResult(mode)
	defer res.finish()

	plugin, found := dnsplugin.Lookup(provider)
	if !found {
		res.add("init", VerifyStatusUnsupported, i18n.T(loc, "service.verify.dns_provider_unsupported", map[string]any{"provider": provider}))
		return *res
	}
	if mode == "read" {
		// 阶段 0 的 helper 协议没有只读探测，明确返回不支持，由前端引导用户
		// 使用默认的写测试，而不是伪造一个“认证通过”的结论。
		res.add("read", VerifyStatusUnsupported, i18n.T(loc, "service.verify.read_unsupported", nil))
		return *res
	}
	domain = NormalizeVerifyDomain(domain)
	if domain == "" {
		res.add("domain", VerifyStatusFailed, i18n.T(loc, "service.verify.domain_invalid", nil))
		return *res
	}
	res.add("domain", VerifyStatusOK, domain)

	instance, err := plugin.New(cfg)
	if err != nil {
		res.add("init", VerifyStatusFailed, i18n.T(loc, "service.verify.init_failed", map[string]any{"error": err.Error()}))
		return *res
	}
	res.add("init", VerifyStatusOK, "")

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			res.add("write", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
			return *res
		}
	}
	if lockKey != "" {
		unlock := m.dnsLocks.Lock(lockKey)
		defer unlock()
	}

	token, keyAuth, err := randomVerifyToken()
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

func randomVerifyToken() (string, string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", err
	}
	value := hex.EncodeToString(buf[:])
	return value, "lactssl-verify-" + value, nil
}

// VerifyDeployAccount 验证目标凭据。deployCfg 为空时只做凭据级检查；
// 传入部署节点参数时，会额外检查该站点/目录/桶的权限（阶段 2 的 L2 验证）。
func (m *Manager) VerifyDeployAccount(ctx context.Context, loc i18n.Locale, accountType string, auth, deployCfg map[string]string, mode string, account *model.DeployAccount) VerifyResult {
	mode = VerifyModeNormalize(mode)
	res := newVerifyResult(mode)
	defer res.finish()

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

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			res.add("connect", VerifyStatusError, i18n.T(loc, "service.verify.canceled", nil))
			return *res
		}
	}

	switch strings.ToLower(strings.TrimSpace(def.Value)) {
	case "local":
		verifyLocalDeploy(m, loc, res, merged)
	case "ssh":
		verifySSHDeploy(ctx, m, loc, res, merged, account)
	default:
		verifyPluginDeploy(ctx, loc, res, accountType, auth, merged, account)
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
		parent := path.Dir(current)
		if parent == current || parent == "." {
			return "", current != original
		}
		current = parent
	}
}

// verifyPluginDeploy 调用插件可选实现的 Verifier；当前官方 helper 尚未实现，
// 返回 unsupported 让用户明确知道“不是权限失败，而是插件版本不支持”。
func verifyPluginDeploy(ctx context.Context, loc i18n.Locale, res *VerifyResult, accountType string, auth, merged map[string]string, account *model.DeployAccount) {
	deployer, found := deployplugin.Lookup(accountType)
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
	if err := verifier.Verify(ctx, deployplugin.Request{AccountID: accountID, Auth: auth, Config: merged}); err != nil {
		res.add("verify", VerifyStatusFailed, i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}))
		return
	}
	res.add("verify", VerifyStatusOK, "")
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
