package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/outbound"
	"lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/install"
	"lactssl/internal/plugin/notify"
	"lactssl/internal/plugin/repo"
	"lactssl/internal/service"
)

const (
	repoEnabledSetting     = "plugins.repo.enabled"
	repoURLSetting         = "plugins.repo.url"
	repoChannelSetting     = "plugins.repo.channel"
	repoAutoUpdateSetting  = "plugins.repo.auto_update"
	repoPublicKeySetting   = "plugins.repo.public_key"
	repoProxySetting       = "plugins.repo.proxy"
	repoIndexSetting       = "plugins.repo.index"
	repoIndexMACSetting    = "plugins.repo.index_mac"
	repoIndexSigSetting    = "plugins.repo.index_sig"
	repoETagSetting        = "plugins.repo.index_etag"
	repoCheckedSetting     = "plugins.repo.checked_at"
	repoRevisionSetting    = "plugins.repo.revision"
	repoGeneratedSetting   = "plugins.repo.generated_at"
	repoVerifiedSetting    = "plugins.repo.signature_verified"
	repoVerifiedKeySetting = "plugins.repo.verified_key"
)

// repoCacheIdentity 把本地索引缓存绑定到当前仓库地址与渠道。
// 密钥变化不进入身份串，由加载缓存时的重新验签负责；URL 或渠道变化后，
// 旧缓存即使被数据库恢复/并发写回，也会因 HMAC 不匹配而被拒绝。
func (s *Server) repoCacheIdentity() string {
	url := strings.TrimSpace(s.PluginRepoURL)
	channel := strings.TrimSpace(s.PluginRepoChannel)
	if s.Store != nil {
		url = strings.TrimSpace(s.setting(repoURLSetting, s.PluginRepoURL))
		channel = strings.TrimSpace(s.setting(repoChannelSetting, s.PluginRepoChannel))
	}
	channel = strings.ToLower(channel)
	if channel == "" {
		channel = "stable"
	}
	return url + "\n" + channel
}

// repoIndexMAC 用服务端密钥保护本地缓存的索引，防止数据库被篡改后
// 直接注入包含恶意资产地址的索引。
func (s *Server) repoIndexMAC(raw string) string {
	return s.repoIndexMACFor(raw, s.repoCacheIdentity())
}

// repoIndexMACFor 使用调用方捕获的身份计算 MAC，避免持久化期间仓库
// 配置变化导致旧原始字节被错误地签成新身份。
func (s *Server) repoIndexMACFor(raw, identity string) string {
	if s.Auth == nil {
		return ""
	}
	return s.Auth.SignValue("plugin-index-cache", identity+"\x00"+raw)
}

// repoGeneration 返回当前索引缓存代次。清空/切换仓库会递增该值。
func (s *Server) repoGeneration() uint64 {
	s.pluginRepoMu.Lock()
	defer s.pluginRepoMu.Unlock()
	return s.pluginRepoGen
}

func (s *Server) cachedRepoIndexRaw() (string, bool) {
	raw := s.setting(repoIndexSetting, "")
	if raw == "" {
		return "", false
	}
	mac := s.setting(repoIndexMACSetting, "")
	if mac == "" || subtle.ConstantTimeCompare([]byte(mac), []byte(s.repoIndexMAC(raw))) != 1 {
		return "", false
	}
	return raw, true
}

// cachedRepoIndexSignature 返回缓存的索引签名（base64 解码后）。
// 签名本身是公开数据，真正的信任锚是当前配置的公钥；损坏时按缺失处理。
func (s *Server) cachedRepoIndexSignature() ([]byte, bool) {
	encoded := strings.TrimSpace(s.setting(repoIndexSigSetting, ""))
	if encoded == "" {
		return nil, false
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	return sig, true
}

var legacyPluginNames = map[string][]string{
	pluginKindDNS:    {"lactssl-dns-helper-extra", "lactssl-dns-helper", "lactssl-dns-helper-full", "dns-helper"},
	pluginKindDeploy: {"lactssl-deploy-helper", "deploy-helper"},
	pluginKindNotify: {"lactssl-notify-helper", "notify-helper"},
}

type repoPluginView struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	Name              string `json:"name,omitempty"`
	Description       string `json:"description,omitempty"`
	Version           string `json:"version"`
	Protocol          string `json:"protocol"`
	MinHost           string `json:"min_host,omitempty"`
	Bundle            bool   `json:"bundle,omitempty"`
	ProviderCount     int    `json:"provider_count,omitempty"`
	Compatible        bool   `json:"compatible"`
	PlatformAvailable bool   `json:"platform_available"`
	Installed         bool   `json:"installed"`
	InstalledVersion  string `json:"installed_version,omitempty"`
	Source            string `json:"source,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	Icon              string `json:"icon,omitempty"`
	Size              int64  `json:"size,omitempty"`
	DownloadSize      int64  `json:"download_size,omitempty"`
	Invalid           bool   `json:"invalid,omitempty"`
	InvalidCode       string `json:"invalid_code,omitempty"`
	InvalidReason     string `json:"invalid_reason,omitempty"`
}

func (s *Server) repoBool(key string, fallback bool) bool {
	value := strings.TrimSpace(s.setting(key, ""))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true") || value == "1"
}

func (s *Server) repoEnabled() bool {
	return s.repoBool(repoEnabledSetting, s.PluginRepoEnabled)
}

func (s *Server) repoURL() string {
	return strings.TrimSpace(s.setting(repoURLSetting, s.PluginRepoURL))
}

func (s *Server) repoChannel() string {
	value := strings.TrimSpace(s.setting(repoChannelSetting, s.PluginRepoChannel))
	if value == "" {
		return "stable"
	}
	return value
}

func (s *Server) repoAutoUpdate() bool {
	return s.repoBool(repoAutoUpdateSetting, s.PluginRepoAutoUpdate)
}

// repoPublicKey 返回索引验签公钥。
// 这里必须区分“设置不存在”（回退到配置文件默认值）与“显式设置为空”
// （用户主动关闭验签），因此不能复用 setting() 的空值回退语义。
func (s *Server) repoPublicKey() string {
	if s.Store != nil {
		if value, found, err := s.Store.LookupSetting(repoPublicKeySetting); err == nil && found {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(s.PluginRepoPublicKey)
}

// repoProxy 返回插件仓库下载代理。
// 与公钥相同，必须区分“设置不存在”（回退到配置文件默认值）与
// “显式设置为空”（用户主动关闭代理，即使配置文件配置了代理）。
func (s *Server) repoProxy() string {
	if s.Store != nil {
		if _, found, err := s.Store.LookupSetting(repoProxySetting); err == nil && found {
			value, err := s.Store.GetSecret(repoProxySetting)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(s.PluginRepoProxy)
}

func (s *Server) repoClient() (*pluginrepo.Client, error) {
	return pluginrepo.NewClientWithProxy(s.repoURL(), s.repoChannel(), s.repoPublicKey(), s.PluginRepoTimeout, s.PluginRepoMaxDownloadMB, s.repoProxy())
}

// repoParseOptions 构造索引校验参数，保证网络、缓存与 304 分支使用同一套规则。
func (s *Server) repoParseOptions() pluginrepo.ParseOptions {
	return pluginrepo.ParseOptions{
		Channel:          s.repoChannel(),
		IndexURL:         s.repoURL(),
		LastRevision:     s.repoLastRevision(),
		LastGeneratedAt:  s.repoLastGeneratedAt(),
		MaxDownloadBytes: int64(s.PluginRepoMaxDownloadMB) << 20,
	}
}

// repoExpiredError 在安装/自动更新前拒绝已过期的索引。
// 列表与状态仍允许展示过期索引，便于管理员排查；只有写入动作严格拒绝。
func repoExpiredError(validated pluginrepo.ValidatedIndex, now time.Time) error {
	if !validated.Expired(now) {
		return nil
	}
	return i18n.E("plugin.repo.index_expired", "time", validated.ExpiresAt.UTC().Format(time.RFC3339))
}

// repoEntryValidationError 在安装前复核条目字段，避免调用方绕过索引过滤
// 直接安装被隔离的条目。没有资产信息的调用方（测试或自定义流程）由
// Download 的 sha256 校验兜底。
func repoEntryValidationError(plugin pluginrepo.Plugin, opts pluginrepo.ParseOptions) error {
	for _, issue := range pluginrepo.ValidateEntry(plugin, opts) {
		if issue.Severity == pluginrepo.SeverityEntry || issue.Severity == pluginrepo.SeverityFatal {
			return issue.Error()
		}
	}
	return nil
}

func (s *Server) repoLastRevision() uint64 {
	value := strings.TrimSpace(s.setting(repoRevisionSetting, ""))
	if value == "" {
		return 0
	}
	revision, _ := strconv.ParseUint(value, 10, 64)
	return revision
}

func (s *Server) repoLastGeneratedAt() time.Time {
	value := strings.TrimSpace(s.setting(repoGeneratedSetting, ""))
	if value == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

// repoKeyFingerprint 返回当前配置公钥的指纹；未配置或格式非法时返回空/占位。
func (s *Server) repoKeyFingerprint() string {
	value := s.repoPublicKey()
	if value == "" {
		return ""
	}
	keys, err := pluginrepo.ParsePublicKeys(value)
	if err != nil || len(keys) == 0 {
		return "invalid"
	}
	return pluginrepo.PublicKeysFingerprint(keys)
}

// repoUpdateAvailable 判断仓库插件是否需要更新。
// 版本相同时比较当前平台资产的 SHA256，覆盖 all-lego 这类跟随上游版本、
// 但包装层/共享 SDK 变化后内容可能变化的包。
func repoUpdateAvailable(plugin pluginrepo.Plugin, asset pluginrepo.Asset, record model.PluginInstallation) bool {
	cmp := pluginrepo.CompareVersion(plugin.Version, record.Version)
	if cmp != 0 {
		return cmp > 0
	}
	if record.AssetSHA256 == "" || asset.SHA256 == "" {
		return false
	}
	return !strings.EqualFold(asset.SHA256, record.AssetSHA256)
}

// repoSignatureVerified 返回当前索引是否已通过 Ed25519 验签。
func (s *Server) repoSignatureVerified() bool {
	s.pluginRepoMu.Lock()
	defer s.pluginRepoMu.Unlock()
	return s.pluginRepoVerified
}

// requireSignedRepoIndex 在配置了公钥但索引未验签时拒绝安装/更新。
// 未配置公钥的旧部署保持可用，但只能依赖 HTTPS 与 asset sha256。
func (s *Server) requireSignedRepoIndex() error {
	if s.repoPublicKey() == "" || s.repoSignatureVerified() {
		return nil
	}
	return i18n.E("api.error.plugin_repo_signature_required")
}

// redactRepoURL 去掉 URL 中的凭据、query 与 fragment，供状态接口和日志展示。
// 仓库 token 常放在 query 中，不能回显给 plugins:read。
func redactRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "(invalid url)"
	}
	if parsed.Scheme == "file" {
		return parsed.String()
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// redactProxyURL 只保留代理的 scheme/host/port，隐藏认证信息和路径/query。
func redactProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "(invalid proxy)"
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Server) repoStatus(contexts ...*gin.Context) gin.H {
	s.pluginRepoMu.Lock()
	hasMemoryIndex := len(s.pluginRepoIndexRaw) > 0
	lastErr := s.pluginRepoErr
	s.pluginRepoMu.Unlock()
	// 进程刚启动且后台刷新失败时，内存里还没有索引；先尝试恢复本地已验签
	// 缓存，避免状态接口把“暂时无法访问仓库”误报成“签名校验失败”。
	if !hasMemoryIndex {
		if _, ok := s.loadCachedRepoIndex(); ok && lastErr != "" {
			s.pluginRepoMu.Lock()
			s.pluginRepoErr = lastErr
			s.pluginRepoMu.Unlock()
		}
	}
	s.pluginRepoMu.Lock()
	index := s.pluginRepoIndex
	checkedAt := s.pluginRepoCheckedAt
	lastErr = s.pluginRepoErr
	verified := s.pluginRepoVerified
	issues := append([]pluginrepo.Issue(nil), s.pluginRepoIssues...)
	rejected := len(s.pluginRepoRejected)
	revision := s.pluginRepoRevision
	indexDigest := s.pluginRepoIndexDigest
	keyID := s.pluginRepoKeyID
	generatedAt := s.pluginRepoGeneratedAt
	expiresAt := s.pluginRepoExpiresAt
	s.pluginRepoMu.Unlock()
	checked := ""
	if !checkedAt.IsZero() {
		checked = checkedAt.Format(time.RFC3339)
	}
	generated := ""
	if !generatedAt.IsZero() {
		generated = generatedAt.Format(time.RFC3339)
	}
	expires := ""
	if !expiresAt.IsZero() {
		expires = expiresAt.Format(time.RFC3339)
	}
	locale := i18n.Default
	if len(contexts) > 0 && contexts[0] != nil {
		locale = i18n.FromContext(contexts[0])
	}
	// 只返回公钥指纹，不回显公钥本身。
	publicKey := s.repoPublicKey()
	publicKeyValid := false
	fingerprint := ""
	if publicKey != "" {
		if keys, err := pluginrepo.ParsePublicKeys(publicKey); err == nil && len(keys) > 0 {
			publicKeyValid = true
			fingerprint = pluginrepo.PublicKeysFingerprint(keys)
		}
	}
	proxy := s.repoProxy()
	signatureKeyID := ""
	if publicKeyValid && verified {
		signatureKeyID = keyID
	}
	return gin.H{
		"enabled":                s.repoEnabled(),
		"url":                    redactRepoURL(s.repoURL()),
		"channel":                s.repoChannel(),
		"auto_update":            s.repoAutoUpdate(),
		"proxy":                  redactProxyURL(proxy),
		"proxy_set":              proxy != "",
		"public_key_set":         publicKey != "",
		"public_key_valid":       publicKeyValid,
		"public_key_fingerprint": fingerprint,
		"signature_key_id":       signatureKeyID,
		"signature_verified":     publicKeyValid && verified,
		"timeout_seconds":        int(s.PluginRepoTimeout.Seconds()),
		"max_download_mb":        s.PluginRepoMaxDownloadMB,
		"platform":               runtime.GOOS + "/" + runtime.GOARCH,
		"host_version":           Version,
		"checked_at":             checked,
		"index_schema":           index.Schema,
		"index_revision":         revision,
		"index_digest":           indexDigest,
		"index_generated_at":     generated,
		"index_expires_at":       expires,
		"plugins":                len(index.Plugins),
		"plugins_rejected":       rejected,
		"issues":                 s.repoIssueViews(locale, issues),
		"error":                  service.SanitizeErrorText(lastErr),
	}
}

// repoIssueViews 把索引问题转换为可直接返回给前端的结构，最多保留 20 条。
func (s *Server) repoIssueViews(locale i18n.Locale, issues []pluginrepo.Issue) []gin.H {
	if len(issues) == 0 {
		return nil
	}
	limit := len(issues)
	if limit > 20 {
		limit = 20
	}
	out := make([]gin.H, 0, limit)
	for _, issue := range issues[:limit] {
		detailCode := ""
		detailMessage := ""
		if issue.Detail != nil {
			detailCode = issue.Detail.Code
			detailMessage = clipText(service.SanitizeErrorText(issue.DetailText(locale)), 512)
		}
		out = append(out, gin.H{
			"code":           issue.Code,
			"severity":       issue.Severity,
			"path":           clipText(issue.Path, 256),
			"kind":           clipText(issue.Kind, 64),
			"plugin":         clipText(issue.Plugin, 256),
			"platform":       clipText(issue.Platform, 64),
			"detail_code":    detailCode,
			"detail_message": detailMessage,
			"message":        clipText(service.SanitizeErrorText(issue.Text(locale)), 1024),
		})
	}
	return out
}

// clipText 限制索引字段长度，避免恶意索引用超长参数放大状态响应。
func clipText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// loadRepoIndex 返回仓库中可用条目的索引。内存/本地缓存未命中时才访问网络，
// 网络请求不在 pluginRepoMu 内执行，避免慢仓库阻塞插件页状态与图标接口；
// 并发刷新通过 singleflight 合并为一次请求。
func (s *Server) loadRepoIndex(force bool) (pluginrepo.Index, error) {
	result, err := s.loadRepoValidated(force)
	return result.Index, err
}

// loadRepoValidated 返回完整校验结果（可用条目、被隔离条目与问题列表）。
func (s *Server) loadRepoValidated(force bool) (pluginrepo.ValidatedIndex, error) {
	if !force {
		s.pluginRepoMu.Lock()
		if len(s.pluginRepoIndexRaw) > 0 {
			result := s.repoValidatedLocked()
			s.pluginRepoMu.Unlock()
			return result, nil
		}
		s.pluginRepoMu.Unlock()
		if result, ok := s.loadCachedRepoIndex(); ok {
			return result, nil
		}
	}
	// force 使用独立的 singleflight key，避免强制刷新合入正在进行的
	// 普通刷新后只拿到 304/缓存结果。
	key := "plugin-repo-index:normal"
	if force {
		key = "plugin-repo-index:force"
	}
	value, err, _ := s.repoGroup.Do(key, func() (any, error) {
		return s.fetchRepoIndex(force)
	})
	if err != nil {
		s.setRepoError(err)
		if result, ok := value.(pluginrepo.ValidatedIndex); ok && len(result.Issues) > 0 {
			s.pluginRepoMu.Lock()
			s.pluginRepoIssues = append([]pluginrepo.Issue(nil), result.Issues...)
			s.pluginRepoMu.Unlock()
		}
		return pluginrepo.ValidatedIndex{}, err
	}
	result, _ := value.(pluginrepo.ValidatedIndex)
	return result, nil
}

// repoValidatedLocked 把内存缓存拼装为完整校验结果，调用方需持有 pluginRepoMu。
func (s *Server) repoValidatedLocked() pluginrepo.ValidatedIndex {
	return pluginrepo.ValidatedIndex{
		Index:       s.pluginRepoIndex,
		Revision:    s.pluginRepoRevision,
		GeneratedAt: s.pluginRepoGeneratedAt,
		ExpiresAt:   s.pluginRepoExpiresAt,
		Digest:      s.pluginRepoIndexDigest,
		KeyID:       s.pluginRepoKeyID,
		Issues:      append([]pluginrepo.Issue(nil), s.pluginRepoIssues...),
		Rejected:    append([]pluginrepo.RejectedEntry(nil), s.pluginRepoRejected...),
	}
}

// loadCachedRepoIndex 读取数据库中的索引缓存（带服务端 HMAC 校验）。
func (s *Server) loadCachedRepoIndex() (pluginrepo.ValidatedIndex, bool) {
	identity := s.repoCacheIdentity()
	keyFingerprint := s.repoKeyFingerprint()
	publicKey := s.repoPublicKey()
	generation := s.repoGeneration()
	raw, ok := s.cachedRepoIndexRaw()
	if !ok {
		// 缓存缺失/被篡改时不能继续带旧 ETag：否则服务端返回 304 后仍会因
		// 缓存校验失败而无法刷新。
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.ValidatedIndex{}, false
	}
	// 缓存索引由服务端 HMAC 保护，但“已验签”必须由当前公钥对缓存原始字节
	// 重新验签得出，不能信任数据库中的独立布尔位或公钥指纹。
	verified := false
	keyID := ""
	if keyFingerprint != "" {
		signature, ok := s.cachedRepoIndexSignature()
		if ok {
			keyID, ok = pluginrepo.VerifyIndexSignatureKeyID(publicKey, []byte(raw), signature)
		}
		if !ok {
			// 缓存不是由当前公钥签名的（例如升级前未验签的缓存）：丢弃 ETag，
			// 强制下一次刷新全量重取，而不是让 304 把旧缓存“洗白”。
			_ = s.Store.DeleteSetting(repoETagSetting)
			return pluginrepo.ValidatedIndex{}, false
		}
		verified = true
	}
	// 与网络路径共用 ParseIndex，避免缓存/304 漏掉 schema、channel、revision 等规则。
	result, err := pluginrepo.ParseIndex([]byte(raw), s.repoParseOptions())
	if err != nil {
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.ValidatedIndex{}, false
	}
	result.KeyID = keyID
	checkedAt := time.Time{}
	if ts := s.setting(repoCheckedSetting, ""); ts != "" {
		checkedAt, _ = time.Parse(time.RFC3339, ts)
	}

	// 与清理/刷新持久化串行，避免读取期间仓库配置已变化、清理完成后
	// 又把旧缓存写回内存。
	s.pluginRepoPersistMu.Lock()
	defer s.pluginRepoPersistMu.Unlock()
	s.pluginRepoMu.Lock()
	defer s.pluginRepoMu.Unlock()
	if s.pluginRepoGen != generation ||
		s.repoCacheIdentity() != identity ||
		s.repoKeyFingerprint() != keyFingerprint {
		return pluginrepo.ValidatedIndex{}, false
	}
	// 并发刷新可能已经写入更新的内存缓存，优先使用它。
	if len(s.pluginRepoIndexRaw) > 0 {
		return s.repoValidatedLocked(), true
	}
	s.storeRepoValidatedLocked(result, []byte(raw), checkedAt, verified)
	return result, true
}

// fetchRepoIndex 执行实际的网络拉取。仅在 singleflight 内调用。
func (s *Server) fetchRepoIndex(force bool) (pluginrepo.ValidatedIndex, error) {
	// 等待 singleflight 期间其他调用可能已完成刷新，这里再检查一次内存缓存。
	if !force {
		s.pluginRepoMu.Lock()
		if len(s.pluginRepoIndexRaw) > 0 {
			result := s.repoValidatedLocked()
			s.pluginRepoMu.Unlock()
			return result, nil
		}
		s.pluginRepoMu.Unlock()
	}
	identity := s.repoCacheIdentity()
	client, err := s.repoClient()
	if err != nil {
		return pluginrepo.ValidatedIndex{}, err
	}
	s.pluginRepoMu.Lock()
	generation := s.pluginRepoGen
	s.pluginRepoMu.Unlock()
	etag := s.setting(repoETagSetting, "")
	publicKey := s.repoPublicKey()
	currentKey := s.repoKeyFingerprint()
	storedKey := s.setting(repoVerifiedKeySetting, "")
	if storedKey != currentKey {
		// 公钥（包括从“未配置”变为“已配置”）已变化：旧 ETag 对应的缓存
		// 不是用当前公钥验签的，必须全量重取。
		etag = ""
		_ = s.Store.DeleteSetting(repoETagSetting)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.PluginRepoTimeout)
	defer cancel()
	opts := s.repoParseOptions()
	result, raw, newETag, signature, notModified, err := client.FetchValidated(ctx, etag, opts)
	if err != nil {
		return result, err
	}
	if notModified {
		rawText, ok := s.cachedRepoIndexRaw()
		cachedSig, sigOK := s.cachedRepoIndexSignature()
		cacheValid := ok
		if cacheValid && currentKey != "" {
			// 304 不携带签名，必须用当前公钥重新校验缓存字节；否则未验签的
			// 旧缓存会被错误地标记为“已验签”。
			cacheValid = sigOK && pluginrepo.VerifyIndexSignature(publicKey, []byte(rawText), cachedSig)
		}
		if cacheValid {
			cachedResult, parseErr := pluginrepo.ParseIndex([]byte(rawText), opts)
			if parseErr == nil {
				result = cachedResult
				raw = []byte(rawText)
				signature = cachedSig
			} else {
				// 缓存无法通过当前规则时不能继续使用 ETag，改为全量重取。
				cacheValid = false
			}
		}
		if !cacheValid {
			result, raw, newETag, signature, _, err = client.FetchValidated(ctx, "", opts)
			if err != nil {
				return result, err
			}
		}
	}
	checkedAt := time.Now().UTC()
	verified := false
	keyID := ""
	if currentKey != "" {
		// 全量拉取时 FetchValidated 已验签；304 分支使用缓存签名，这里统一再校验一次，
		// 避免任何分支仅凭“配置了公钥”就置为已验签。
		verified = len(signature) > 0
		if verified {
			keyID, verified = pluginrepo.VerifyIndexSignatureKeyID(publicKey, raw, signature)
		}
		if !verified {
			return pluginrepo.ValidatedIndex{}, i18n.E("api.error.plugin_repo_signature_required")
		}
	}
	result.KeyID = keyID
	verifiedKey := ""
	if verified {
		verifiedKey = currentKey
	}

	// 持久化与 clearRepoIndexCache 共用一把锁：要么清理先完成、本次刷新
	// 因代次或身份变化放弃；要么本次刷新先完成、随后清理把缓存整体删除。
	// 这样不会出现“清理完成后旧索引又被写回”的窗口。
	s.pluginRepoPersistMu.Lock()
	defer s.pluginRepoPersistMu.Unlock()
	s.pluginRepoMu.Lock()
	if s.pluginRepoGen != generation || s.repoCacheIdentity() != identity {
		s.pluginRepoMu.Unlock()
		return pluginrepo.ValidatedIndex{}, i18n.E("api.error.plugin_repo_changed")
	}
	s.storeRepoValidatedLocked(result, raw, checkedAt, verified)
	s.pluginRepoETag = newETag
	s.pluginRepoMu.Unlock()

	// 数据库写入在 persistMu 内完成；不持有 pluginRepoMu，避免 SQLite
	// 短暂阻塞影响其他读缓存请求。
	_ = s.Store.SetSetting(repoIndexSetting, string(raw))
	_ = s.Store.SetSetting(repoIndexMACSetting, s.repoIndexMACFor(string(raw), identity))
	if len(signature) > 0 {
		_ = s.Store.SetSetting(repoIndexSigSetting, base64.StdEncoding.EncodeToString(signature))
	} else {
		_ = s.Store.DeleteSetting(repoIndexSigSetting)
	}
	if newETag != "" {
		_ = s.Store.SetSetting(repoETagSetting, newETag)
	} else {
		_ = s.Store.DeleteSetting(repoETagSetting)
	}
	_ = s.Store.SetSetting(repoCheckedSetting, checkedAt.Format(time.RFC3339))
	if result.Revision > 0 {
		_ = s.Store.SetSetting(repoRevisionSetting, strconv.FormatUint(result.Revision, 10))
	}
	if !result.GeneratedAt.IsZero() {
		_ = s.Store.SetSetting(repoGeneratedSetting, result.GeneratedAt.UTC().Format(time.RFC3339))
	} else {
		_ = s.Store.DeleteSetting(repoGeneratedSetting)
	}
	_ = s.Store.SetSetting(repoVerifiedSetting, strconv.FormatBool(verified))
	_ = s.Store.SetSetting(repoVerifiedKeySetting, verifiedKey)
	if len(result.Rejected) > 0 && s.Logger != nil {
		s.Logger.Warn("插件索引包含被隔离的条目", "count", len(result.Rejected))
	}
	return result, nil
}

// storeRepoValidatedLocked 写入内存索引与校验元数据，调用方需持有 pluginRepoMu。
func (s *Server) storeRepoValidatedLocked(result pluginrepo.ValidatedIndex, raw []byte, checkedAt time.Time, verified bool) {
	s.pluginRepoIndex = result.Index
	s.pluginRepoIndexRaw = raw
	s.pluginRepoCheckedAt = checkedAt
	s.pluginRepoErr = ""
	s.pluginRepoVerified = verified
	s.pluginRepoRevision = result.Revision
	s.pluginRepoIndexDigest = result.Digest
	s.pluginRepoKeyID = result.KeyID
	s.pluginRepoGeneratedAt = result.GeneratedAt
	s.pluginRepoExpiresAt = result.ExpiresAt
	s.pluginRepoIssues = append([]pluginrepo.Issue(nil), result.Issues...)
	s.pluginRepoRejected = append([]pluginrepo.RejectedEntry(nil), result.Rejected...)
}

// setRepoError 记录最近一次索引拉取错误。
func (s *Server) setRepoError(err error) {
	if err == nil {
		return
	}
	s.pluginRepoMu.Lock()
	s.pluginRepoErr = service.SanitizeErrorText(err.Error())
	s.pluginRepoMu.Unlock()
}

func (s *Server) clearRepoIndexCache() {
	// 与刷新持久化串行，确保清理完成后不会被在途刷新重新写回。
	s.pluginRepoPersistMu.Lock()
	defer s.pluginRepoPersistMu.Unlock()
	s.pluginRepoMu.Lock()
	s.pluginRepoGen++
	s.pluginRepoIndex = pluginrepo.Index{}
	s.pluginRepoIndexRaw = nil
	s.pluginRepoETag = ""
	s.pluginRepoCheckedAt = time.Time{}
	s.pluginRepoErr = ""
	s.pluginRepoVerified = false
	s.pluginRepoRevision = 0
	s.pluginRepoIndexDigest = ""
	s.pluginRepoKeyID = ""
	s.pluginRepoGeneratedAt = time.Time{}
	s.pluginRepoExpiresAt = time.Time{}
	s.pluginRepoIssues = nil
	s.pluginRepoRejected = nil
	s.pluginRepoMu.Unlock()
	_ = s.Store.DeleteSetting(repoIndexSetting)
	_ = s.Store.DeleteSetting(repoIndexMACSetting)
	_ = s.Store.DeleteSetting(repoIndexSigSetting)
	_ = s.Store.DeleteSetting(repoETagSetting)
	_ = s.Store.DeleteSetting(repoCheckedSetting)
	_ = s.Store.DeleteSetting(repoRevisionSetting)
	_ = s.Store.DeleteSetting(repoGeneratedSetting)
	_ = s.Store.DeleteSetting(repoVerifiedSetting)
	_ = s.Store.DeleteSetting(repoVerifiedKeySetting)
}

// RecoverLegacyPlugins 恢复迁移中断时被隐藏的旧 bundle 文件。
func (s *Server) RecoverLegacyPlugins() {
	for kind := range legacyPluginNames {
		dir, _ := s.pluginKindDir(kind)
		if dir == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, ".migrating-*"))
		for _, hidden := range matches {
			original := filepath.Join(dir, strings.TrimPrefix(filepath.Base(hidden), ".migrating-"))
			if _, err := os.Stat(original); errors.Is(err, os.ErrNotExist) {
				_ = os.Rename(hidden, original)
				continue
			}
			_ = os.Remove(hidden)
		}
	}
}

// StartPluginRepoWorker 在启动后异步拉取索引，并按配置周期检查更新。
func (s *Server) StartPluginRepoWorker() {
	if !s.repoEnabled() {
		return
	}
	interval := s.PluginRepoCheckInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	s.goSafe("plugin-repo-worker", func() {
		s.refreshRepoOnce()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.refreshRepoOnce()
		}
	})
}

func (s *Server) refreshRepoOnce() {
	if !s.repoEnabled() {
		return
	}
	if _, err := s.loadRepoIndex(true); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("插件仓库索引拉取失败", "error", service.SafeError(err))
		}
		return
	}
	if s.repoAutoUpdate() {
		s.runRepoAutoUpdate()
	}
}

// runRepoAutoUpdate 更新所有已安装且版本落后的插件。默认关闭，由管理员显式启用。
func (s *Server) runRepoAutoUpdate() {
	// 未配置签名公钥时拒绝无人值守更新：仅靠 HTTPS + 索引内 sha256
	// 无法抵御仓库/索引被替换，自动更新会把风险放大为静默 RCE。
	if s.repoPublicKey() == "" {
		if s.Logger != nil {
			s.Logger.Warn("插件自动更新已跳过：未配置索引签名公钥")
		}
		return
	}
	validated, err := s.loadRepoValidated(false)
	if err != nil {
		return
	}
	if err := repoExpiredError(validated, time.Now()); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("插件自动更新已跳过：索引已过期", "error", service.SafeError(err))
		}
		return
	}
	records, err := s.Store.ListPluginInstallations()
	if err != nil {
		return
	}
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	for _, record := range records {
		plugin, found := s.findRepoPlugin(validated.Index, record.Kind, record.ID, "")
		if !found {
			continue
		}
		asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		if !okAsset || !pluginrepo.Compatible(plugin, Version) {
			continue
		}
		if !repoUpdateAvailable(plugin, asset, record) {
			continue
		}
		if _, _, err := s.installRepoPluginWithAudit(record.Kind, plugin, asset, validated.Audit(plugin)); err != nil {
			if s.Logger != nil {
				s.Logger.Warn("插件自动更新失败", "kind", record.Kind, "id", record.ID, "error", service.SafeError(err))
			}
		}
	}
}

// getPluginRepo 返回插件仓库配置与当前索引状态。
func (s *Server) getPluginRepo(c *gin.Context) {
	ok(c, s.repoStatus(c))
}

// updatePluginRepo 修改插件仓库配置。
func (s *Server) updatePluginRepo(c *gin.Context) {
	var body struct {
		Enabled    *bool   `json:"enabled"`
		URL        *string `json:"url"`
		Channel    *string `json:"channel"`
		AutoUpdate *bool   `json:"auto_update"`
		PublicKey  *string `json:"public_key"`
		Proxy      *string `json:"proxy"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	// URL 或公钥实际变更会移动插件索引的信任锚；浏览器会话必须处于再认证
	// 有效期内。API 密钥仍由 plugins:write/security:write 控制。
	urlChanged := body.URL != nil && strings.TrimSpace(*body.URL) != s.repoURL()
	keyChanged := body.PublicKey != nil && strings.TrimSpace(*body.PublicKey) != strings.TrimSpace(s.repoPublicKey())
	if (urlChanged || keyChanged) && !s.requireReauthElevated(c) {
		return
	}
	changedSource := false
	if body.URL != nil {
		value := strings.TrimSpace(*body.URL)
		// 未配置公钥时切换仓库地址会改变“未签名仓库”的信任来源；
		// API Key 必须具备 security:write。
		effectiveKey := strings.TrimSpace(s.repoPublicKey())
		if body.PublicKey != nil {
			effectiveKey = strings.TrimSpace(*body.PublicKey)
		}
		if effectiveKey == "" && !s.requireAPIKeyScope(c, apiScopeSecurityWrite) {
			return
		}
		if err := validateRepoURL(value); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if value != s.repoURL() {
			changedSource = true
		}
		if err := s.Store.SetSetting(repoURLSetting, value); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Channel != nil {
		value := strings.TrimSpace(*body.Channel)
		if value == "" {
			value = "stable"
		}
		if value != s.repoChannel() {
			changedSource = true
		}
		if err := s.Store.SetSetting(repoChannelSetting, value); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	if body.PublicKey != nil {
		value := strings.TrimSpace(*body.PublicKey)
		// 替换或清空仓库公钥都会改变索引信任锚，API Key 必须具备 security:write。
		// 否则 plugins:write 可以换成攻击者公钥后安装“已签名”的恶意插件。
		if value != strings.TrimSpace(s.repoPublicKey()) &&
			!s.requireAPIKeyScope(c, apiScopeSecurityWrite) {
			return
		}
		// 保存前先校验格式，避免写入无法验签的公钥后才发现索引刷新失败。
		if value != "" {
			keys, err := pluginrepo.ParsePublicKeys(value)
			if err != nil || len(keys) == 0 {
				if err == nil {
					err = errors.New("插件仓库公钥格式不正确")
				}
				failErr(c, http.StatusBadRequest, err)
				return
			}
		}
		if value != s.repoPublicKey() {
			changedSource = true
		}
		if err := s.Store.SetSetting(repoPublicKeySetting, value); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Proxy != nil {
		value := strings.TrimSpace(*body.Proxy)
		if err := outbound.ValidateProxyURL(value); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		if value != s.repoProxy() {
			changedSource = true
		}
		// 代理地址可能包含认证信息，与 SMTP 密码一样加密保存。
		if err := s.Store.SetSecret(repoProxySetting, value); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Enabled != nil {
		_ = s.Store.SetSetting(repoEnabledSetting, strconv.FormatBool(*body.Enabled))
	}
	if body.AutoUpdate != nil {
		_ = s.Store.SetSetting(repoAutoUpdateSetting, strconv.FormatBool(*body.AutoUpdate))
	}
	if changedSource {
		s.clearRepoIndexCache()
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.plugin_repo_updated", map[string]any{"url": redactRepoURL(s.repoURL())})
	ok(c, s.repoStatus(c))
}

// refreshPluginRepo 强制拉取仓库索引。
func (s *Server) refreshPluginRepo(c *gin.Context) {
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	if _, err := s.loadRepoIndex(true); err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	ok(c, s.repoStatus(c))
}

// listPluginRepoPlugins 返回仓库中的插件与安装状态。
func (s *Server) listPluginRepoPlugins(c *gin.Context) {
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	validated, err := s.loadRepoValidated(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	index := validated.Index
	kindFilter := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	installed := map[string]model.PluginInstallation{}
	if list, err := s.Store.ListPluginInstallations(); err == nil {
		for _, item := range list {
			installed[item.Kind+"/"+item.ID] = item
		}
	}
	out := make([]repoPluginView, 0, len(index.Plugins)+len(validated.Rejected))
	icons := index.IconMap()
	locale := i18n.FromContext(c)
	for _, plugin := range index.Plugins {
		if kindFilter != "" && plugin.Kind != kindFilter {
			continue
		}
		if query != "" && !pluginMatchesQuery(plugin, query) {
			continue
		}
		asset, platformOK := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		size := asset.UncompressedSize
		if size <= 0 {
			size = plugin.UncompressedSize
		}
		if size <= 0 {
			size = asset.Size
		}
		item := repoPluginView{
			Kind: plugin.Kind, ID: plugin.ID, Name: plugin.Name, Description: plugin.Description,
			Version: plugin.Version, Protocol: plugin.Protocol, MinHost: plugin.MinHost,
			Bundle: plugin.Bundle, ProviderCount: len(plugin.Providers),
			Compatible: pluginrepo.Compatible(plugin, Version), PlatformAvailable: platformOK,
			Icon: pluginMarketIcon(plugin, icons),
			Size: size, DownloadSize: asset.Size,
		}
		if record, found := installed[plugin.Kind+"/"+plugin.ID]; found {
			item.Installed = true
			item.InstalledVersion = record.Version
			item.Source = record.Source
			item.UpdateAvailable = repoUpdateAvailable(plugin, asset, record)
		}
		out = append(out, item)
	}
	// 被隔离条目仍然展示，但明确标记不可安装，避免“插件从市场消失”让管理员无从排查。
	for _, rejected := range validated.Rejected {
		plugin := rejected.Plugin
		if kindFilter != "" && plugin.Kind != kindFilter {
			continue
		}
		if query != "" && !pluginMatchesQuery(plugin, query) {
			continue
		}
		asset, _ := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		size := asset.UncompressedSize
		if size <= 0 {
			size = plugin.UncompressedSize
		}
		if size <= 0 {
			size = asset.Size
		}
		item := repoPluginView{
			Kind: plugin.Kind, ID: plugin.ID, Name: plugin.Name, Description: plugin.Description,
			Version: plugin.Version, Protocol: plugin.Protocol, MinHost: plugin.MinHost,
			Bundle: plugin.Bundle, ProviderCount: len(plugin.Providers),
			Compatible: false, PlatformAvailable: false,
			Icon: pluginMarketIcon(plugin, icons), Size: size, DownloadSize: asset.Size,
			Invalid: true, InvalidCode: rejected.Reason.Code,
			InvalidReason: service.SanitizeErrorText(rejected.Reason.Text(locale)),
		}
		if record, found := installed[plugin.Kind+"/"+plugin.ID]; found {
			item.Installed = true
			item.InstalledVersion = record.Version
			item.Source = record.Source
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	ok(c, gin.H{"plugins": out, "platform": runtime.GOOS + "/" + runtime.GOARCH, "host_version": Version})
}

func pluginMatchesQuery(plugin pluginrepo.Plugin, query string) bool {
	if strings.Contains(strings.ToLower(plugin.ID), query) ||
		strings.Contains(strings.ToLower(plugin.Name), query) ||
		strings.Contains(strings.ToLower(plugin.Description), query) {
		return true
	}
	for _, id := range plugin.Providers {
		if strings.Contains(strings.ToLower(id), query) {
			return true
		}
	}
	return false
}

// findKindBundles 返回指定类型的全部合并插件包，按 ID 排序。
func (s *Server) findKindBundles(index pluginrepo.Index, kind string) []pluginrepo.Plugin {
	out := make([]pluginrepo.Plugin, 0, 2)
	for _, plugin := range index.Plugins {
		if plugin.Kind == kind && plugin.Bundle {
			out = append(out, plugin)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) findRepoPlugin(index pluginrepo.Index, kind, id, version string) (pluginrepo.Plugin, bool) {
	for _, plugin := range index.Plugins {
		if plugin.Kind != kind || plugin.ID != id {
			continue
		}
		if version == "" || plugin.Version == version {
			return plugin, true
		}
	}
	return pluginrepo.Plugin{}, false
}

// findRejectedRepoPlugin 返回被隔离条目的原因，用于安装接口给出可诊断的错误。
func findRejectedRepoPlugin(validated pluginrepo.ValidatedIndex, kind, id, version string) (pluginrepo.RejectedEntry, bool) {
	for _, rejected := range validated.Rejected {
		if rejected.Plugin.Kind != kind || rejected.Plugin.ID != id {
			continue
		}
		if version == "" || rejected.Plugin.Version == version {
			return rejected, true
		}
	}
	return pluginrepo.RejectedEntry{}, false
}

type inspectResult struct {
	IDs     []string
	Aliases []string
	Version string
	Digest  string
}

func (s *Server) inspectPluginBinary(kind, path string) (inspectResult, error) {
	switch kind {
	case pluginKindDNS:
		manifest, digest, err := dnsplugin.InspectHelper(path, dnsplugin.DiscoverConfig{
			MemoryLimitMB: s.PluginMemoryLimitMB, Timeout: s.PluginTimeout, MaxOutputKB: s.PluginMaxOutputKB,
		})
		if err != nil {
			return inspectResult{}, err
		}
		ids := make([]string, 0, len(manifest.Providers))
		aliases := make([]string, 0)
		for _, item := range manifest.Providers {
			ids = append(ids, item.ID)
			aliases = append(aliases, item.Aliases...)
		}
		return inspectResult{IDs: ids, Aliases: aliases, Version: manifest.Version, Digest: digest}, nil
	case pluginKindDeploy:
		manifest, digest, err := deployplugin.InspectHelper(path, deployplugin.DiscoverConfig{
			MemoryLimitMB: s.DeployPluginMemoryLimitMB, Timeout: s.DeployPluginTimeout, MaxOutputKB: s.DeployPluginMaxOutputKB,
			KnownIDs: service.BuiltinDeployTypeIDs(),
		})
		if err != nil {
			return inspectResult{}, err
		}
		ids := make([]string, 0, len(manifest.Deployers))
		aliases := make([]string, 0)
		for _, item := range manifest.Deployers {
			ids = append(ids, item.ID)
			aliases = append(aliases, item.Aliases...)
		}
		return inspectResult{IDs: ids, Aliases: aliases, Version: manifest.Version, Digest: digest}, nil
	case pluginKindNotify:
		manifest, digest, err := notifyplugin.InspectHelper(path, notifyplugin.DiscoverConfig{
			MemoryLimitMB: s.NotifyPluginMemoryLimitMB, Timeout: s.NotifyPluginTimeout, MaxOutputKB: s.NotifyPluginMaxOutputKB,
			KnownIDs: service.BuiltinNotifyChannelIDs(),
		})
		if err != nil {
			return inspectResult{}, err
		}
		ids := make([]string, 0, len(manifest.Channels))
		aliases := make([]string, 0)
		for _, item := range manifest.Channels {
			ids = append(ids, item.ID)
			aliases = append(aliases, item.Aliases...)
		}
		return inspectResult{IDs: ids, Aliases: aliases, Version: manifest.Version, Digest: digest}, nil
	default:
		return inspectResult{}, fmt.Errorf("不支持的插件类型: %s", kind)
	}
}

func (s *Server) pluginKindDir(kind string) (string, bool) {
	switch kind {
	case pluginKindDNS:
		return strings.TrimSpace(s.PluginDir), s.PluginsEnabled
	case pluginKindDeploy:
		return strings.TrimSpace(s.DeployPluginDir), s.DeployPluginsEnabled
	case pluginKindNotify:
		return strings.TrimSpace(s.NotifyPluginDir), s.NotifyPluginsEnabled
	default:
		return "", false
	}
}

func (s *Server) rescanPluginKind(kind string) []string {
	switch kind {
	case pluginKindDNS:
		return s.DiscoverPlugins().Errors
	case pluginKindDeploy:
		return s.DiscoverDeployPlugins().Errors
	case pluginKindNotify:
		return s.DiscoverNotifyPlugins().Errors
	default:
		return nil
	}
}

func (s *Server) installRepoPlugin(kind string, plugin pluginrepo.Plugin, asset pluginrepo.Asset) (model.PluginInstallation, []string, error) {
	return s.installRepoPluginWithAudit(kind, plugin, asset, s.currentRepoAudit(plugin))
}

// currentRepoAudit 从内存索引读取审计身份，供没有显式 ValidatedIndex 的调用方使用。
func (s *Server) currentRepoAudit(plugin pluginrepo.Plugin) pluginrepo.IndexAudit {
	s.pluginRepoMu.Lock()
	audit := pluginrepo.IndexAudit{
		Revision:    s.pluginRepoRevision,
		IndexDigest: s.pluginRepoIndexDigest,
		EntryDigest: pluginrepo.PluginDigest(plugin),
	}
	s.pluginRepoMu.Unlock()
	return audit
}

func (s *Server) installRepoPluginWithAudit(kind string, plugin pluginrepo.Plugin, asset pluginrepo.Asset, audit pluginrepo.IndexAudit) (model.PluginInstallation, []string, error) {
	if err := s.requireSignedRepoIndex(); err != nil {
		return model.PluginInstallation{}, nil, err
	}
	if len(plugin.Assets) > 0 {
		if err := repoEntryValidationError(plugin, s.repoParseOptions()); err != nil {
			return model.PluginInstallation{}, nil, err
		}
	}
	dir, enabled := s.pluginKindDir(kind)
	if !enabled {
		return model.PluginInstallation{}, nil, fmt.Errorf("插件类型 %s 未启用", kind)
	}
	if dir == "" {
		return model.PluginInstallation{}, nil, fmt.Errorf("插件目录不可用")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.PluginInstallation{}, nil, err
	}
	filename := plugininstall.Filename(kind, plugin.ID, runtime.GOOS)
	target, err := plugininstall.SafePath(dir, filename)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	client, err := s.repoClient()
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	archive, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	archivePath := archive.Name()
	_ = archive.Close()
	defer os.Remove(archivePath)
	ctx, cancel := context.WithTimeout(context.Background(), s.PluginRepoTimeout)
	defer cancel()
	assetDigest, err := client.Download(ctx, asset, archivePath)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	extractDir, err := os.MkdirTemp(dir, ".extract-*")
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	defer os.RemoveAll(extractDir)
	meta, binaryPath, err := pluginrepo.ExtractPackage(archivePath, extractDir)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	if meta.Kind != plugin.Kind || meta.ID != plugin.ID || meta.Version != plugin.Version || meta.OS != runtime.GOOS || meta.Arch != runtime.GOARCH || meta.Bundle != plugin.Bundle {
		return model.PluginInstallation{}, nil, fmt.Errorf("plugin.json 与仓库索引不一致")
	}
	inspect, err := s.inspectPluginBinary(kind, binaryPath)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	if plugin.Bundle {
		if len(inspect.IDs) == 0 {
			return model.PluginInstallation{}, nil, fmt.Errorf("合并插件 manifest 为空")
		}
		if len(plugin.Providers) > 0 && !sameStringSet(inspect.IDs, plugin.Providers) {
			return model.PluginInstallation{}, nil, fmt.Errorf("合并插件 manifest 与索引不一致: 期望 %d 个条目，实际 %d 个", len(plugin.Providers), len(inspect.IDs))
		}
	} else if len(inspect.IDs) != 1 || inspect.IDs[0] != plugin.ID {
		return model.PluginInstallation{}, nil, fmt.Errorf("插件 manifest 必须只包含 %s，实际为 %v", plugin.ID, inspect.IDs)
	}
	if inspect.Version != plugin.Version {
		return model.PluginInstallation{}, nil, fmt.Errorf("插件版本 %s 与索引 %s 不一致", inspect.Version, plugin.Version)
	}
	// 写入目录前统一检查 ID 与别名冲突；完全被合并包覆盖的旧文件由
	// cleanupSupersededHelpers 在安装后清理，不要求管理员手动卸载。
	// 升级时安装记录里的旧文件名也要排除，避免把即将被替换的文件当成冲突源。
	oldRecord, hadOld, _ := s.Store.GetPluginInstallation(kind, plugin.ID)
	ignoredFiles := []string{filename}
	if hadOld && oldRecord.Filename != "" {
		ignoredFiles = append(ignoredFiles, oldRecord.Filename)
	}
	if conflict := s.detectPluginConflicts(kind, inspect.IDs, inspect.Aliases, plugin.Bundle, plugin.ID, plugin.Name, ignoredFiles...); conflict != nil {
		return model.PluginInstallation{}, nil, conflict
	}
	// 记录安装前文件是否已存在：只有全新的 helper 才默认停用文件级加载。
	_, statErr := os.Lstat(target)
	fileExisted := statErr == nil
	dest, backup, err := plugininstall.Install(dir, filename, binaryPath)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	record := model.PluginInstallation{
		Kind: kind, ID: plugin.ID, Version: plugin.Version, Filename: filename,
		SHA256: inspect.Digest, AssetSHA256: assetDigest, Source: "repo", RepoURL: redactRepoURL(s.repoURL()),
		IndexRevision: audit.Revision, IndexDigest: audit.IndexDigest, EntryDigest: audit.EntryDigest,
	}
	if err := s.Store.UpsertPluginInstallation(record); err != nil {
		plugininstall.Rollback(dest, backup)
		return model.PluginInstallation{}, nil, err
	}
	plugininstall.Commit(backup)
	if err := s.savePluginIcons(kind, plugin.ID, meta.Icons, filepath.Join(extractDir, "icons")); err != nil {
		s.Store.AddLogKey("settings", 0, "warning", "log.settings.plugin_icons_failed", map[string]any{
			"kind": kind, "id": plugin.ID, "error": err.Error(),
		})
	}
	if hadOld && oldRecord.Filename != "" && oldRecord.Filename != filename {
		oldItems, _ := s.pluginFileItems(kind, filepath.Join(dir, oldRecord.Filename))
		_ = plugininstall.Remove(dir, oldRecord.Filename)
		s.removePluginFileData(kind, oldRecord.Filename, oldItems)
	}
	// 缓存安装时校验过的完整清单：合并包条目很多，后续发现结果可能省略冲突项。
	s.upsertPluginFileMeta(kind, filename, plugin.Name, plugin.Version, inspect.Digest, inspect.IDs)
	s.prunePluginEnabledWhitelist(kind)
	cleanupErrors := []string(nil)
	if plugin.Bundle {
		_, cleanupErrors = s.cleanupSupersededHelpers(kind, dir, filename, plugin.Providers)
	}
	// 新下载的插件默认停用文件级加载与其中条目；升级或重装保留原启用状态。
	// 写入放在覆盖清理之后，让清理仍能依据新文件提供的条目回收旧 helper。
	if !hadOld && !fileExisted {
		s.disablePluginItemsForNewFile(kind, filename, inspect.IDs)
		if err := s.updatePluginFileEnabled(kind, filename, false); err != nil {
			s.Store.AddLogKey("settings", 0, "warning", "log.settings.plugin_file_state_failed", map[string]any{
				"kind": kind, "file": filename, "error": err.Error(),
			})
		}
	}
	scanErrors := append(cleanupErrors, s.rescanPluginKind(kind)...)
	s.Store.AddLogKey("settings", 0, "info", "log.settings.plugin_installed", map[string]any{
		"kind": kind, "id": plugin.ID, "version": plugin.Version, "source": "repo",
	})
	return record, scanErrors, nil
}

// sameStringSet 判断两个字符串集合是否完全一致（忽略顺序与重复）。
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, item := range a {
		seen[item]++
	}
	for _, item := range b {
		seen[item]--
		if seen[item] < 0 {
			return false
		}
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

// cleanupSupersededHelpers 删除被当前目录内其他 helper 完全覆盖的旧文件及安装记录。
// 这里按目录内全部可解析 helper 的并集判断覆盖率，因此 DNS 常用包和重型包
// 可以分两次安装，第二次安装后会清掉旧的单服务商/旧全量 helper。
func (s *Server) cleanupSupersededHelpers(kind, dir, keepFilename string, providers []string) ([]string, []string) {
	allowed := make(map[string]bool, len(providers))
	for _, id := range providers {
		allowed[canonicalPluginID(kind, id)] = true
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{err.Error()}
	}
	type helperInfo struct {
		name string
		ids  []string
	}
	infos := make([]helperInfo, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.Type().IsRegular() {
			continue
		}
		if s.pluginFileEnabled(kind, name) {
			inspect, err := s.inspectPluginBinary(kind, filepath.Join(dir, name))
			if err != nil {
				continue
			}
			infos = append(infos, helperInfo{name: name, ids: inspect.IDs})
			continue
		}
		// 已停用的文件不执行 manifest，只用安装/上传时缓存的清单判断覆盖关系，
		// 避免为了回收旧条目而执行被隔离的 helper。
		if ids, _ := s.pluginFileItems(kind, filepath.Join(dir, name)); len(ids) > 0 {
			infos = append(infos, helperInfo{name: name, ids: ids})
		}
	}
	provided := func(exclude, id string) bool {
		for _, info := range infos {
			if info.name == exclude {
				continue
			}
			for _, item := range info.ids {
				if canonicalPluginID(kind, item) == id {
					return true
				}
			}
		}
		return false
	}
	records := map[string][]model.PluginInstallation{}
	if list, err := s.Store.ListPluginInstallations(); err == nil {
		for _, record := range list {
			if record.Kind == kind {
				records[record.Filename] = append(records[record.Filename], record)
			}
		}
	}
	var removed, errs []string
	for _, info := range infos {
		if info.name == keepFilename {
			continue
		}
		overlap := false
		for _, id := range info.ids {
			if allowed[canonicalPluginID(kind, id)] {
				overlap = true
				break
			}
		}
		if !overlap {
			continue
		}
		covered := true
		for _, id := range info.ids {
			if !provided(info.name, canonicalPluginID(kind, id)) {
				covered = false
				break
			}
		}
		if !covered {
			errs = append(errs, fmt.Sprintf("%s 仍包含未覆盖的条目，已保留", info.name))
			continue
		}
		if err := plugininstall.Remove(dir, info.name); err != nil {
			errs = append(errs, fmt.Sprintf("删除旧插件 %s 失败: %v", info.name, err))
			continue
		}
		for _, record := range records[info.name] {
			_ = s.Store.DeletePluginInstallation(kind, record.ID)
		}
		s.removePluginFileData(kind, info.name, info.ids)
		removed = append(removed, info.name)
	}
	return removed, errs
}

// canonicalPluginID 把已合并的旧部署目标 ID 归一化为规范 ID。
func canonicalPluginID(kind, id string) string {
	if kind == pluginKindDeploy {
		if canonical, ok := service.DeprecatedDeployTypeCanonical[id]; ok {
			return canonical
		}
	}
	return id
}

// installPluginFromRepo 安装仓库中的插件。
func (s *Server) installPluginFromRepo(c *gin.Context) {
	var body struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	id := strings.ToLower(strings.TrimSpace(body.ID))
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	validated, err := s.loadRepoValidated(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	if err := repoExpiredError(validated, time.Now()); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	plugin, found := s.findRepoPlugin(validated.Index, kind, id, strings.TrimSpace(body.Version))
	if !found {
		if rejected, ok := findRejectedRepoPlugin(validated, kind, id, strings.TrimSpace(body.Version)); ok {
			failErr(c, http.StatusBadRequest, i18n.E("plugin.repo.entry_invalid",
				"plugin", kind+"/"+id, "reason", rejected.Reason.Text(i18n.FromContext(c))))
			return
		}
		failKey(c, http.StatusNotFound, "api.error.plugin_repo_plugin_missing")
		return
	}
	if !pluginrepo.Compatible(plugin, Version) {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_incompatible")
		return
	}
	asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
	if !okAsset {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_platform_unsupported")
		return
	}
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	record, scanErrors, err := s.installRepoPluginWithAudit(kind, plugin, asset, validated.Audit(plugin))
	if err != nil {
		if failPluginConflict(c, err) {
			return
		}
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ok(c, gin.H{"plugin": record, "errors": scanErrors, "message": "插件安装完成"})
}

// updatePluginFromRepo 更新单个或全部已安装插件。
func (s *Server) updatePluginFromRepo(c *gin.Context) {
	var body struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	validated, err := s.loadRepoValidated(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	if err := repoExpiredError(validated, time.Now()); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	index := validated.Index
	records, err := s.Store.ListPluginInstallations()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	id := strings.ToLower(strings.TrimSpace(body.ID))
	targeted := kind != "" && id != ""
	matched := 0
	results := make([]gin.H, 0)
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	for _, record := range records {
		if kind != "" && record.Kind != kind {
			continue
		}
		if id != "" && record.ID != id {
			continue
		}
		matched++
		plugin, found := s.findRepoPlugin(index, record.Kind, record.ID, "")
		if !found {
			// 前端据此把“可更新”改成真实状态，而不是继续显示旧标记。
			results = append(results, gin.H{
				"kind": record.Kind, "id": record.ID, "from": record.Version,
				"updated": false, "error": "仓库中不存在该插件",
			})
			continue
		}
		asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		if !okAsset || !pluginrepo.Compatible(plugin, Version) {
			results = append(results, gin.H{
				"kind": record.Kind, "id": record.ID, "from": record.Version,
				"to": plugin.Version, "updated": false, "error": "当前平台或宿主版本不兼容",
			})
			continue
		}
		if !repoUpdateAvailable(plugin, asset, record) {
			// 保留“无需更新”的结果，前端能区分“已是最新”和“没有找到安装记录”。
			results = append(results, gin.H{
				"kind": record.Kind, "id": record.ID, "from": record.Version,
				"to": record.Version, "updated": false,
			})
			continue
		}
		updatedRecord, scanErrors, err := s.installRepoPluginWithAudit(record.Kind, plugin, asset, validated.Audit(plugin))
		item := gin.H{
			"kind": record.Kind, "id": record.ID, "from": record.Version,
			"to": plugin.Version, "updated": err == nil, "errors": scanErrors,
		}
		if err != nil {
			item["error"] = err.Error()
			var conflictErr *pluginConflictError
			if errors.As(err, &conflictErr) {
				item["code"] = "api.error.plugin_conflict"
				item["conflicts"] = conflictErr.Conflicts
			}
		} else {
			// 直接返回更新后的安装记录，前端可立即刷新该行状态，无需等待二次列表请求。
			item["plugin"] = updatedRecord
		}
		results = append(results, item)
	}
	if targeted && matched == 0 {
		// 指定了 kind/id 却没有任何安装记录：以前会静默返回成功，界面因此一直
		// 显示“可更新”。这里明确报错，让前端刷新列表并提示真实原因。
		failKey(c, http.StatusNotFound, "api.error.plugin_repo_not_installed")
		return
	}
	ok(c, gin.H{"results": results})
}

// uninstallPluginFromRepo 卸载由仓库安装的插件。
func (s *Server) uninstallPluginFromRepo(c *gin.Context) {
	kind := strings.ToLower(strings.TrimSpace(c.Param("kind")))
	id := strings.ToLower(strings.TrimSpace(c.Param("id")))
	record, found, err := s.Store.GetPluginInstallation(kind, id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !found {
		failKey(c, http.StatusNotFound, "api.error.plugin_repo_not_installed")
		return
	}
	dir, _ := s.pluginKindDir(kind)
	if dir == "" {
		failKey(c, http.StatusBadRequest, "api.error.plugin_dir_unavailable")
		return
	}
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	items, _ := s.pluginFileItems(kind, filepath.Join(dir, record.Filename))
	if err := plugininstall.Remove(dir, record.Filename); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if err := s.Store.DeletePluginInstallation(kind, id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	_ = s.removePluginIcons(kind, id)
	s.removePluginFileData(kind, record.Filename, items)
	scanErrors := s.rescanPluginKind(kind)
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.plugin_uninstalled", map[string]any{"kind": kind, "id": id})
	ok(c, gin.H{"uninstalled": kind + "/" + id, "errors": scanErrors})
}

// listLegacyPlugins 返回检测到的旧版全量 helper 文件。
func (s *Server) listLegacyPlugins(c *gin.Context) {
	items := make([]gin.H, 0)
	for kind, names := range legacyPluginNames {
		dir, _ := s.pluginKindDir(kind)
		if dir == "" {
			continue
		}
		for _, name := range names {
			for _, candidate := range []string{name, name + ".exe"} {
				path := filepath.Join(dir, candidate)
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					items = append(items, gin.H{"kind": kind, "file": candidate, "path": path, "size": info.Size()})
				}
			}
		}
	}
	ok(c, gin.H{"legacy": items})
}

func (s *Server) legacyPluginPath(kind string) (string, bool) {
	dir, _ := s.pluginKindDir(kind)
	if dir == "" {
		return "", false
	}
	for _, name := range legacyPluginNames[kind] {
		for _, candidate := range []string{name, name + ".exe"} {
			path := filepath.Join(dir, candidate)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, true
			}
		}
	}
	return "", false
}

func (s *Server) legacyProviderIDs(kind, legacyPath string) []string {
	out := make([]string, 0)
	switch kind {
	case pluginKindDNS:
		for _, item := range dnsplugin.ExternalProviders() {
			if filepath.Clean(item.Helper) == filepath.Clean(legacyPath) {
				out = append(out, item.ID)
			}
		}
	case pluginKindDeploy:
		for _, item := range deployplugin.ExternalDeployers() {
			if filepath.Clean(item.Helper) == filepath.Clean(legacyPath) {
				out = append(out, item.ID)
			}
		}
	case pluginKindNotify:
		for _, item := range notifyplugin.ExternalChannels() {
			if filepath.Clean(item.Helper) == filepath.Clean(legacyPath) {
				out = append(out, item.ID)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) requiredLegacyIDs(kind string, legacyIDs []string) []string {
	allowed := map[string]bool{}
	for _, id := range legacyIDs {
		allowed[id] = true
	}
	used := map[string]bool{}
	switch kind {
	case pluginKindDNS:
		for _, id := range s.dnsPluginEnabledProviders() {
			used[id] = true
		}
		if accounts, err := s.Store.ListDNSAccounts(); err == nil {
			for _, account := range accounts {
				used[account.Provider] = true
			}
		}
	case pluginKindDeploy:
		for _, id := range s.deployPluginEnabledDeployers() {
			used[id] = true
		}
		if accounts, err := s.Store.ListDeployAccounts(); err == nil {
			for _, account := range accounts {
				used[account.Type] = true
			}
		}
	case pluginKindNotify:
		for _, id := range s.notifyPluginEnabledChannels() {
			used[id] = true
		}
		if s.Manager != nil {
			for id := range s.Manager.LoadNotifyChannels() {
				used[id] = true
			}
		}
	}
	if kind == pluginKindDeploy {
		for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
			if used[legacy] {
				used[canonical] = true
			}
			// 旧 helper 可能只声明旧 ID，迁移时需要按规范 ID 安装。
			if allowed[legacy] {
				allowed[canonical] = true
			}
		}
	}
	out := make([]string, 0, len(used))
	for id := range used {
		if allowed[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// migrateLegacyPlugins 把旧 bundle 中正在使用的插件替换为仓库单插件包。
func (s *Server) migrateLegacyPlugins(c *gin.Context) {
	var body struct {
		Kind string `json:"kind"`
	}
	_ = c.ShouldBindJSON(&body)
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	if kind != pluginKindDNS && kind != pluginKindDeploy && kind != pluginKindNotify {
		failKey(c, http.StatusBadRequest, "api.error.plugin_kind_invalid")
		return
	}
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	legacyPath, found := s.legacyPluginPath(kind)
	if !found {
		failKey(c, http.StatusNotFound, "api.error.plugin_repo_no_legacy")
		return
	}
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	validated, err := s.loadRepoValidated(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	if err := repoExpiredError(validated, time.Now()); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	index := validated.Index
	legacyIDs := s.legacyProviderIDs(kind, legacyPath)
	required := s.requiredLegacyIDs(kind, legacyIDs)
	if len(required) == 0 {
		// 没有正在使用的服务商，直接移除旧文件。
		if err := os.Remove(legacyPath); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		s.dropPluginFileMeta(kind, filepath.Base(legacyPath))
		scanErrors := s.rescanPluginKind(kind)
		ok(c, gin.H{"migrated": []string{}, "errors": scanErrors})
		return
	}
	bundles := s.findKindBundles(index, kind)
	if len(required) > 0 {
		needed := make(map[string]bool, len(required))
		for _, id := range required {
			needed[id] = true
		}
		filtered := make([]pluginrepo.Plugin, 0, len(bundles))
		for _, bundle := range bundles {
			for _, id := range bundle.Providers {
				if needed[id] {
					filtered = append(filtered, bundle)
					break
				}
			}
		}
		bundles = filtered
	}
	if len(bundles) > 0 {
		type bundleAsset struct {
			plugin pluginrepo.Plugin
			asset  pluginrepo.Asset
		}
		items := make([]bundleAsset, 0, len(bundles))
		for _, bundle := range bundles {
			asset, okAsset := pluginrepo.SelectAsset(bundle, runtime.GOOS, runtime.GOARCH)
			if !okAsset || !pluginrepo.Compatible(bundle, Version) {
				failKey(c, http.StatusBadRequest, "api.error.plugin_repo_incompatible")
				return
			}
			items = append(items, bundleAsset{plugin: bundle, asset: asset})
		}
		hidden := filepath.Join(filepath.Dir(legacyPath), ".migrating-"+filepath.Base(legacyPath))
		if err := os.Rename(legacyPath, hidden); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		dir, _ := s.pluginKindDir(kind)
		installed := make([]string, 0, len(items))
		for _, item := range items {
			if _, _, err := s.installRepoPluginWithAudit(kind, item.plugin, item.asset, validated.Audit(item.plugin)); err != nil {
				for _, id := range installed {
					record, found, _ := s.Store.GetPluginInstallation(kind, id)
					if found {
						_ = plugininstall.Remove(dir, record.Filename)
						_ = s.Store.DeletePluginInstallation(kind, id)
						s.dropPluginFileMeta(kind, record.Filename)
					}
				}
				_ = os.Rename(hidden, legacyPath)
				s.rescanPluginKind(kind)
				if failPluginConflict(c, err) {
					return
				}
				failErr(c, http.StatusBadRequest, err)
				return
			}
			installed = append(installed, item.plugin.ID)
		}
		if err := os.Remove(hidden); err != nil {
			for _, id := range installed {
				record, found, _ := s.Store.GetPluginInstallation(kind, id)
				if found {
					_ = plugininstall.Remove(dir, record.Filename)
					_ = s.Store.DeletePluginInstallation(kind, id)
					s.dropPluginFileMeta(kind, record.Filename)
				}
			}
			_ = os.Rename(hidden, legacyPath)
			s.rescanPluginKind(kind)
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		scanErrors := s.rescanPluginKind(kind)
		ids := make([]string, 0)
		for _, item := range items {
			ids = append(ids, item.plugin.Providers...)
		}
		s.Store.AddLogKey("settings", 0, "warning", "log.settings.plugin_repo_migrated", map[string]any{"kind": kind, "count": len(ids)})
		ok(c, gin.H{"migrated": ids, "errors": scanErrors})
		return
	}
	hidden := filepath.Join(filepath.Dir(legacyPath), ".migrating-"+filepath.Base(legacyPath))
	if err := os.Rename(legacyPath, hidden); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.rescanPluginKind(kind)
	dir, _ := s.pluginKindDir(kind)
	installed := make([]string, 0, len(required))
	rollback := func() {
		for _, id := range installed {
			record, found, _ := s.Store.GetPluginInstallation(kind, id)
			if found {
				_ = plugininstall.Remove(dir, record.Filename)
				_ = s.Store.DeletePluginInstallation(kind, id)
				s.dropPluginFileMeta(kind, record.Filename)
			}
		}
		_ = os.Rename(hidden, legacyPath)
		s.rescanPluginKind(kind)
	}
	for _, id := range required {
		plugin, found := s.findRepoPlugin(index, kind, id, "")
		if !found {
			rollback()
			failErr(c, http.StatusBadRequest, fmt.Errorf("仓库缺少正在使用的插件 %s/%s", kind, id))
			return
		}
		asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		if !okAsset || !pluginrepo.Compatible(plugin, Version) {
			rollback()
			failErr(c, http.StatusBadRequest, fmt.Errorf("插件 %s/%s 当前平台或宿主版本不兼容", kind, id))
			return
		}
		if _, _, err := s.installRepoPluginWithAudit(kind, plugin, asset, validated.Audit(plugin)); err != nil {
			rollback()
			if failPluginConflict(c, err) {
				return
			}
			failErr(c, http.StatusBadRequest, err)
			return
		}
		installed = append(installed, id)
	}
	if err := os.Remove(hidden); err != nil {
		rollback()
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	scanErrors := s.rescanPluginKind(kind)
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.plugin_repo_migrated", map[string]any{"kind": kind, "count": len(installed)})
	ok(c, gin.H{"migrated": installed, "errors": scanErrors})
}

func validateRepoURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("插件仓库地址不能为空")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("插件仓库地址无效: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("插件仓库地址必须是有效的 http 或 https 地址")
		}
	case "file":
		if parsed.Path == "" || parsed.Host != "" {
			return fmt.Errorf("插件仓库 file 地址必须包含绝对路径")
		}
	default:
		return fmt.Errorf("插件仓库地址只支持 http、https 或 file")
	}
	return nil
}
