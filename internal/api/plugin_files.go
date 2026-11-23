package api

import (
	"strings"

	"lactssl/internal/service"
)

// legacyPluginNames 是历史全量 helper 文件名，用于在插件页标记旧版来源。
var legacyPluginNames = map[string][]string{
	pluginKindDNS:    {"lactssl-dns-helper-extra", "lactssl-dns-helper", "lactssl-dns-helper-full", "dns-helper"},
	pluginKindDeploy: {"lactssl-deploy-helper", "deploy-helper"},
	pluginKindNotify: {"lactssl-notify-helper", "notify-helper"},
}

// pluginKindDir 返回插件类别目录及该类别是否启用。
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

// rescanPluginKind 重新发现指定类别的插件并返回错误。
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

// canonicalPluginID 把已合并的旧部署目标 ID 归一化为规范 ID。
func canonicalPluginID(kind, id string) string {
	if kind == pluginKindDeploy {
		if canonical, ok := service.DeprecatedDeployTypeCanonical[id]; ok {
			return canonical
		}
	}
	return id
}
