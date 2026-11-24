package api

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
	notifyplugin "lactssl/internal/plugin/host/notify"
)

// TestConcurrentDiscoverDoesNotPanic 覆盖两个管理员同时启停/重扫的场景。
// 修复前内置注册表先注销再逐条注册，交错执行会触发重复注册 panic，
// 并把注册表留在半注册状态。
func TestConcurrentDiscoverDoesNotPanic(t *testing.T) {
	s, _, _ := newPluginTestServer(t)
	s.BuiltinPluginsEnabled = true
	s.DeployPluginsEnabled = true
	s.NotifyPluginsEnabled = true
	t.Cleanup(func() {
		s.PluginsEnabled = false
		s.DeployPluginsEnabled = false
		s.NotifyPluginsEnabled = false
		s.BuiltinPluginsEnabled = false
		s.DiscoverPlugins()
		s.DiscoverDeployPlugins()
		s.DiscoverNotifyPlugins()
	})

	// 先扫描一次以生成内置元数据，再模拟「全部启用 DNS 服务商」。
	s.DiscoverPlugins()
	ids := make([]string, 0, 256)
	for _, provider := range dnsplugin.BuiltinProviders() {
		ids = append(ids, provider.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetSetting(dnsPluginEnabledSetting, string(raw)); err != nil {
		t.Fatal(err)
	}

	var panics int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					atomic.AddInt32(&panics, 1)
					t.Errorf("concurrent discover panicked: %v", recovered)
				}
			}()
			for round := 0; round < 20; round++ {
				s.DiscoverPlugins()
				s.DiscoverDeployPlugins()
				s.DiscoverNotifyPlugins()
			}
		}()
	}
	wg.Wait()

	if panics != 0 {
		t.Fatalf("concurrent discover panics = %d", panics)
	}
	if !dnsplugin.Has("cloudflare") {
		t.Fatal("enabled builtin DNS provider should stay registered")
	}
	if !deployplugin.Has("webhook") {
		t.Fatal("default builtin deploy target should stay registered")
	}
	if !notifyplugin.Has("telegram") {
		t.Fatal("default builtin notify channel should stay registered")
	}
}
