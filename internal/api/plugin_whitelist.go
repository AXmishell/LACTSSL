package api

import (
	"strings"

	"lactssl/internal/plugin/host/deploy"
	"lactssl/internal/plugin/host/dns"
	"lactssl/internal/plugin/host/notify"
	"lactssl/internal/plugin/whitelist"
	"lactssl/internal/service"
)

// pluginWhitelists 保存 DNS / 部署 / 通知三类条目启用白名单。
// 三类差异通过 whitelist.Spec 描述，读写与并发控制由 whitelist.Service 统一实现。
type pluginWhitelists struct {
	DNS    *whitelist.Service
	Deploy *whitelist.Service
	Notify *whitelist.Service
}

func newPluginWhitelists(s *Server) *pluginWhitelists {
	return &pluginWhitelists{
		DNS: whitelist.New(whitelist.Spec{
			Kind:       pluginKindDNS,
			SettingKey: dnsPluginEnabledSetting,
			LegacyKeys: []string{dnsPluginLegacyDisabledSetting},
			Defaults:   defaultDNSPluginEnabledSet,
			Normalize:  func(id string) string { return normalizePluginItemID(pluginKindDNS, id) },
			Valid: func(id string) bool {
				_, found := dnsplugin.ExternalProviderInfo(id)
				return found
			},
			Known: func() map[string]bool {
				known := s.pluginFileKnownItems(pluginKindDNS)
				for id := range dnsplugin.BuiltinProviderIDs() {
					known[id] = true
				}
				return known
			},
			Retain: isDefaultDNSPluginProvider,
		}, s.Store),
		Deploy: whitelist.New(whitelist.Spec{
			Kind:       pluginKindDeploy,
			SettingKey: deployPluginEnabledSetting,
			Defaults:   defaultDeployPluginEnabledSet,
			Normalize:  func(id string) string { return strings.ToLower(strings.TrimSpace(id)) },
			LookupKey:  func(id string) string { return normalizePluginItemID(pluginKindDeploy, id) },
			Valid: func(id string) bool {
				_, found := deployplugin.ExternalDeployerInfo(id)
				return found
			},
			Known: func() map[string]bool {
				known := s.pluginFileKnownItems(pluginKindDeploy)
				for id := range deployplugin.BuiltinDeployerIDs() {
					known[id] = true
				}
				return known
			},
			Retain:    isDefaultDeployPluginDeployer,
			AfterLoad: promoteMergedDeployTypes,
			AfterDisable: func(set map[string]bool, id string) {
				// 停用合并目标时一并移除旧 ID，避免白名单补全逻辑把它重新启用。
				for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
					if canonical == id {
						delete(set, legacy)
					}
				}
			},
		}, s.Store),
		Notify: whitelist.New(whitelist.Spec{
			Kind:       pluginKindNotify,
			SettingKey: notifyPluginEnabledSetting,
			Defaults:   defaultNotifyPluginEnabledSet,
			Normalize:  func(id string) string { return normalizePluginItemID(pluginKindNotify, id) },
			Valid: func(id string) bool {
				_, found := notifyplugin.ExternalChannelInfo(id)
				return found
			},
			Known: func() map[string]bool {
				known := s.pluginFileKnownItems(pluginKindNotify)
				for id := range notifyplugin.BuiltinChannelIDs() {
					known[id] = true
				}
				return known
			},
			Retain: isDefaultNotifyPluginChannel,
		}, s.Store),
	}
}

func (s *Server) whitelistServices() *pluginWhitelists {
	s.whitelistOnce.Do(func() {
		s.pluginWhitelists = newPluginWhitelists(s)
	})
	return s.pluginWhitelists
}

// 以下方法保留原有 receiver 形态，便于 handler 与测试逐步迁移到
// pluginWhitelists；实际读写逻辑已经不在 api 层。
func (s *Server) dnsPluginEnabledSet() map[string]bool {
	return s.whitelistServices().DNS.EnabledSet()
}

func (s *Server) dnsPluginEnabledProviders() []string {
	return s.whitelistServices().DNS.Enabled()
}

func (s *Server) saveDNSPluginEnabledSet(set map[string]bool) error {
	return s.whitelistServices().DNS.Save(set)
}

func (s *Server) updateDNSPluginProviders(ids []string, enabled bool) ([]string, []string, error) {
	return s.whitelistServices().DNS.Update(ids, enabled)
}

func (s *Server) deployPluginEnabledSet() map[string]bool {
	return s.whitelistServices().Deploy.EnabledSet()
}

func (s *Server) deployPluginEnabledDeployers() []string {
	return s.whitelistServices().Deploy.Enabled()
}

func (s *Server) saveDeployPluginEnabledSet(set map[string]bool) error {
	return s.whitelistServices().Deploy.Save(set)
}

func (s *Server) updateDeployPluginDeployers(ids []string, enabled bool) ([]string, []string, error) {
	return s.whitelistServices().Deploy.Update(ids, enabled)
}

func (s *Server) notifyPluginEnabledSet() map[string]bool {
	return s.whitelistServices().Notify.EnabledSet()
}

func (s *Server) notifyPluginEnabledChannels() []string {
	return s.whitelistServices().Notify.Enabled()
}

func (s *Server) saveNotifyPluginEnabledSet(set map[string]bool) error {
	return s.whitelistServices().Notify.Save(set)
}

func (s *Server) updateNotifyPluginChannels(ids []string, enabled bool) ([]string, []string, error) {
	return s.whitelistServices().Notify.Update(ids, enabled)
}

// defaultDNSPluginEnabledSet 返回默认启用的插件服务商集合。
// 新安装默认不启用任何 DNS 服务商，由管理员在插件页按需开启。
func defaultDNSPluginEnabledSet() map[string]bool {
	return map[string]bool{}
}

func isDefaultDNSPluginProvider(id string) bool {
	id = normalizePluginItemID(pluginKindDNS, id)
	for _, builtin := range service.BuiltinDNSProviderIDs() {
		if normalizePluginItemID(pluginKindDNS, builtin) == id {
			return true
		}
	}
	return false
}

// defaultDeployPluginEnabledSet 返回默认启用的插件部署目标集合。
// 迁移前内置的部署目标改由 helper 提供后仍默认启用；其余插件目标默认停用。
func defaultDeployPluginEnabledSet() map[string]bool {
	ids := service.DefaultPluginDeployIDs()
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[strings.ToLower(strings.TrimSpace(id))] = true
	}
	return out
}

func isDefaultDeployPluginDeployer(id string) bool {
	return service.IsDefaultPluginDeployID(normalizePluginItemID(pluginKindDeploy, id))
}

// promoteMergedDeployTypes 在白名单包含旧目标时补充其合并后的规范目标。
func promoteMergedDeployTypes(set map[string]bool) {
	for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
		if set[legacy] && !set[canonical] {
			set[canonical] = true
		}
	}
}

// defaultNotifyPluginEnabledSet 返回默认启用的通知渠道集合。
// 迁移前内置的渠道改由 helper 提供后仍默认启用；其余插件渠道默认停用。
func defaultNotifyPluginEnabledSet() map[string]bool {
	ids := service.DefaultNotifyChannelIDs()
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[normalizePluginItemID(pluginKindNotify, id)] = true
	}
	return out
}

func isDefaultNotifyPluginChannel(id string) bool {
	id = normalizePluginItemID(pluginKindNotify, id)
	for _, builtin := range service.DefaultNotifyChannelIDs() {
		if normalizePluginItemID(pluginKindNotify, builtin) == id {
			return true
		}
	}
	return false
}
