package api

import (
	"testing"

	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
	notifyplugin "lactssl/internal/plugin/host/notify"
)

func TestBuiltinPluginsDiscoverAndToggle(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	s.BuiltinPluginsEnabled = true
	s.DeployPluginsEnabled = true
	s.NotifyPluginsEnabled = true
	t.Cleanup(func() {
		s.BuiltinPluginsEnabled = false
		s.DiscoverPlugins()
		s.DiscoverDeployPlugins()
		s.DiscoverNotifyPlugins()
	})

	s.DiscoverPlugins()
	if dnsplugin.Has("cloudflare") {
		t.Fatal("DNS providers must be disabled by default")
	}
	if len(dnsplugin.BuiltinProviders()) < 200 {
		t.Fatalf("unexpected builtin DNS metadata count: %d", len(dnsplugin.BuiltinProviders()))
	}

	s.DiscoverDeployPlugins()
	if !deployplugin.Has("webhook") {
		t.Fatal("default builtin deploy target should be registered")
	}
	if len(deployplugin.BuiltinDeployers()) != 25 {
		t.Fatalf("unexpected builtin deploy count: %d", len(deployplugin.BuiltinDeployers()))
	}

	s.DiscoverNotifyPlugins()
	if !notifyplugin.Has("telegram") {
		t.Fatal("default builtin notify channel should be registered")
	}

	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/cloudflare", `{"enabled":true}`); rec.Code != 200 {
		t.Fatalf("enable builtin DNS provider: %d %s", rec.Code, rec.Body.String())
	}
	if !dnsplugin.Has("cloudflare") {
		t.Fatal("enabled builtin DNS provider should be registered")
	}
	if rec := putPluginToggle(t, r, "/entry/api/plugins/providers/cloudflare", `{"enabled":false}`); rec.Code != 200 {
		t.Fatalf("disable builtin DNS provider: %d %s", rec.Code, rec.Body.String())
	}
	if dnsplugin.Has("cloudflare") {
		t.Fatal("disabled builtin DNS provider should be unregistered")
	}
}
