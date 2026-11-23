package builtin

import (
	"os"
	"testing"

	dnsplugin "lactssl/internal/plugin/dns"
)

func TestBuiltinDNSEnvIsRestored(t *testing.T) {
	SyncDNS(true, true, IDSet([]string{"cloudflare"}))
	plugin, ok := dnsplugin.Lookup("cloudflare")
	if !ok {
		t.Fatal("cloudflare not registered")
	}
	const key = "CLOUDFLARE_DNS_API_TOKEN"
	previous, had := os.LookupEnv(key)
	defer func() {
		if had {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	_ = os.Setenv(key, "outer")
	if _, err := plugin.New(map[string]string{"api_token": "inner"}); err != nil {
		t.Fatalf("construct provider: %v", err)
	}
	if value := os.Getenv(key); value != "outer" {
		t.Fatalf("env not restored: %q", value)
	}
}
