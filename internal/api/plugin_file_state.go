package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
	"lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/dns"
	"lactssl/internal/plugin/install"
	"lactssl/internal/plugin/notify"
	"lactssl/internal/service"
)

// 文件级（二级）启停设置：保存被管理员停用的 helper 文件名。
// 使用“停用集合”而不是启用集合，保证升级同名 helper 不会丢失启用状态；
// 仓库安装与手动上传的新文件由调用方在写入目录后显式停用，实现“下载后默认不启用”。
const (
	dnsDisabledHelpersSetting    = "plugins.dns.disabled_helpers"
	deployDisabledHelpersSetting = "plugins.deploy.disabled_helpers"
	notifyDisabledHelpersSetting = "plugins.notify.disabled_helpers"
)

func disabledHelpersSetting(kind string) string {
	switch kind {
	case pluginKindDNS:
		return dnsDisabledHelpersSetting
	case pluginKindDeploy:
		return deployDisabledHelpersSetting
	case pluginKindNotify:
		return notifyDisabledHelpersSetting
	default:
		return ""
	}
}

func normalizePluginFile(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// helperDisabledSet 返回某类插件被停用的 helper 文件名集合（键为小写）。
func (s *Server) helperDisabledSet(kind string) map[string]bool {
	out := make(map[string]bool)
	key := disabledHelpersSetting(kind)
	if key == "" || s.Store == nil {
		return out
	}
	raw, err := s.Store.GetSetting(key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return out
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return out
	}
	for _, name := range names {
		if name = normalizePluginFile(name); name != "" {
			out[name] = true
		}
	}
	return out
}

// disabledHelpers 返回停用的 helper 文件名，供 Discover 跳过探测。
func (s *Server) disabledHelpers(kind string) []string {
	set := s.helperDisabledSet(kind)
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *Server) saveHelperDisabledSet(kind string, set map[string]bool) error {
	key := disabledHelpersSetting(kind)
	if key == "" {
		return nil
	}
	names := make([]string, 0, len(set))
	for name, disabled := range set {
		if disabled && strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	data, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(key, string(data))
}

// pluginFileEnabled 判断 helper 文件是否参与发现与加载。
func (s *Server) pluginFileEnabled(kind, filename string) bool {
	return !s.helperDisabledSet(kind)[normalizePluginFile(filename)]
}

// updatePluginFileEnabled 写入文件级启用状态。
func (s *Server) updatePluginFileEnabled(kind, filename string, enabled bool) error {
	name := strings.TrimSpace(filename)
	if name == "" {
		return nil
	}
	set := s.helperDisabledSet(kind)
	if enabled {
		delete(set, normalizePluginFile(name))
	} else {
		set[normalizePluginFile(name)] = true
	}
	return s.saveHelperDisabledSet(kind, set)
}

// setPluginFileEnabled 处理插件文件级启用/停用。
// 启用会重新执行第三方 helper；停用只影响加载。
func (s *Server) setPluginFileEnabled(c *gin.Context) {
	kind := strings.ToLower(strings.TrimSpace(c.Param("kind")))
	dir, kindEnabled := s.pluginKindDir(kind)
	if strings.TrimSpace(dir) == "" || !kindEnabled {
		switch kind {
		case pluginKindDNS:
			failKey(c, http.StatusBadRequest, "api.error.plugins_disabled")
		case pluginKindDeploy:
			failKey(c, http.StatusBadRequest, "api.error.deploy_plugins_disabled")
		case pluginKindNotify:
			failKey(c, http.StatusBadRequest, "api.error.notify_plugins_disabled")
		default:
			failKey(c, http.StatusBadRequest, "api.error.plugin_kind_invalid")
		}
		return
	}
	file := strings.TrimSpace(c.Param("file"))
	path, err := plugininstall.SafePath(dir, file)
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.plugin_name_invalid")
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			failKey(c, http.StatusNotFound, "api.error.plugin_file_not_found")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !info.Mode().IsRegular() {
		failKey(c, http.StatusBadRequest, "api.error.plugin_path_invalid")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	// 与上传/安装/删除共用锁，避免文件状态与目录扫描交错。
	s.pluginUploadMu.Lock()
	defer s.pluginUploadMu.Unlock()
	if err := s.updatePluginFileEnabled(kind, file, *body.Enabled); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	scanErrors := s.rescanPluginKind(kind)
	action := "log.settings.plugin_file_disabled"
	if *body.Enabled {
		action = "log.settings.plugin_file_enabled"
	}
	s.Store.AddLogKey("settings", 0, "info", action, map[string]any{
		"kind": kind,
		"file": file,
	})
	ok(c, gin.H{
		"kind":    kind,
		"file":    file,
		"enabled": *body.Enabled,
		"errors":  scanErrors,
	})
}

// syncPluginFileMeta 在发现成功后缓存每个 helper 的条目摘要。
// 文件停用后不再探测 manifest，插件页需要这份缓存才能继续展示条目并提供清理依据。
func (s *Server) syncPluginFileMeta(kind string) {
	if s.Store == nil {
		return
	}
	type discovered struct {
		version string
		entries []model.PluginFileEntry
	}
	byFile := make(map[string]discovered)
	for _, item := range discoverItems(kind) {
		name := filepath.Base(item.helper)
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		current := byFile[name]
		if current.version == "" {
			current.version = item.version
		}
		current.entries = append(current.entries, model.PluginFileEntry{ID: item.id, Name: item.name})
		byFile[name] = current
	}
	existing := map[string]model.PluginFileMeta{}
	if list, err := s.Store.ListPluginFileMeta(kind); err == nil {
		for _, meta := range list {
			existing[meta.Kind+"\x00"+meta.Filename] = meta
		}
	}
	for filename, info := range byFile {
		old, found := existing[kind+"\x00"+filename]
		entries := info.entries
		// 发现结果会省略被内置实现或其他文件占用的 ID，同版本时与缓存取并集，
		// 避免一次重扫把完整清单缩小。
		if found && (info.version == "" || info.version == old.Version) {
			entries = mergePluginFileEntries(old.Entries, entries)
		}
		sha := ""
		if found {
			sha = old.SHA256
		}
		name := ""
		if found {
			name = old.Name
		}
		if found && old.Version == info.version && samePluginFileEntries(old.Entries, entries) && old.SHA256 == sha {
			continue
		}
		meta := model.PluginFileMeta{
			Kind:     kind,
			Filename: filename,
			Name:     name,
			Version:  info.version,
			Entries:  entries,
			SHA256:   sha,
		}
		if meta.Version == "" && found {
			meta.Version = old.Version
		}
		_ = s.Store.UpsertPluginFileMeta(meta)
	}
}

type discoveredPluginItem struct {
	id      string
	name    string
	helper  string
	version string
}

// discoverItems 把三类插件的发现结果统一成条目列表。
func discoverItems(kind string) []discoveredPluginItem {
	switch kind {
	case pluginKindDNS:
		all := dnsplugin.ExternalProviders()
		out := make([]discoveredPluginItem, 0, len(all))
		for _, item := range all {
			out = append(out, discoveredPluginItem{id: item.ID, name: item.Name, helper: item.Helper, version: item.Version})
		}
		return out
	case pluginKindDeploy:
		all := deployplugin.ExternalDeployers()
		out := make([]discoveredPluginItem, 0, len(all))
		for _, item := range all {
			out = append(out, discoveredPluginItem{id: item.ID, name: item.Name, helper: item.Helper, version: item.Version})
		}
		return out
	case pluginKindNotify:
		all := notifyplugin.ExternalChannels()
		out := make([]discoveredPluginItem, 0, len(all))
		for _, item := range all {
			out = append(out, discoveredPluginItem{id: item.ID, name: item.Name, helper: item.Helper, version: item.Version})
		}
		return out
	default:
		return nil
	}
}

// upsertPluginFileMeta 在安装/上传后写入一份完整清单（发现结果可能因冲突而缺项）。
func (s *Server) upsertPluginFileMeta(kind, filename, name, version, digest string, ids []string) {
	if s.Store == nil || strings.TrimSpace(filename) == "" {
		return
	}
	entries := make([]model.PluginFileEntry, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			entries = append(entries, model.PluginFileEntry{ID: id})
		}
	}
	meta := model.PluginFileMeta{
		Kind:     kind,
		Filename: filename,
		Name:     name,
		Version:  version,
		Entries:  entries,
		SHA256:   digest,
	}
	// 保留同名旧文件已知的条目名称，避免升级后名称短暂丢失。
	if old, found, err := s.Store.GetPluginFileMeta(kind, filename); err == nil && found {
		if meta.Name == "" {
			meta.Name = old.Name
		}
		names := make(map[string]string, len(old.Entries))
		for _, entry := range old.Entries {
			names[entry.ID] = entry.Name
		}
		for i := range meta.Entries {
			if meta.Entries[i].Name == "" {
				meta.Entries[i].Name = names[meta.Entries[i].ID]
			}
		}
	}
	_ = s.Store.UpsertPluginFileMeta(meta)
}

// prunePluginEnabledWhitelist 按最新 manifest 缓存清理已消失的条目。
// 覆盖安装或升级后，旧版本独有的条目不应继续留在白名单里。
func (s *Server) prunePluginEnabledWhitelist(kind string) {
	switch kind {
	case pluginKindDNS:
		_ = s.saveDNSPluginEnabledSet(s.dnsPluginEnabledSet())
	case pluginKindDeploy:
		_ = s.saveDeployPluginEnabledSet(s.deployPluginEnabledSet())
	case pluginKindNotify:
		_ = s.saveNotifyPluginEnabledSet(s.notifyPluginEnabledSet())
	}
}

func mergePluginFileEntries(primary, extra []model.PluginFileEntry) []model.PluginFileEntry {
	out := make([]model.PluginFileEntry, 0, len(primary)+len(extra))
	index := make(map[string]int, len(primary)+len(extra))
	for _, entry := range primary {
		if entry.ID == "" {
			continue
		}
		if pos, found := index[entry.ID]; found {
			if out[pos].Name == "" {
				out[pos].Name = entry.Name
			}
			continue
		}
		index[entry.ID] = len(out)
		out = append(out, entry)
	}
	for _, entry := range extra {
		if entry.ID == "" {
			continue
		}
		if pos, found := index[entry.ID]; found {
			if out[pos].Name == "" {
				out[pos].Name = entry.Name
			}
			continue
		}
		index[entry.ID] = len(out)
		out = append(out, entry)
	}
	return out
}

func samePluginFileEntries(a, b []model.PluginFileEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

// pluginFileKnownItems 返回某类插件当前已知的全部条目 ID。
// 除发现结果外还包含缓存的条目，因此停用文件后保存其他白名单不会误删勾选。
func (s *Server) pluginFileKnownItems(kind string) map[string]bool {
	out := make(map[string]bool)
	for _, item := range discoverItems(kind) {
		out[normalizePluginItemID(kind, item.id)] = true
	}
	if s.Store != nil {
		if list, err := s.Store.ListPluginFileMeta(kind); err == nil {
			for _, meta := range list {
				for _, entry := range meta.Entries {
					out[normalizePluginItemID(kind, entry.ID)] = true
				}
			}
		}
	}
	return out
}

func normalizePluginItemID(kind, id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if kind == pluginKindDeploy {
		return canonicalPluginID(kind, id)
	}
	return id
}

// pluginFileItems 返回文件当前及历史上提供的条目 ID（发现结果 + 缓存）。
// 删除文件时用并集清理白名单，避免缓存与发现结果不一致导致残留。
func (s *Server) pluginFileItems(kind, helper string) ([]string, string) {
	clean := filepath.Clean(helper)
	seen := make(map[string]bool)
	ids := make([]string, 0)
	version := ""
	for _, item := range discoverItems(kind) {
		if item.helper == "" || filepath.Clean(item.helper) != clean {
			continue
		}
		if !seen[item.id] {
			seen[item.id] = true
			ids = append(ids, item.id)
		}
		if version == "" {
			version = item.version
		}
	}
	if s.Store != nil {
		if meta, found, err := s.Store.GetPluginFileMeta(kind, filepath.Base(clean)); err == nil && found {
			for _, entry := range meta.Entries {
				if entry.ID != "" && !seen[entry.ID] {
					seen[entry.ID] = true
					ids = append(ids, entry.ID)
				}
			}
			if version == "" {
				version = meta.Version
			}
		}
	}
	sort.Strings(ids)
	return ids, version
}

// dropPluginFileMeta 只清理文件的 manifest 缓存与停用状态，不动白名单。
// 用于回滚或迁移临时移除文件，保留条目勾选以便重新安装后恢复。
func (s *Server) dropPluginFileMeta(kind, filename string) {
	if s.Store != nil {
		_ = s.Store.DeletePluginFileMeta(kind, filename)
	}
	set := s.helperDisabledSet(kind)
	if set[normalizePluginFile(filename)] {
		delete(set, normalizePluginFile(filename))
		_ = s.saveHelperDisabledSet(kind, set)
	}
}

// removePluginFileData 清理文件级状态、manifest 缓存与白名单条目。
// 调用方负责先删除文件本身；这里只在内存与数据库中收尾。
func (s *Server) removePluginFileData(kind, filename string, items []string) {
	s.removePluginWhitelistEntries(kind, items)
	s.dropPluginFileMeta(kind, filename)
}

// shadowDNSProviders 为已停用文件构造条目视图。
// 文件不参与发现，但插件页仍需展示其条目、名称与版本。
func (s *Server) shadowDNSProviders() []dnsplugin.ExternalProvider {
	if s.Store == nil {
		return nil
	}
	disabled := s.helperDisabledSet(pluginKindDNS)
	if len(disabled) == 0 {
		return nil
	}
	dir, _ := s.pluginKindDir(pluginKindDNS)
	list, err := s.Store.ListPluginFileMeta(pluginKindDNS)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, item := range dnsplugin.ExternalProviders() {
		seen[strings.ToLower(item.ID)] = true
	}
	out := make([]dnsplugin.ExternalProvider, 0)
	for _, meta := range list {
		if !disabled[normalizePluginFile(meta.Filename)] {
			continue
		}
		for _, entry := range meta.Entries {
			id := strings.ToLower(strings.TrimSpace(entry.ID))
			if id == "" || seen[id] {
				continue
			}
			if _, ok := dnsplugin.BuiltinProviderInfo(id); ok {
				continue
			}
			if plugin, ok := dnsplugin.Lookup(id); ok && plugin.Source() == dnsplugin.SourceBuiltin {
				continue
			}
			seen[id] = true
			out = append(out, dnsplugin.ExternalProvider{
				ID:          entry.ID,
				Name:        entry.Name,
				Helper:      filepath.Join(dir, meta.Filename),
				Version:     meta.Version,
				Enabled:     false,
				FileEnabled: false,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// shadowDeployers 为已停用文件构造部署目标视图，按合并后的规范 ID 去重。
func (s *Server) shadowDeployers() []deployplugin.ExternalDeployer {
	if s.Store == nil {
		return nil
	}
	disabled := s.helperDisabledSet(pluginKindDeploy)
	if len(disabled) == 0 {
		return nil
	}
	dir, _ := s.pluginKindDir(pluginKindDeploy)
	list, err := s.Store.ListPluginFileMeta(pluginKindDeploy)
	if err != nil {
		return nil
	}
	builtin := service.BuiltinDeployTypeIDs()
	for id := range deployplugin.BuiltinDeployerIDs() {
		builtin[id] = true
	}
	seen := make(map[string]bool)
	for _, item := range visibleExternalDeployers() {
		seen[normalizePluginItemID(pluginKindDeploy, item.ID)] = true
	}
	out := make([]deployplugin.ExternalDeployer, 0)
	index := make(map[string]int)
	for _, meta := range list {
		if !disabled[normalizePluginFile(meta.Filename)] {
			continue
		}
		for _, entry := range meta.Entries {
			id := strings.ToLower(strings.TrimSpace(entry.ID))
			if id == "" {
				continue
			}
			canonical := normalizePluginItemID(pluginKindDeploy, id)
			if builtin[canonical] || seen[canonical] {
				continue
			}
			item := deployplugin.ExternalDeployer{
				ID:          entry.ID,
				Name:        entry.Name,
				Helper:      filepath.Join(dir, meta.Filename),
				Version:     meta.Version,
				Enabled:     false,
				FileEnabled: false,
			}
			if pos, found := index[canonical]; found {
				// 旧 ID 与规范 ID 同时存在时优先展示规范 ID。
				if id == canonical {
					out[pos] = item
				}
				continue
			}
			index[canonical] = len(out)
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// shadowNotifyChannels 为已停用文件构造通知渠道视图。
func (s *Server) shadowNotifyChannels() []notifyplugin.ExternalChannel {
	if s.Store == nil {
		return nil
	}
	disabled := s.helperDisabledSet(pluginKindNotify)
	if len(disabled) == 0 {
		return nil
	}
	dir, _ := s.pluginKindDir(pluginKindNotify)
	list, err := s.Store.ListPluginFileMeta(pluginKindNotify)
	if err != nil {
		return nil
	}
	builtin := service.BuiltinNotifyChannelIDs()
	seen := make(map[string]bool)
	for _, item := range notifyplugin.ExternalChannels() {
		seen[strings.ToLower(item.ID)] = true
	}
	out := make([]notifyplugin.ExternalChannel, 0)
	for _, meta := range list {
		if !disabled[normalizePluginFile(meta.Filename)] {
			continue
		}
		for _, entry := range meta.Entries {
			id := strings.ToLower(strings.TrimSpace(entry.ID))
			if id == "" || seen[id] || builtin[id] {
				continue
			}
			seen[id] = true
			out = append(out, notifyplugin.ExternalChannel{
				ID:          entry.ID,
				Name:        entry.Name,
				Helper:      filepath.Join(dir, meta.Filename),
				Version:     meta.Version,
				Enabled:     false,
				FileEnabled: false,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
