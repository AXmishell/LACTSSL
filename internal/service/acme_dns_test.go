package service

import (
	"os"
	"strings"
	"testing"

	legodns "github.com/go-acme/lego/v4/providers/dns"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
)

func TestBuiltinProviderEnv(t *testing.T) {
	alidns, ok := DNSProviderInfo("alidns")
	if !ok {
		t.Fatal("alidns provider missing")
	}
	envs := builtinProviderEnv(alidns, map[string]string{
		"access_key_id":     "id",
		"access_key_secret": "secret",
		"region_id":         "cn-hangzhou",
	})
	if envs["ALICLOUD_ACCESS_KEY"] != "id" || envs["ALICLOUD_SECRET_KEY"] != "secret" {
		t.Fatalf("unexpected alidns env: %v", envs)
	}

	cloudflare, ok := DNSProviderInfo("cloudflare")
	if !ok {
		t.Fatal("cloudflare provider missing")
	}
	envs = builtinProviderEnv(cloudflare, map[string]string{
		"api_token": "token",
		"email":     "user@example.com",
		"api_key":   "key",
		"base_url":  "https://cf.example.com",
	})
	if envs["CLOUDFLARE_DNS_API_TOKEN"] != "token" {
		t.Fatalf("missing cloudflare token: %v", envs)
	}
	if _, ok := envs["CLOUDFLARE_EMAIL"]; ok {
		t.Fatal("email should not be set when an API token is used")
	}
	if _, ok := envs["CLOUDFLARE_API_KEY"]; ok {
		t.Fatal("api key should not be set when an API token is used")
	}
	if envs["CLOUDFLARE_BASE_URL"] != "https://cf.example.com" {
		t.Fatalf("missing base url: %v", envs)
	}

	envs = builtinProviderEnv(cloudflare, map[string]string{
		"email":   "user@example.com",
		"api_key": "key",
	})
	if _, ok := envs["CLOUDFLARE_DNS_API_TOKEN"]; ok {
		t.Fatal("token should not be set when using the global API key")
	}
	if envs["CLOUDFLARE_EMAIL"] != "user@example.com" || envs["CLOUDFLARE_API_KEY"] != "key" {
		t.Fatalf("unexpected cloudflare key env: %v", envs)
	}
}

func TestGeneratedDNSProvidersRegistered(t *testing.T) {
	if len(generatedDNSProviders) < 100 {
		t.Fatalf("generated providers missing: %d", len(generatedDNSProviders))
	}
	if len(DNSProviders) < 190 {
		t.Fatalf("unexpected provider count: %d", len(DNSProviders))
	}
	for _, def := range generatedDNSProviders {
		if _, ok := DNSProviderInfo(def.Value); !ok {
			t.Errorf("provider %s not found in DNSProviders", def.Value)
		}
		if len(def.Fields) == 0 {
			t.Errorf("provider %s has no fields", def.Value)
		}
		for _, field := range def.Fields {
			if field.Env == "" {
				t.Errorf("provider %s field %s has no env", def.Value, field.Key)
			}
		}
		if _, err := legodns.NewDNSChallengeProviderByName(def.Value); err != nil && strings.Contains(err.Error(), "unsupported DNS provider") {
			t.Errorf("provider %s is not supported by lego: %v", def.Value, err)
		}
		for _, key := range []string{def.LabelKey, def.DescKey} {
			if i18n.T(i18n.ZhCN, key, nil) == key {
				t.Errorf("missing zh-CN translation for %s", key)
			}
			if i18n.T(i18n.EnUS, key, nil) == key {
				t.Errorf("missing en-US translation for %s", key)
			}
		}
	}

	for _, def := range DNSProviders {
		for _, key := range []string{def.LabelKey, def.DescKey} {
			if key == "" {
				t.Errorf("provider %s has no label or description key", def.Value)
				continue
			}
			if i18n.T(i18n.ZhCN, key, nil) == key {
				t.Errorf("missing zh-CN translation for %s", key)
			}
			if i18n.T(i18n.EnUS, key, nil) == key {
				t.Errorf("missing en-US translation for %s", key)
			}
		}
	}
}

func TestGeneratedProviderEnv(t *testing.T) {
	if !dnsplugin.Has("ovh") {
		t.Skip("ovh 未包含在当前构建中；使用 -tags dns_all 运行完整测试")
	}
	keys := []string{"OVH_APPLICATION_KEY", "OVH_APPLICATION_SECRET", "OVH_CONSUMER_KEY"}
	for _, key := range keys {
		old, existed := os.LookupEnv(key)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}

	provider, restore, err := newDNSProvider("ovh", map[string]string{
		"ovh_application_key":    "application-key",
		"ovh_application_secret": "application-secret",
		"ovh_consumer_key":       "consumer-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if value := os.Getenv("OVH_APPLICATION_KEY"); value != "application-key" {
		t.Fatalf("generated field was not applied: %q", value)
	}
	restore()
}

func TestNewDNSProviderAppliesAndRestoresEnv(t *testing.T) {
	if !dnsplugin.Has("rfc2136") {
		t.Skip("rfc2136 未包含在当前构建中；使用 -tags dns_all 运行完整测试")
	}
	const key = "DNSUPDATE_NAMESERVER"
	old, existed := os.LookupEnv(key)
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	provider, restore, err := newDNSProvider("rfc2136", map[string]string{"nameserver": "192.0.2.1:53"})
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if value, _ := os.LookupEnv(key); value != "192.0.2.1:53" {
		t.Fatalf("environment was not applied: %q", value)
	}
	restore()
	if value, _ := os.LookupEnv(key); existed && value != old {
		t.Fatalf("environment was not restored: %q", value)
	}
	if !existed {
		if _, ok := os.LookupEnv(key); ok {
			t.Fatal("temporary environment variable was not removed")
		}
	}
}
