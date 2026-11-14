package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/deployplugin"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

// dnsPluginEnabledSetting 保存被管理员启用的插件服务商 ID（JSON 数组）。
// 使用“启用白名单”：迁移前内置的服务商默认启用，其余插件服务商默认停用，
// 必须显式启用后才会出现在 DNS 服务商列表中。
const dnsPluginEnabledSetting = "plugins.dns.enabled_providers"

// dnsPluginLegacyDisabledSetting 是旧版“停用列表”键，写入新白名单时顺手清理。
const dnsPluginLegacyDisabledSetting = "plugins.dns.disabled_providers"

// deployPluginEnabledSetting 保存被管理员启用的部署目标 ID（JSON 数组）。
// 迁移前内置的部署目标默认启用，其余插件目标需要显式启用。
const deployPluginEnabledSetting = "plugins.deploy.enabled_deployers"

const (
	pluginKindDNS    = "dns"
	pluginKindDeploy = "deploy"
	// maxPluginUploadSize 是单个 helper 文件大小上限，与备份包上限保持一致。
	maxPluginUploadSize int64 = 256 << 20
)

// pluginFileNamePattern 限制上传后落盘的文件名，避免路径穿越与不可控字符。
var pluginFileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// uploadedPlugin 汇总上传 helper 的清单信息，用于接口响应与操作日志。
type uploadedPlugin struct {
	name    string
	version string
	digest  string
	items   []string
}

// DiscoverPlugins 按当前配置重新扫描外部 DNS 插件目录，并记录最近一次结果。
func (s *Server) DiscoverPlugins() dnsplugin.DiscoverResult {
	cfg := dnsplugin.DiscoverConfig{
		MemoryLimitMB: s.PluginMemoryLimitMB,
		Timeout:       s.PluginTimeout,
		MaxOutputKB:   s.PluginMaxOutputKB,
	}
	if s.PluginsEnabled {
		cfg.Dir = s.PluginDir
		cfg.EnabledProviders = s.dnsPluginEnabledProviders()
	}
	result := dnsplugin.Discover(cfg)
	s.pluginMu.Lock()
	s.pluginResult = result
	s.pluginMu.Unlock()
	return result
}

// DiscoverDeployPlugins 按当前配置重新扫描外部证书部署插件目录。
func (s *Server) DiscoverDeployPlugins() deployplugin.DiscoverResult {
	cfg := deployplugin.DiscoverConfig{
		MemoryLimitMB: s.DeployPluginMemoryLimitMB,
		Timeout:       s.DeployPluginTimeout,
		MaxOutputKB:   s.DeployPluginMaxOutputKB,
		KnownIDs:      service.BuiltinDeployTypeIDs(),
	}
	if s.DeployPluginsEnabled {
		cfg.Dir = s.DeployPluginDir
		cfg.EnabledDeployers = s.deployPluginEnabledDeployers()
	}
	result := deployplugin.Discover(cfg)
	s.deployPluginMu.Lock()
	s.deployPluginResult = result
	s.deployPluginMu.Unlock()
	return result
}

// listPlugins 返回外部插件状态与目标清单，供设置页展示与排障。
func (s *Server) listPlugins(c *gin.Context) {
	s.pluginMu.Lock()
	dnsResult := s.pluginResult
	s.pluginMu.Unlock()
	s.deployPluginMu.Lock()
	deployResult := s.deployPluginResult
	s.deployPluginMu.Unlock()

	ok(c, gin.H{
		"enabled":   s.PluginsEnabled,
		"dir":       s.PluginDir,
		"helpers":   dnsplugin.ExternalHelpers(),
		"providers": dnsplugin.ExternalProviders(),
		"errors":    dnsResult.Errors,
		"skipped":   dnsResult.Skipped,
		"limits": gin.H{
			"memory_limit_mb": s.PluginMemoryLimitMB,
			"timeout_seconds": int(s.PluginTimeout.Seconds()),
			"max_output_kb":   s.PluginMaxOutputKB,
		},
		"deploy": gin.H{
			"enabled":   s.DeployPluginsEnabled,
			"dir":       s.DeployPluginDir,
			"helpers":   deployplugin.ExternalDeployHelpers(),
			"deployers": visibleExternalDeployers(),
			"errors":    deployResult.Errors,
			"skipped":   deployResult.Skipped,
			"disabled":  deployResult.Disabled,
			"limits": gin.H{
				"memory_limit_mb": s.DeployPluginMemoryLimitMB,
				"timeout_seconds": int(s.DeployPluginTimeout.Seconds()),
				"max_output_kb":   s.DeployPluginMaxOutputKB,
			},
		},
	})
}

// visibleExternalDeployers 返回管理界面需要展示的部署目标。
// 合并后的规范目标存在时隐藏旧 ID，避免「插件」页出现重复条目。
func visibleExternalDeployers() []deployplugin.ExternalDeployer {
	all := deployplugin.ExternalDeployers()
	enabled := make(map[string]bool, len(all))
	for _, deployer := range all {
		enabled[deployer.ID] = deployer.Enabled
	}
	out := make([]deployplugin.ExternalDeployer, 0, len(all))
	for _, deployer := range all {
		if canonical, ok := service.DeprecatedDeployTypeCanonical[deployer.ID]; ok && enabled[canonical] {
			continue
		}
		out = append(out, deployer)
	}
	return out
}

// rescanPlugins 手动重扫 DNS 与部署插件目录，避免新增插件后必须重启服务。
// 重扫本身不修改配置，无需二次验证。
func (s *Server) rescanPlugins(c *gin.Context) {
	if !s.PluginsEnabled && !s.DeployPluginsEnabled {
		failKey(c, http.StatusBadRequest, "api.error.plugins_disabled")
		return
	}
	dnsResult := s.DiscoverPlugins()
	deployResult := s.DiscoverDeployPlugins()
	if s.PluginsEnabled {
		s.Store.AddLogKey("settings", 0, "info", "log.settings.plugins_rescanned", map[string]any{
			"providers": dnsResult.Providers,
			"helpers":   dnsResult.Helpers,
			"errors":    len(dnsResult.Errors),
		})
	}
	if s.DeployPluginsEnabled {
		s.Store.AddLogKey("settings", 0, "info", "log.settings.deploy_plugins_rescanned", map[string]any{
			"deployers": deployResult.Deployers,
			"helpers":   deployResult.Helpers,
			"errors":    len(deployResult.Errors),
		})
	}
	ok(c, gin.H{
		"providers": dnsResult.Providers,
		"helpers":   dnsResult.Helpers,
		"errors":    dnsResult.Errors,
		"deploy":    deployResult,
	})
}

// uploadPlugin 接收一个 helper 可执行文件，校验清单后写入插件目录并立即重扫。
// 上传文件会以服务账号执行，因此始终要求二次验证，且不提供关闭开关。
func (s *Server) uploadPlugin(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionPluginUpload) {
		return
	}
	// 同一时间只处理一个上传，避免同名文件互相覆盖或重扫结果交错。
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()

	// bodyLimit 默认只允许 2 MB，这里放宽到插件上传上限。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPluginUploadSize+8<<20)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			failKey(c, http.StatusRequestEntityTooLarge, "api.error.plugin_file_too_large")
			return
		}
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	kind := strings.ToLower(strings.TrimSpace(c.Request.FormValue("kind")))
	var dir string
	switch kind {
	case pluginKindDNS:
		if !s.PluginsEnabled {
			failKey(c, http.StatusBadRequest, "api.error.plugins_disabled")
			return
		}
		dir = strings.TrimSpace(s.PluginDir)
	case pluginKindDeploy:
		if !s.DeployPluginsEnabled {
			failKey(c, http.StatusBadRequest, "api.error.deploy_plugins_disabled")
			return
		}
		dir = strings.TrimSpace(s.DeployPluginDir)
	default:
		failKey(c, http.StatusBadRequest, "api.error.plugin_kind_invalid")
		return
	}
	if dir == "" {
		failKey(c, http.StatusBadRequest, "api.error.plugin_dir_unavailable")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.plugin_file_required")
		return
	}
	if fileHeader.Size <= 0 {
		failKey(c, http.StatusBadRequest, "api.error.plugin_file_required")
		return
	}
	if fileHeader.Size > maxPluginUploadSize {
		failKey(c, http.StatusRequestEntityTooLarge, "api.error.plugin_file_too_large")
		return
	}

	// name 可选：浏览器上传的文件名可能包含空格或中文，允许管理员另存为合法文件名。
	rawName := strings.TrimSpace(c.Request.FormValue("name"))
	if rawName == "" {
		rawName = fileHeader.Filename
	}
	name := filepath.Base(strings.ReplaceAll(rawName, "\\", "/"))
	if !pluginFileNamePattern.MatchString(name) {
		failKey(c, http.StatusBadRequest, "api.error.plugin_name_invalid")
		return
	}
	overwrite := strings.EqualFold(strings.TrimSpace(c.Request.FormValue("overwrite")), "true")

	dest := filepath.Join(dir, name)
	exists := false
	if info, statErr := os.Lstat(dest); statErr == nil {
		exists = true
		if !overwrite {
			failKey(c, http.StatusConflict, "api.error.plugin_exists")
			return
		}
		if !info.Mode().IsRegular() {
			// 目标不是普通文件（目录、符号链接等），拒绝覆盖以免写出插件目录。
			failKey(c, http.StatusBadRequest, "api.error.plugin_path_invalid")
			return
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		failErr(c, http.StatusInternalServerError, statErr)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.plugin_file_required")
		return
	}
	defer src.Close()

	// 先写入以点开头的临时文件，Discover 会跳过点文件，避免扫描到半成品。
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	written, err := io.Copy(tmp, io.LimitReader(src, maxPluginUploadSize+1))
	if err != nil {
		_ = tmp.Close()
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if written > maxPluginUploadSize {
		_ = tmp.Close()
		failKey(c, http.StatusRequestEntityTooLarge, "api.error.plugin_file_too_large")
		return
	}
	if written == 0 {
		_ = tmp.Close()
		failKey(c, http.StatusBadRequest, "api.error.plugin_file_required")
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if err := tmp.Close(); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	// 在落盘到最终位置前先探测 manifest，非法二进制不会污染插件目录。
	info, err := s.inspectUploadedPlugin(kind, tmpName)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.plugin_manifest_invalid",
			"error", service.SanitizeErrorText(err.Error()))
		return
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	// 覆盖时先把旧文件改名备份，rename 失败则恢复，避免上传失败导致插件丢失。
	backup := ""
	if exists {
		backup = filepath.Join(dir, fmt.Sprintf(".replaced-%d-%s", time.Now().UnixNano(), name))
		if err := os.Rename(dest, backup); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
		defer func() {
			if backup != "" {
				_ = os.Remove(backup)
			}
		}()
	}
	if err := os.Rename(tmpName, dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
			backup = ""
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	removeTemp = false
	if backup != "" {
		_ = os.Remove(backup)
		backup = ""
	}

	// 安装完成后立即重扫，让新服务商/部署目标无需重启即可使用。
	var scanErrors []string
	var scanKey string
	switch kind {
	case pluginKindDNS:
		result := s.DiscoverPlugins()
		scanErrors = result.Errors
		scanKey = "log.settings.plugin_uploaded"
	case pluginKindDeploy:
		result := s.DiscoverDeployPlugins()
		scanErrors = result.Errors
		scanKey = "log.settings.deploy_plugin_uploaded"
	}
	s.Store.AddLogKey("settings", 0, "warning", scanKey, map[string]any{
		"kind":    kind,
		"file":    name,
		"name":    info.name,
		"version": info.version,
		"items":   len(info.items),
	})
	ok(c, gin.H{
		"kind":    kind,
		"file":    name,
		"name":    info.name,
		"version": info.version,
		"sha256":  info.digest,
		"items":   info.items,
		"errors":  scanErrors,
		"message": i18n.T(locale(c), "api.ok.plugin_uploaded", map[string]any{"file": name}),
	})
}

// inspectUploadedPlugin 按插件类型探测临时文件，返回清单信息但不注册到运行时。
func (s *Server) inspectUploadedPlugin(kind, path string) (uploadedPlugin, error) {
	switch kind {
	case pluginKindDNS:
		manifest, digest, err := dnsplugin.InspectHelper(path, dnsplugin.DiscoverConfig{
			MemoryLimitMB: s.PluginMemoryLimitMB,
			Timeout:       s.PluginTimeout,
			MaxOutputKB:   s.PluginMaxOutputKB,
		})
		if err != nil {
			return uploadedPlugin{}, err
		}
		info := uploadedPlugin{name: manifest.Name, version: manifest.Version, digest: digest}
		for _, provider := range manifest.Providers {
			info.items = append(info.items, provider.ID)
		}
		return info, nil
	case pluginKindDeploy:
		manifest, digest, err := deployplugin.InspectHelper(path, deployplugin.DiscoverConfig{
			MemoryLimitMB: s.DeployPluginMemoryLimitMB,
			Timeout:       s.DeployPluginTimeout,
			MaxOutputKB:   s.DeployPluginMaxOutputKB,
			KnownIDs:      service.BuiltinDeployTypeIDs(),
		})
		if err != nil {
			return uploadedPlugin{}, err
		}
		info := uploadedPlugin{name: manifest.Name, version: manifest.Version, digest: digest}
		for _, deployer := range manifest.Deployers {
			info.items = append(info.items, deployer.ID)
		}
		return info, nil
	default:
		return uploadedPlugin{}, fmt.Errorf("不支持的插件类型: %s", kind)
	}
}

// setPluginProviderEnabled 启用或停用单个插件服务商。
// 内置服务商不属于插件，始终可用，不能通过该接口修改。
func (s *Server) setPluginProviderEnabled(c *gin.Context) {
	if !s.PluginsEnabled {
		failKey(c, http.StatusBadRequest, "api.error.plugins_disabled")
		return
	}
	id := strings.ToLower(strings.TrimSpace(c.Param("id")))
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if _, found := dnsplugin.ExternalProviderInfo(id); !found {
		// 只有编译进当前主程序的内置实现才算“内置”；
		// 未编译的服务商必须先由插件提供，不能通过停用列表操作。
		if plugin, registered := dnsplugin.Lookup(id); registered && plugin.Source() == dnsplugin.SourceBuiltin {
			failKey(c, http.StatusBadRequest, "api.error.plugin_provider_builtin")
			return
		}
		failKey(c, http.StatusNotFound, "api.error.plugin_provider_not_found")
		return
	}
	updated, _, err := s.updateDNSPluginProviders([]string{id}, *body.Enabled)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if len(updated) == 0 {
		failKey(c, http.StatusNotFound, "api.error.plugin_provider_not_found")
		return
	}
	result := s.DiscoverPlugins()
	action := "log.settings.plugin_provider_disabled"
	if *body.Enabled {
		action = "log.settings.plugin_provider_enabled"
	}
	s.Store.AddLogKey("settings", 0, "info", action, map[string]any{"provider": id})
	ok(c, gin.H{
		"id":      id,
		"enabled": *body.Enabled,
		"errors":  result.Errors,
	})
}

// setPluginProvidersEnabled 批量启用或停用插件服务商。
// ids 为空时作用于当前发现的所有插件服务商。
func (s *Server) setPluginProvidersEnabled(c *gin.Context) {
	if !s.PluginsEnabled {
		failKey(c, http.StatusBadRequest, "api.error.plugins_disabled")
		return
	}
	var body struct {
		Enabled *bool    `json:"enabled"`
		IDs     []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ids := body.IDs
	if len(ids) == 0 {
		for _, provider := range dnsplugin.ExternalProviders() {
			ids = append(ids, provider.ID)
		}
	}
	updated, skipped, err := s.updateDNSPluginProviders(ids, *body.Enabled)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	result := dnsplugin.DiscoverResult{}
	if len(updated) > 0 {
		result = s.DiscoverPlugins()
		s.Store.AddLogKey("settings", 0, "info", "log.settings.plugin_providers_updated", map[string]any{
			"count": len(updated),
		})
	}
	ok(c, gin.H{
		"updated": len(updated),
		"ids":     updated,
		"skipped": skipped,
		"enabled": *body.Enabled,
		"errors":  result.Errors,
	})
}

// updateDNSPluginProviders 写入启用白名单并返回实际更新的服务商。
// 找不到对应插件元数据的 ID 会被跳过，避免白名单无限膨胀。
func (s *Server) updateDNSPluginProviders(ids []string, enabled bool) ([]string, []string, error) {
	// 串行化读改写，避免两个管理员同时操作时丢失彼此的启用/停用结果。
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()

	set := s.dnsPluginEnabledSet()
	updated := make([]string, 0, len(ids))
	skipped := make([]string, 0)
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if _, found := dnsplugin.ExternalProviderInfo(id); !found {
			skipped = append(skipped, id)
			continue
		}
		if enabled {
			set[id] = true
		} else {
			delete(set, id)
		}
		updated = append(updated, id)
	}
	if len(updated) == 0 {
		return updated, skipped, nil
	}
	return updated, skipped, s.saveDNSPluginEnabledSet(set)
}

// defaultDNSPluginEnabledSet 返回默认启用的插件服务商集合。
// 迁移前内置的 35 家服务商改由 helper 提供后仍默认启用；其余服务商默认停用。
func defaultDNSPluginEnabledSet() map[string]bool {
	ids := service.BuiltinDNSProviderIDs()
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[strings.ToLower(strings.TrimSpace(id))] = true
	}
	return out
}

func isDefaultDNSPluginProvider(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, builtin := range service.BuiltinDNSProviderIDs() {
		if strings.ToLower(strings.TrimSpace(builtin)) == id {
			return true
		}
	}
	return false
}

func (s *Server) dnsPluginEnabledSet() map[string]bool {
	raw, err := s.Store.GetSetting(dnsPluginEnabledSetting)
	if err != nil || strings.TrimSpace(raw) == "" {
		// 尚未保存过启用状态：使用默认白名单，避免升级后内置服务商全部消失。
		return defaultDNSPluginEnabledSet()
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return defaultDNSPluginEnabledSet()
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			out[id] = true
		}
	}
	return out
}

func (s *Server) dnsPluginEnabledProviders() []string {
	set := s.dnsPluginEnabledSet()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) saveDNSPluginEnabledSet(set map[string]bool) error {
	ids := make([]string, 0, len(set))
	for id, enabled := range set {
		if !enabled {
			continue
		}
		// 顺手清理已卸载 helper 留下的陈旧 ID，但保留默认启用的内置服务商：
		// helper 尚未安装时也要记住它们，安装后无需重新启用。
		if _, found := dnsplugin.ExternalProviderInfo(id); found || isDefaultDNSPluginProvider(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := s.Store.SetSetting(dnsPluginEnabledSetting, string(data)); err != nil {
		return err
	}
	// 旧键不再参与判断，删除以免后续排查时产生误解。
	_ = s.Store.DeleteSetting(dnsPluginLegacyDisabledSetting)
	return nil
}

// setDeployPluginDeployerEnabled 启用或停用单个插件部署目标。
// 本地文件与 SSH 属于内置目标，始终可用，不能通过该接口修改。
func (s *Server) setDeployPluginDeployerEnabled(c *gin.Context) {
	if !s.DeployPluginsEnabled {
		failKey(c, http.StatusBadRequest, "api.error.deploy_plugins_disabled")
		return
	}
	id := strings.ToLower(strings.TrimSpace(c.Param("id")))
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if service.BuiltinDeployTypeIDs()[id] {
		// 本地文件与 SSH 不属于插件，不能通过该接口停用。
		failKey(c, http.StatusBadRequest, "api.error.plugin_deployer_builtin")
		return
	}
	if _, found := deployplugin.ExternalDeployerInfo(id); !found {
		failKey(c, http.StatusNotFound, "api.error.plugin_deployer_not_found")
		return
	}
	updated, _, err := s.updateDeployPluginDeployers([]string{id}, *body.Enabled)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if len(updated) == 0 {
		failKey(c, http.StatusNotFound, "api.error.plugin_deployer_not_found")
		return
	}
	result := s.DiscoverDeployPlugins()
	action := "log.settings.deploy_plugin_deployer_disabled"
	if *body.Enabled {
		action = "log.settings.deploy_plugin_deployer_enabled"
	}
	s.Store.AddLogKey("settings", 0, "info", action, map[string]any{"deployer": id})
	ok(c, gin.H{
		"id":      id,
		"enabled": *body.Enabled,
		"errors":  result.Errors,
	})
}

// setDeployPluginDeployersEnabled 批量启用或停用插件部署目标。
// ids 为空时作用于当前发现的所有插件部署目标。
func (s *Server) setDeployPluginDeployersEnabled(c *gin.Context) {
	if !s.DeployPluginsEnabled {
		failKey(c, http.StatusBadRequest, "api.error.deploy_plugins_disabled")
		return
	}
	var body struct {
		Enabled *bool    `json:"enabled"`
		IDs     []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ids := body.IDs
	if len(ids) == 0 {
		for _, deployer := range visibleExternalDeployers() {
			ids = append(ids, deployer.ID)
		}
	}
	updated, skipped, err := s.updateDeployPluginDeployers(ids, *body.Enabled)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	result := deployplugin.DiscoverResult{}
	if len(updated) > 0 {
		result = s.DiscoverDeployPlugins()
		s.Store.AddLogKey("settings", 0, "info", "log.settings.deploy_plugin_deployers_updated", map[string]any{
			"count": len(updated),
		})
	}
	ok(c, gin.H{
		"updated": len(updated),
		"ids":     updated,
		"skipped": skipped,
		"enabled": *body.Enabled,
		"errors":  result.Errors,
	})
}

// updateDeployPluginDeployers 写入启用白名单并返回实际更新的部署目标。
// 找不到对应插件元数据的 ID 会被跳过，避免白名单无限膨胀。
func (s *Server) updateDeployPluginDeployers(ids []string, enabled bool) ([]string, []string, error) {
	// 串行化读改写，避免两个管理员同时操作时丢失彼此的启用/停用结果。
	s.deployPluginMu.Lock()
	defer s.deployPluginMu.Unlock()

	set := s.deployPluginEnabledSet()
	updated := make([]string, 0, len(ids))
	skipped := make([]string, 0)
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if _, found := deployplugin.ExternalDeployerInfo(id); !found {
			skipped = append(skipped, id)
			continue
		}
		if enabled {
			set[id] = true
		} else {
			delete(set, id)
			// 停用合并目标时一并移除旧 ID，避免白名单补全逻辑把它重新启用。
			for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
				if canonical == id {
					delete(set, legacy)
				}
			}
		}
		updated = append(updated, id)
	}
	if len(updated) == 0 {
		return updated, skipped, nil
	}
	return updated, skipped, s.saveDeployPluginEnabledSet(set)
}

// defaultDeployPluginEnabledSet 返回默认启用的插件部署目标集合。
// 迁移前内置的部署目标改由 helper 提供后仍默认启用；其余插件目标默认停用。
func defaultDeployPluginEnabledSet() map[string]bool {
	out := make(map[string]bool, len(service.PluginDeployTypes))
	for _, def := range service.PluginDeployTypes {
		out[strings.ToLower(strings.TrimSpace(def.Value))] = true
	}
	return out
}

func isDefaultDeployPluginDeployer(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, def := range service.PluginDeployTypes {
		if strings.ToLower(strings.TrimSpace(def.Value)) == id {
			return true
		}
	}
	return false
}

func (s *Server) deployPluginEnabledSet() map[string]bool {
	raw, err := s.Store.GetSetting(deployPluginEnabledSetting)
	if err != nil || strings.TrimSpace(raw) == "" {
		// 尚未保存过启用状态：使用默认白名单，避免升级后内置部署目标全部消失。
		return defaultDeployPluginEnabledSet()
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return defaultDeployPluginEnabledSet()
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			out[id] = true
		}
	}
	// 已合并目标：旧版本白名单只有 qiniu_cdn/qiniu_kodo 等旧 ID，
	// 升级后需要自动启用新的规范 ID，否则合并目标会被判定为未启用。
	promoteMergedDeployTypes(out)
	return out
}

// promoteMergedDeployTypes 在白名单包含旧目标时补充其合并后的规范目标。
func promoteMergedDeployTypes(set map[string]bool) {
	for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
		if set[legacy] && !set[canonical] {
			set[canonical] = true
		}
	}
}

func (s *Server) deployPluginEnabledDeployers() []string {
	set := s.deployPluginEnabledSet()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) saveDeployPluginEnabledSet(set map[string]bool) error {
	ids := make([]string, 0, len(set))
	for id, enabled := range set {
		if !enabled {
			continue
		}
		// 顺手清理已卸载 helper 留下的陈旧 ID，但保留默认启用的部署目标：
		// helper 尚未安装时也要记住它们，安装后无需重新启用。
		if _, found := deployplugin.ExternalDeployerInfo(id); found || isDefaultDeployPluginDeployer(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(deployPluginEnabledSetting, string(data))
}
