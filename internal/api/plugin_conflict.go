package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	deployplugin "lactssl/internal/plugin/deploy"
	dnsplugin "lactssl/internal/plugin/dns"
	notifyplugin "lactssl/internal/plugin/notify"
	"lactssl/internal/service"
)

// 插件冲突类型。ID 与别名都会占用运行时注册表，内置实现则无法通过卸载插件解决。
const (
	pluginConflictTypeID      = "id"
	pluginConflictTypeAlias   = "alias"
	pluginConflictTypeBuiltin = "builtin"
)

// pluginConflictUsage 表示冲突插件当前被多少业务对象引用。
// 卸载冲突插件前需要让管理员看清影响面。
type pluginConflictUsage struct {
	Accounts int `json:"accounts,omitempty"`
	Targets  int `json:"targets,omitempty"`
	Channels int `json:"channels,omitempty"`
}

// pluginConflictExisting 描述占用 ID/别名的现有 helper 文件。
type pluginConflictExisting struct {
	Kind      string              `json:"kind"`
	File      string              `json:"file"`
	PluginID  string              `json:"plugin_id,omitempty"`
	Version   string              `json:"version,omitempty"`
	Source    string              `json:"source"`
	Enabled   bool                `json:"enabled"`
	Covered   bool                `json:"covered"`
	Uncovered []string            `json:"uncovered,omitempty"`
	Usage     pluginConflictUsage `json:"usage"`
}

// pluginConflict 是返回给前端的一条冲突。
type pluginConflict struct {
	Kind     string                  `json:"kind"`
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Existing *pluginConflictExisting `json:"existing,omitempty"`
	Actions  []string                `json:"actions"`
}

// pluginConflictError 让安装流程把冲突以结构化数据返回，而不是只返回一段文本。
type pluginConflictError struct {
	PluginID   string
	PluginName string
	Conflicts  []pluginConflict
}

func (e *pluginConflictError) Error() string {
	name := strings.TrimSpace(e.PluginName)
	if name == "" {
		name = e.PluginID
	}
	return fmt.Sprintf("插件 %s 与已安装插件冲突，请先卸载冲突插件后重试", name)
}

// detectPluginConflicts 在写入插件目录前检查 incoming 的 ID 与别名是否已被
// 其他 helper 文件或内置实现占用。bundle 为 true 且旧文件被新包完全覆盖时，
// 沿用安装后的自动清理逻辑，不返回冲突。
func (s *Server) detectPluginConflicts(kind string, incomingIDs, incomingAliases []string, bundle bool, pluginID, pluginName string, ignoredFiles ...string) *pluginConflictError {
	if s == nil {
		return nil
	}
	incoming := make(map[string]bool, len(incomingIDs))
	for _, id := range incomingIDs {
		if id = normalizePluginItemID(kind, id); id != "" {
			incoming[id] = true
		}
	}
	incomingAlias := make(map[string]bool, len(incomingAliases))
	for _, alias := range incomingAliases {
		if alias = normalizePluginItemID(kind, alias); alias != "" {
			incomingAlias[alias] = true
		}
	}

	ignored := make(map[string]bool, len(ignoredFiles))
	for _, file := range ignoredFiles {
		if file = strings.TrimSpace(file); file != "" {
			ignored[file] = true
		}
	}

	aliasesByItem := s.pluginItemAliases(kind)
	conflicts := make([]pluginConflict, 0)
	seen := make(map[string]bool)

	addBuiltin := func(id string) {
		if !isBuiltinPluginID(kind, id) {
			return
		}
		key := kind + "\x00" + id + "\x00" + pluginConflictTypeBuiltin
		if seen[key] {
			return
		}
		seen[key] = true
		conflicts = append(conflicts, pluginConflict{
			Kind:    kind,
			ID:      id,
			Type:    pluginConflictTypeBuiltin,
			Actions: []string{"cancel"},
		})
	}

	for id := range incoming {
		addBuiltin(id)
	}
	for alias := range incomingAlias {
		addBuiltin(alias)
	}

	type fileInventory struct {
		view    pluginFileView
		ids     map[string]bool
		aliases map[string]string
	}
	inventories := make([]fileInventory, 0)
	for _, file := range s.pluginFiles(kind) {
		if ignored[file.File] {
			continue
		}
		ids := make(map[string]bool, len(file.Items))
		for _, id := range file.Items {
			if id = normalizePluginItemID(kind, id); id != "" {
				ids[id] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		fileAliases := make(map[string]string, len(ids))
		for id := range ids {
			for _, alias := range aliasesByItem[id] {
				if alias = normalizePluginItemID(kind, alias); alias != "" {
					fileAliases[alias] = id
				}
			}
		}
		inventories = append(inventories, fileInventory{view: file, ids: ids, aliases: fileAliases})
	}
	// providedByOthers 判断某个旧文件的条目是否已被新包或其他已安装文件接管。
	// cleanupSupersededHelpers 也按目录内全部 helper 的并集判断覆盖率，
	// 因此分两次安装常用包和重型包时不应误报冲突。
	providedByOthers := func(file string, id string) bool {
		if incoming[id] {
			return true
		}
		for _, other := range inventories {
			if other.view.File != file && other.ids[id] {
				return true
			}
		}
		return false
	}

	for _, inventory := range inventories {
		file := inventory.view
		fileIDs := inventory.ids
		fileAliases := inventory.aliases

		uncovered := make([]string, 0)
		for id := range fileIDs {
			if !providedByOthers(file.File, id) {
				uncovered = append(uncovered, id)
			}
		}
		sort.Strings(uncovered)
		// 合并包完整接管旧文件的全部条目时，安装后的 cleanupSupersededHelpers
		// 会删除旧文件，因此不需要再要求管理员手动卸载。
		covered := bundle && len(uncovered) == 0

		addFileConflict := func(id, conflictType string) {
			if covered {
				return
			}
			key := kind + "\x00" + id + "\x00" + conflictType + "\x00" + file.File
			if seen[key] {
				return
			}
			seen[key] = true
			conflicts = append(conflicts, pluginConflict{
				Kind: kind,
				ID:   id,
				Type: conflictType,
				Existing: &pluginConflictExisting{
					Kind:      kind,
					File:      file.File,
					PluginID:  file.PluginID,
					Version:   file.Version,
					Source:    file.Source,
					Enabled:   file.Enabled,
					Covered:   covered,
					Uncovered: uncovered,
					Usage:     s.pluginFileUsage(kind, fileIDs),
				},
				Actions: []string{"uninstall_existing", "cancel"},
			})
		}

		for id := range incoming {
			if fileIDs[id] {
				addFileConflict(id, pluginConflictTypeID)
				continue
			}
			if _, ok := fileAliases[id]; ok {
				addFileConflict(id, pluginConflictTypeAlias)
			}
		}
		for alias := range incomingAlias {
			if fileIDs[alias] {
				addFileConflict(alias, pluginConflictTypeAlias)
				continue
			}
			if _, ok := fileAliases[alias]; ok {
				addFileConflict(alias, pluginConflictTypeAlias)
			}
		}
	}

	if len(conflicts) == 0 {
		return nil
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Type != conflicts[j].Type {
			return conflicts[i].Type < conflicts[j].Type
		}
		if conflicts[i].ID != conflicts[j].ID {
			return conflicts[i].ID < conflicts[j].ID
		}
		left, right := "", ""
		if conflicts[i].Existing != nil {
			left = conflicts[i].Existing.File
		}
		if conflicts[j].Existing != nil {
			right = conflicts[j].Existing.File
		}
		return left < right
	})
	return &pluginConflictError{
		PluginID:   pluginID,
		PluginName: pluginName,
		Conflicts:  conflicts,
	}
}

// pluginItemAliases 返回条目 ID 到别名的映射。
// 使用数据库中的 manifest 缓存，因此已停用文件也能参与冲突检测，
// 但不会为了检查冲突而执行被隔离的 helper。
func (s *Server) pluginItemAliases(kind string) map[string][]string {
	out := make(map[string][]string)
	if s.Store == nil {
		return out
	}
	list, err := s.Store.ListPluginMetadata(kind)
	if err != nil {
		return out
	}
	for _, row := range list {
		if strings.TrimSpace(row.Manifest) == "" {
			continue
		}
		var manifest struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
			continue
		}
		id := normalizePluginItemID(kind, row.ID)
		if id == "" {
			continue
		}
		for _, alias := range manifest.Aliases {
			if alias = normalizePluginItemID(kind, alias); alias != "" {
				out[id] = append(out[id], alias)
			}
		}
	}
	return out
}

// pluginFileUsage 统计某个 helper 提供的条目当前被多少账户、部署目标或通知渠道使用。
func (s *Server) pluginFileUsage(kind string, ids map[string]bool) pluginConflictUsage {
	var usage pluginConflictUsage
	if s.Store == nil || len(ids) == 0 {
		return usage
	}
	switch kind {
	case pluginKindDNS:
		if list, err := s.Store.ListDNSAccounts(); err == nil {
			for _, account := range list {
				if ids[normalizePluginItemID(kind, account.Provider)] {
					usage.Accounts++
				}
			}
		}
	case pluginKindDeploy:
		if list, err := s.Store.ListDeployAccounts(); err == nil {
			for _, account := range list {
				if ids[normalizePluginItemID(kind, account.Type)] {
					usage.Targets++
				}
			}
		}
	case pluginKindNotify:
		if s.Manager != nil {
			for id := range s.Manager.LoadNotifyChannels() {
				if ids[normalizePluginItemID(kind, id)] {
					usage.Channels++
				}
			}
		}
	}
	return usage
}

// isBuiltinPluginID 判断 ID/别名是否由主程序内置实现占用。
func isBuiltinPluginID(kind, id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	switch kind {
	case pluginKindDeploy:
		// 未启用的内置部署目标只保留在元数据中、未注册到运行期，
		// 因此必须同时查询元数据，不能只看注册表。
		if _, ok := deployplugin.BuiltinDeployerInfo(id); ok {
			return true
		}
		return service.BuiltinDeployTypeIDs()[id]
	case pluginKindNotify:
		if _, ok := notifyplugin.BuiltinChannelInfo(id); ok {
			return true
		}
		return service.BuiltinNotifyChannelIDs()[id]
	case pluginKindDNS:
		// 同样按元数据判断：已编译但未启用的服务商也是保留 ID。
		if _, ok := dnsplugin.BuiltinProviderInfo(id); ok {
			return true
		}
		if plugin, ok := dnsplugin.Lookup(id); ok && plugin.Source() != dnsplugin.SourceExec {
			return true
		}
	}
	return false
}

// failPluginConflict 在冲突错误时返回 409 与结构化冲突列表。
// 返回 true 表示响应已经写出，调用方不再继续处理。
func failPluginConflict(c *gin.Context, err error) bool {
	var conflictErr *pluginConflictError
	if !errors.As(err, &conflictErr) {
		return false
	}
	name := strings.TrimSpace(conflictErr.PluginName)
	if name == "" {
		name = conflictErr.PluginID
	}
	params := gin.H{"plugin": name}
	c.Abort()
	payload := gin.H{
		"success":   false,
		"code":      "api.error.plugin_conflict",
		"message":   i18n.T(locale(c), "api.error.plugin_conflict", params),
		"params":    params,
		"conflicts": conflictErr.Conflicts,
	}
	if id := c.GetString(requestIDKey); id != "" {
		payload["request_id"] = id
	}
	c.JSON(http.StatusConflict, payload)
	return true
}
