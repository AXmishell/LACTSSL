package builtin

import (
	"testing"

	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
	notifyplugin "lactssl/internal/plugin/host/notify"
)

func TestSyncNotifyRegistersEnabledBuiltin(t *testing.T) {
	SyncNotify(true, true, IDSet([]string{"telegram"}))
	if _, ok := notifyplugin.Lookup("telegram"); !ok {
		t.Fatal("telegram should be registered")
	}
	if _, ok := notifyplugin.Lookup("bark"); ok {
		t.Fatal("bark should not be registered when not enabled")
	}
	info, ok := notifyplugin.ExternalChannelInfo("telegram")
	if !ok || info.Source != string(notifyplugin.SourceBuiltin) {
		t.Fatalf("telegram metadata missing: %+v %v", info, ok)
	}
	if len(notifyplugin.BuiltinChannels()) != 23 {
		t.Fatalf("unexpected builtin channel count: %d", len(notifyplugin.BuiltinChannels()))
	}
}

func TestSyncDeployRegistersEnabledBuiltin(t *testing.T) {
	SyncDeploy(true, true, IDSet([]string{"webhook"}))
	if _, ok := deployplugin.Lookup("webhook"); !ok {
		t.Fatal("webhook should be registered")
	}
	if _, ok := deployplugin.Lookup("baota"); ok {
		t.Fatal("baota should not be registered when not enabled")
	}
	info, ok := deployplugin.ExternalDeployerInfo("webhook")
	if !ok || info.Source != string(deployplugin.SourceBuiltin) {
		t.Fatalf("webhook metadata missing: %+v %v", info, ok)
	}
	if len(deployplugin.BuiltinDeployers()) != 25 {
		t.Fatalf("unexpected builtin deployer count: %d", len(deployplugin.BuiltinDeployers()))
	}
}

func TestSyncDNSRegistersEnabledBuiltin(t *testing.T) {
	SyncDNS(true, true, IDSet([]string{"cloudflare"}))
	if _, ok := dnsplugin.Lookup("cloudflare"); !ok {
		t.Fatal("cloudflare should be registered")
	}
	if _, ok := dnsplugin.Lookup("alidns"); ok {
		t.Fatal("alidns should not be registered when not enabled")
	}
	info, ok := dnsplugin.ExternalProviderInfo("cloudflare")
	if !ok || info.Source != string(dnsplugin.SourceBuiltin) {
		t.Fatalf("cloudflare metadata missing: %+v %v", info, ok)
	}
	if len(dnsplugin.BuiltinProviders()) == 0 {
		t.Fatal("builtin DNS metadata should not be empty")
	}
}
