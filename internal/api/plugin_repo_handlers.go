package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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
	repoIndexSetting       = "plugins.repo.index"
	repoIndexMACSetting    = "plugins.repo.index_mac"
	repoETagSetting        = "plugins.repo.index_etag"
	repoCheckedSetting     = "plugins.repo.checked_at"
	repoVerifiedSetting    = "plugins.repo.signature_verified"
	repoVerifiedKeySetting = "plugins.repo.verified_key"
)

// repoIndexMAC 用服务端密钥保护本地缓存的索引，防止数据库被篡改后
// 直接注入包含恶意资产地址的索引。
func (s *Server) repoIndexMAC(raw string) string {
	if s.Auth == nil {
		return ""
	}
	return s.Auth.SignValue("plugin-index-cache", raw)
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

func (s *Server) repoClient() (*pluginrepo.Client, error) {
	return pluginrepo.NewClient(s.repoURL(), s.repoChannel(), s.repoPublicKey(), s.PluginRepoTimeout, s.PluginRepoMaxDownloadMB)
}

// repoKeyFingerprint 返回当前配置公钥的指纹；未配置或格式非法时返回空/占位。
func (s *Server) repoKeyFingerprint() string {
	value := s.repoPublicKey()
	if value == "" {
		return ""
	}
	parsed, err := pluginrepo.ParsePublicKey(value)
	if err != nil {
		return "invalid"
	}
	return pluginrepo.PublicKeyFingerprint(parsed)
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

func (s *Server) repoStatus() gin.H {
	s.pluginRepoMu.Lock()
	index := s.pluginRepoIndex
	checkedAt := s.pluginRepoCheckedAt
	lastErr := s.pluginRepoErr
	verified := s.pluginRepoVerified
	s.pluginRepoMu.Unlock()
	checked := ""
	if !checkedAt.IsZero() {
		checked = checkedAt.Format(time.RFC3339)
	}
	// 只返回公钥指纹，不回显公钥本身。
	publicKey := s.repoPublicKey()
	publicKeyValid := false
	fingerprint := ""
	if publicKey != "" {
		if parsed, err := pluginrepo.ParsePublicKey(publicKey); err == nil {
			publicKeyValid = true
			fingerprint = pluginrepo.PublicKeyFingerprint(parsed)
		}
	}
	return gin.H{
		"enabled":                s.repoEnabled(),
		"url":                    s.repoURL(),
		"channel":                s.repoChannel(),
		"auto_update":            s.repoAutoUpdate(),
		"public_key_set":         publicKey != "",
		"public_key_valid":       publicKeyValid,
		"public_key_fingerprint": fingerprint,
		"signature_verified":     publicKeyValid && verified,
		"timeout_seconds":        int(s.PluginRepoTimeout.Seconds()),
		"max_download_mb":        s.PluginRepoMaxDownloadMB,
		"platform":               runtime.GOOS + "/" + runtime.GOARCH,
		"host_version":           Version,
		"checked_at":             checked,
		"plugins":                len(index.Plugins),
		"error":                  lastErr,
	}
}

// loadRepoIndex 返回仓库索引。内存/本地缓存未命中时才访问网络，
// 网络请求不在 pluginRepoMu 内执行，避免慢仓库阻塞插件页状态与图标接口；
// 并发刷新通过 singleflight 合并为一次请求。
func (s *Server) loadRepoIndex(force bool) (pluginrepo.Index, error) {
	if !force {
		s.pluginRepoMu.Lock()
		if len(s.pluginRepoIndexRaw) > 0 {
			index := s.pluginRepoIndex
			s.pluginRepoMu.Unlock()
			return index, nil
		}
		s.pluginRepoMu.Unlock()
		if index, ok := s.loadCachedRepoIndex(); ok {
			return index, nil
		}
	}
	key := "plugin-repo-index"
	if force {
		key = "plugin-repo-index:force"
	}
	value, err, _ := s.repoGroup.Do(key, func() (any, error) {
		return s.fetchRepoIndex(force)
	})
	if err != nil {
		s.setRepoError(err)
		return pluginrepo.Index{}, err
	}
	index, _ := value.(pluginrepo.Index)
	return index, nil
}

// loadCachedRepoIndex 读取数据库中的索引缓存（带服务端 HMAC 校验）。
func (s *Server) loadCachedRepoIndex() (pluginrepo.Index, bool) {
	raw, ok := s.cachedRepoIndexRaw()
	if !ok {
		// 缓存缺失/被篡改时不能继续带旧 ETag：否则服务端返回 304 后仍会因
		// 缓存校验失败而无法刷新。
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.Index{}, false
	}
	var index pluginrepo.Index
	if err := json.Unmarshal([]byte(raw), &index); err != nil {
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.Index{}, false
	}
	index.SanitizeIcons()
	if err := index.Validate(); err != nil {
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.Index{}, false
	}
	checkedAt := time.Time{}
	if ts := s.setting(repoCheckedSetting, ""); ts != "" {
		checkedAt, _ = time.Parse(time.RFC3339, ts)
	}
	// 缓存索引由服务端 HMAC 保护，但签名状态必须绑定到具体公钥：
	// 仅修改 config.yaml 公钥并重启时，旧索引不能用新公钥冒充“已验签”。
	verifiedKey := s.setting(repoVerifiedKeySetting, "")
	currentKey := s.repoKeyFingerprint()
	storedVerified := s.setting(repoVerifiedSetting, "") == "true"
	if storedVerified && verifiedKey != currentKey && currentKey != "" {
		// 公钥已变更：丢弃 ETag，强制重新拉取并用新公钥验签。
		_ = s.Store.DeleteSetting(repoETagSetting)
		return pluginrepo.Index{}, false
	}
	verified := storedVerified && currentKey != "" && verifiedKey == currentKey

	s.pluginRepoMu.Lock()
	defer s.pluginRepoMu.Unlock()
	// 并发刷新可能已经写入更新的内存缓存，优先使用它。
	if len(s.pluginRepoIndexRaw) > 0 {
		return s.pluginRepoIndex, true
	}
	s.pluginRepoIndex = index
	s.pluginRepoIndexRaw = []byte(raw)
	s.pluginRepoCheckedAt = checkedAt
	s.pluginRepoErr = ""
	s.pluginRepoVerified = verified
	return index, true
}

// fetchRepoIndex 执行实际的网络拉取。仅在 singleflight 内调用。
func (s *Server) fetchRepoIndex(force bool) (pluginrepo.Index, error) {
	// 等待 singleflight 期间其他调用可能已完成刷新，这里再检查一次内存缓存。
	if !force {
		s.pluginRepoMu.Lock()
		if len(s.pluginRepoIndexRaw) > 0 {
			index := s.pluginRepoIndex
			s.pluginRepoMu.Unlock()
			return index, nil
		}
		s.pluginRepoMu.Unlock()
	}
	client, err := s.repoClient()
	if err != nil {
		return pluginrepo.Index{}, err
	}
	s.pluginRepoMu.Lock()
	generation := s.pluginRepoGen
	s.pluginRepoMu.Unlock()
	etag := s.setting(repoETagSetting, "")
	currentKey := s.repoKeyFingerprint()
	storedKey := s.setting(repoVerifiedKeySetting, "")
	if s.setting(repoVerifiedSetting, "") == "true" && storedKey != currentKey {
		// 公钥已变更：旧 ETag 对应的缓存不是用当前公钥验签的，必须全量重取。
		etag = ""
		_ = s.Store.DeleteSetting(repoETagSetting)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.PluginRepoTimeout)
	defer cancel()
	index, raw, newETag, notModified, err := client.Fetch(ctx, etag)
	if err != nil {
		return pluginrepo.Index{}, err
	}
	if notModified {
		rawText, ok := s.cachedRepoIndexRaw()
		if !ok || (s.setting(repoVerifiedSetting, "") == "true" && storedKey != currentKey) {
			// 缓存缺失/被篡改或来自旧版本时，忽略 ETag 重新拉取一次。
			index, raw, newETag, _, err = client.Fetch(ctx, "")
			if err != nil {
				return pluginrepo.Index{}, err
			}
			rawText = string(raw)
		}
		if err := json.Unmarshal([]byte(rawText), &index); err != nil {
			return pluginrepo.Index{}, err
		}
		index.SanitizeIcons()
		if err := index.Validate(); err != nil {
			return pluginrepo.Index{}, err
		}
		raw = []byte(rawText)
	}
	checkedAt := time.Now().UTC()
	verified := client.PublicKey != nil
	verifiedKey := ""
	if client.PublicKey != nil {
		verifiedKey = pluginrepo.PublicKeyFingerprint(client.PublicKey)
	}

	s.pluginRepoMu.Lock()
	if s.pluginRepoGen != generation {
		s.pluginRepoMu.Unlock()
		return pluginrepo.Index{}, i18n.E("api.error.plugin_repo_changed")
	}
	s.pluginRepoIndex = index
	s.pluginRepoIndexRaw = raw
	s.pluginRepoETag = newETag
	s.pluginRepoCheckedAt = checkedAt
	s.pluginRepoErr = ""
	s.pluginRepoVerified = verified
	s.pluginRepoMu.Unlock()

	// 数据库写入放在锁外，避免 SQLite 短暂阻塞影响其他读缓存请求。
	_ = s.Store.SetSetting(repoIndexSetting, string(raw))
	_ = s.Store.SetSetting(repoIndexMACSetting, s.repoIndexMAC(string(raw)))
	if newETag != "" {
		_ = s.Store.SetSetting(repoETagSetting, newETag)
	}
	_ = s.Store.SetSetting(repoCheckedSetting, checkedAt.Format(time.RFC3339))
	_ = s.Store.SetSetting(repoVerifiedSetting, strconv.FormatBool(verified))
	_ = s.Store.SetSetting(repoVerifiedKeySetting, verifiedKey)
	return index, nil
}

// setRepoError 记录最近一次索引拉取错误。
func (s *Server) setRepoError(err error) {
	if err == nil {
		return
	}
	s.pluginRepoMu.Lock()
	s.pluginRepoErr = err.Error()
	s.pluginRepoMu.Unlock()
}

func (s *Server) clearRepoIndexCache() {
	s.pluginRepoMu.Lock()
	s.pluginRepoGen++
	s.pluginRepoIndex = pluginrepo.Index{}
	s.pluginRepoIndexRaw = nil
	s.pluginRepoETag = ""
	s.pluginRepoCheckedAt = time.Time{}
	s.pluginRepoErr = ""
	s.pluginRepoVerified = false
	s.pluginRepoMu.Unlock()
	_ = s.Store.DeleteSetting(repoIndexSetting)
	_ = s.Store.DeleteSetting(repoIndexMACSetting)
	_ = s.Store.DeleteSetting(repoETagSetting)
	_ = s.Store.DeleteSetting(repoCheckedSetting)
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
			s.Logger.Warn("插件仓库索引拉取失败", "error", err)
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
	index, err := s.loadRepoIndex(false)
	if err != nil {
		return
	}
	records, err := s.Store.ListPluginInstallations()
	if err != nil {
		return
	}
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	for _, record := range records {
		plugin, found := s.findRepoPlugin(index, record.Kind, record.ID, "")
		if !found || pluginrepo.CompareVersion(plugin.Version, record.Version) <= 0 {
			continue
		}
		asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		if !okAsset || !pluginrepo.Compatible(plugin, Version) {
			continue
		}
		if _, _, err := s.installRepoPlugin(record.Kind, plugin, asset); err != nil {
			if s.Logger != nil {
				s.Logger.Warn("插件自动更新失败", "kind", record.Kind, "id", record.ID, "error", err)
			}
		}
	}
}

// getPluginRepo 返回插件仓库配置与当前索引状态。
func (s *Server) getPluginRepo(c *gin.Context) {
	ok(c, s.repoStatus())
}

// updatePluginRepo 修改插件仓库配置。
func (s *Server) updatePluginRepo(c *gin.Context) {
	var body struct {
		Enabled    *bool   `json:"enabled"`
		URL        *string `json:"url"`
		Channel    *string `json:"channel"`
		AutoUpdate *bool   `json:"auto_update"`
		PublicKey  *string `json:"public_key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	changedSource := false
	if body.URL != nil {
		value := strings.TrimSpace(*body.URL)
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
		// 保存前先校验格式，避免写入无法验签的公钥后才发现索引刷新失败。
		if value != "" {
			if _, err := pluginrepo.ParsePublicKey(value); err != nil {
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
	if body.Enabled != nil {
		_ = s.Store.SetSetting(repoEnabledSetting, strconv.FormatBool(*body.Enabled))
	}
	if body.AutoUpdate != nil {
		_ = s.Store.SetSetting(repoAutoUpdateSetting, strconv.FormatBool(*body.AutoUpdate))
	}
	if changedSource {
		s.clearRepoIndexCache()
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.plugin_repo_updated", map[string]any{"url": s.repoURL()})
	ok(c, s.repoStatus())
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
	ok(c, s.repoStatus())
}

// listPluginRepoPlugins 返回仓库中的插件与安装状态。
func (s *Server) listPluginRepoPlugins(c *gin.Context) {
	if !s.repoEnabled() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_repo_disabled")
		return
	}
	index, err := s.loadRepoIndex(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	kindFilter := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	installed := map[string]model.PluginInstallation{}
	if list, err := s.Store.ListPluginInstallations(); err == nil {
		for _, item := range list {
			installed[item.Kind+"/"+item.ID] = item
		}
	}
	out := make([]repoPluginView, 0, len(index.Plugins))
	icons := index.IconMap()
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
			item.UpdateAvailable = pluginrepo.CompareVersion(plugin.Version, record.Version) > 0
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

type inspectResult struct {
	IDs     []string
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
		for _, item := range manifest.Providers {
			ids = append(ids, item.ID)
		}
		return inspectResult{IDs: ids, Version: manifest.Version, Digest: digest}, nil
	case pluginKindDeploy:
		manifest, digest, err := deployplugin.InspectHelper(path, deployplugin.DiscoverConfig{
			MemoryLimitMB: s.DeployPluginMemoryLimitMB, Timeout: s.DeployPluginTimeout, MaxOutputKB: s.DeployPluginMaxOutputKB,
			KnownIDs: service.BuiltinDeployTypeIDs(),
		})
		if err != nil {
			return inspectResult{}, err
		}
		ids := make([]string, 0, len(manifest.Deployers))
		for _, item := range manifest.Deployers {
			ids = append(ids, item.ID)
		}
		return inspectResult{IDs: ids, Version: manifest.Version, Digest: digest}, nil
	case pluginKindNotify:
		manifest, digest, err := notifyplugin.InspectHelper(path, notifyplugin.DiscoverConfig{
			MemoryLimitMB: s.NotifyPluginMemoryLimitMB, Timeout: s.NotifyPluginTimeout, MaxOutputKB: s.NotifyPluginMaxOutputKB,
			KnownIDs: service.BuiltinNotifyChannelIDs(),
		})
		if err != nil {
			return inspectResult{}, err
		}
		ids := make([]string, 0, len(manifest.Channels))
		for _, item := range manifest.Channels {
			ids = append(ids, item.ID)
		}
		return inspectResult{IDs: ids, Version: manifest.Version, Digest: digest}, nil
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

func (s *Server) pluginIDConflict(kind, id, target string) error {
	var helper string
	switch kind {
	case pluginKindDNS:
		if info, ok := dnsplugin.ExternalProviderInfo(id); ok {
			helper = info.Helper
		}
	case pluginKindDeploy:
		if info, ok := deployplugin.ExternalDeployerInfo(id); ok {
			helper = info.Helper
		}
	case pluginKindNotify:
		if info, ok := notifyplugin.ExternalChannelInfo(id); ok {
			helper = info.Helper
		}
	}
	if helper != "" && filepath.Clean(helper) != filepath.Clean(target) {
		return fmt.Errorf("插件 %s/%s 已由 %s 提供，请先卸载旧插件或完成迁移", kind, id, filepath.Base(helper))
	}
	return nil
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
	if err := s.requireSignedRepoIndex(); err != nil {
		return model.PluginInstallation{}, nil, err
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
	if err := s.pluginIDConflict(kind, plugin.ID, target); err != nil {
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
	dest, backup, err := plugininstall.Install(dir, filename, binaryPath)
	if err != nil {
		return model.PluginInstallation{}, nil, err
	}
	oldRecord, hadOld, _ := s.Store.GetPluginInstallation(kind, plugin.ID)
	record := model.PluginInstallation{
		Kind: kind, ID: plugin.ID, Version: plugin.Version, Filename: filename,
		SHA256: inspect.Digest, AssetSHA256: assetDigest, Source: "repo", RepoURL: s.repoURL(),
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
		// 已停用的文件不参与安装清理探测，避免为了复用旧条目而执行被隔离的 helper。
		if !s.pluginFileEnabled(kind, name) {
			continue
		}
		inspect, err := s.inspectPluginBinary(kind, filepath.Join(dir, name))
		if err != nil {
			continue
		}
		infos = append(infos, helperInfo{name: name, ids: inspect.IDs})
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
	index, err := s.loadRepoIndex(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	plugin, found := s.findRepoPlugin(index, kind, id, strings.TrimSpace(body.Version))
	if !found {
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
	record, scanErrors, err := s.installRepoPlugin(kind, plugin, asset)
	if err != nil {
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
	index, err := s.loadRepoIndex(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
	records, err := s.Store.ListPluginInstallations()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	id := strings.ToLower(strings.TrimSpace(body.ID))
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
		plugin, found := s.findRepoPlugin(index, record.Kind, record.ID, "")
		if !found || pluginrepo.CompareVersion(plugin.Version, record.Version) <= 0 {
			continue
		}
		asset, okAsset := pluginrepo.SelectAsset(plugin, runtime.GOOS, runtime.GOARCH)
		if !okAsset || !pluginrepo.Compatible(plugin, Version) {
			results = append(results, gin.H{"kind": record.Kind, "id": record.ID, "error": "当前平台或宿主版本不兼容"})
			continue
		}
		_, scanErrors, err := s.installRepoPlugin(record.Kind, plugin, asset)
		item := gin.H{"kind": record.Kind, "id": record.ID, "from": record.Version, "to": plugin.Version, "errors": scanErrors}
		if err != nil {
			item["error"] = err.Error()
		}
		results = append(results, item)
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
	index, err := s.loadRepoIndex(false)
	if err != nil {
		failErr(c, http.StatusBadGateway, err)
		return
	}
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
			if _, _, err := s.installRepoPlugin(kind, item.plugin, item.asset); err != nil {
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
		if _, _, err := s.installRepoPlugin(kind, plugin, asset); err != nil {
			rollback()
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
