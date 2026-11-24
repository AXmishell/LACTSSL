package api

import (
	pluginmanager "lactssl/internal/plugin/manager"
	"lactssl/internal/service"
)

func (s *Server) pluginManagerService() *pluginmanager.Manager {
	s.pluginManagerOnce.Do(func() {
		s.pluginManager = pluginmanager.New(s.pluginManagerOptions(), s.Store)
	})
	// 插件开关可能在构造后被测试或嵌入式调用修改；每次使用前同步一次配置。
	s.pluginManager.Configure(s.pluginManagerOptions())
	return s.pluginManager
}

func (s *Server) pluginManagerOptions() pluginmanager.Options {
	return pluginmanager.Options{
		BuiltinEnabled: s.BuiltinPluginsEnabled,
		DNS: pluginmanager.KindOptions{
			Enabled:       s.PluginsEnabled,
			Dir:           s.PluginDir,
			Sandbox:       s.PluginSandbox,
			MemoryLimitMB: s.PluginMemoryLimitMB,
			Timeout:       s.PluginTimeout,
			MaxOutputKB:   s.PluginMaxOutputKB,
		},
		Deploy: pluginmanager.KindOptions{
			Enabled:       s.DeployPluginsEnabled,
			Dir:           s.DeployPluginDir,
			Sandbox:       s.DeployPluginSandbox,
			MemoryLimitMB: s.DeployPluginMemoryLimitMB,
			Timeout:       s.DeployPluginTimeout,
			MaxOutputKB:   s.DeployPluginMaxOutputKB,
		},
		Notify: pluginmanager.KindOptions{
			Enabled:       s.NotifyPluginsEnabled,
			Dir:           s.NotifyPluginDir,
			Sandbox:       s.NotifyPluginSandbox,
			MemoryLimitMB: s.NotifyPluginMemoryLimitMB,
			Timeout:       s.NotifyPluginTimeout,
			MaxOutputKB:   s.NotifyPluginMaxOutputKB,
		},
		EnabledItems: func(kind string) []string {
			switch kind {
			case pluginmanager.KindDNS:
				return s.dnsPluginEnabledProviders()
			case pluginmanager.KindDeploy:
				return s.deployPluginEnabledDeployers()
			case pluginmanager.KindNotify:
				return s.notifyPluginEnabledChannels()
			default:
				return nil
			}
		},
		KnownIDs: func(kind string) map[string]bool {
			switch kind {
			case pluginmanager.KindDeploy:
				return service.BuiltinDeployTypeIDs()
			case pluginmanager.KindNotify:
				return service.BuiltinNotifyChannelIDs()
			default:
				return nil
			}
		},
		SyncFileMeta: s.syncPluginFileMeta,
		SyncMetadata: s.syncPluginMetadata,
		AfterDeployDiscover: func() {
			if s.Manager == nil {
				return
			}
			_ = s.Manager.MigrateDeployConfigSplit()
			_ = s.Manager.MigrateDeployTargets()
		},
	}
}
