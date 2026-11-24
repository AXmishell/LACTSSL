package api

import (
	"encoding/json"
	"strings"

	"lactssl/internal/model"
	"lactssl/internal/plugin/host/deploy"
	"lactssl/internal/plugin/host/dns"
	"lactssl/internal/plugin/host/notify"
	"lactssl/internal/plugin/proto"
	"lactssl/internal/plugin/runtime/metadata"
)

// syncPluginMetadata 把当前发现的插件条目写入内存与数据库缓存。
// 数据库中的旧条目（插件已卸载或文件已停用）会先重新加载，因此内存缓存
// 只替换当前 kind，而不会丢失 tombstone。缓存只用于展示、脱敏与字段校验，
// 不参与插件执行。
func (s *Server) syncPluginMetadata(kind string) {
	entries := make(map[string]pluginmeta.Entry)
	existing := make(map[string]pluginmeta.Entry)
	if s.Store != nil {
		if rows, err := s.Store.ListPluginMetadata(kind); err == nil {
			for _, row := range rows {
				if strings.TrimSpace(row.Manifest) == "" || len(row.Manifest) > 1<<20 {
					continue
				}
				id := strings.ToLower(strings.TrimSpace(row.ID))
				if id == "" {
					continue
				}
				entry := pluginmeta.Entry{
					Kind:    kind,
					ID:      id,
					Version: row.Version,
					Name:    row.Name,
					Raw:     json.RawMessage(row.Manifest),
				}
				existing[id] = entry
				entries[id] = entry
			}
		}
	}

	add := func(id, version, name string, value any) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return
		}
		entry := pluginmeta.Entry{
			Kind:    kind,
			ID:      id,
			Version: version,
			Name:    name,
			Raw:     raw,
		}
		entries[id] = entry
		if s.Store == nil {
			return
		}
		// 启动时大多数条目没有变化，避免每次扫描都写一遍数据库。
		if old, ok := existing[id]; ok && old.Version == version && old.Name == name && string(old.Raw) == string(raw) {
			return
		}
		_ = s.Store.UpsertPluginMetadata(model.PluginMetadata{
			Kind:     kind,
			ID:       id,
			Version:  version,
			Name:     name,
			Manifest: string(raw),
		})
	}

	switch kind {
	case pluginKindDNS:
		for _, item := range dnsplugin.ExternalProviders() {
			add(item.ID, item.Version, item.Name, pluginproto.DNSProvider{
				ID:              item.ID,
				Name:            item.Name,
				NameI18n:        item.NameI18n,
				Description:     item.Description,
				DescriptionI18n: item.DescriptionI18n,
				Aliases:         item.Aliases,
				Fields:          item.Fields,
			})
		}
	case pluginKindDeploy:
		for _, item := range deployplugin.ExternalDeployers() {
			add(item.ID, item.Version, item.Name, pluginproto.Deployer{
				ID:              item.ID,
				Name:            item.Name,
				NameI18n:        item.NameI18n,
				Description:     item.Description,
				DescriptionI18n: item.DescriptionI18n,
				Aliases:         item.Aliases,
				Fields:          item.Fields,
				DeployFields:    item.DeployFields,
			})
		}
	case pluginKindNotify:
		for _, item := range notifyplugin.ExternalChannels() {
			add(item.ID, item.Version, item.Name, pluginproto.NotifyChannel{
				ID:          item.ID,
				Name:        item.Name,
				Description: item.Description,
				Aliases:     item.Aliases,
				Fields:      item.Fields,
			})
		}
	}

	list := make([]pluginmeta.Entry, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	pluginmeta.ReplaceKind(kind, list)
}
