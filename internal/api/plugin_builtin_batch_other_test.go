package api

import (
	"testing"

	deployplugin "lactssl/internal/plugin/host/deploy"
	notifyplugin "lactssl/internal/plugin/host/notify"
)

func TestBatchEnableAllBuiltinDeployAndNotifyWithoutIDs(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	r.PUT("/entry/api/plugins/deployers", s.setDeployPluginDeployersEnabled)
	r.PUT("/entry/api/plugins/notify-channels", s.setNotifyPluginChannelsEnabled)
	s.BuiltinPluginsEnabled = true
	s.DeployPluginsEnabled = true
	s.NotifyPluginsEnabled = true
	s.DiscoverDeployPlugins()
	s.DiscoverNotifyPlugins()

	if rec := putBatchToggle(t, r, "/entry/api/plugins/deployers", `{"enabled":false}`); rec.Code != 200 {
		t.Fatalf("disable deployers: %d %s", rec.Code, rec.Body.String())
	}
	if deployplugin.Has("webhook") {
		t.Fatal("builtin deployer should be disabled")
	}
	if rec := putBatchToggle(t, r, "/entry/api/plugins/deployers", `{"enabled":true}`); rec.Code != 200 {
		t.Fatalf("enable deployers: %d %s", rec.Code, rec.Body.String())
	}
	if !deployplugin.Has("webhook") {
		t.Fatal("builtin deployer should be enabled")
	}

	if rec := putBatchToggle(t, r, "/entry/api/plugins/notify-channels", `{"enabled":false}`); rec.Code != 200 {
		t.Fatalf("disable channels: %d %s", rec.Code, rec.Body.String())
	}
	if notifyplugin.Has("telegram") {
		t.Fatal("builtin channel should be disabled")
	}
	if rec := putBatchToggle(t, r, "/entry/api/plugins/notify-channels", `{"enabled":true}`); rec.Code != 200 {
		t.Fatalf("enable channels: %d %s", rec.Code, rec.Body.String())
	}
	if !notifyplugin.Has("telegram") {
		t.Fatal("builtin channel should be enabled")
	}
}
