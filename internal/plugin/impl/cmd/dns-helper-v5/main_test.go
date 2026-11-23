package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"lactssl/internal/plugin/impl/dns"
)

func TestManifestCoversAllV5Providers(t *testing.T) {
	manifest := buildManifest()
	if len(manifest.Providers) != 235 {
		t.Fatalf("providers = %d, want 235 (228 v5 + 7 legacy placeholders)", len(manifest.Providers))
	}
	for _, provider := range manifest.Providers {
		if provider.ID == "" {
			t.Fatal("provider id is empty")
		}
		if len(provider.Fields) == 0 {
			t.Fatalf("provider %s has no fields", provider.ID)
		}
		for _, field := range provider.Fields {
			if field.Key == "" {
				t.Fatalf("provider %s has field without key: %+v", provider.ID, field)
			}
		}
	}
}

func TestManifestAliases(t *testing.T) {
	manifest := buildManifest()
	aliases := map[string]bool{}
	for _, provider := range manifest.Providers {
		for _, alias := range provider.Aliases {
			aliases[alias] = true
		}
	}
	for _, alias := range []string{"dnsupdate", "webnamesru", "acme-dns", "fastdns"} {
		if !aliases[alias] {
			t.Errorf("missing alias %s", alias)
		}
	}
}

type fakeWriteProvider struct {
	presentCalls int
	cleanupCalls int
	presentErr   error
	cleanupErr   error
	domain       string
	token        string
	keyAuth      string
}

func (p *fakeWriteProvider) Present(domain, token, keyAuth string) error {
	p.presentCalls++
	p.domain, p.token, p.keyAuth = domain, token, keyAuth
	return p.presentErr
}

func (p *fakeWriteProvider) CleanUp(domain, token, keyAuth string) error {
	p.cleanupCalls++
	return p.cleanupErr
}

func TestVerifyWriteCreatesAndCleansUpInOneProcess(t *testing.T) {
	provider := &fakeWriteProvider{}
	response := verifyWrite(provider, "example.com", i18n.ZhCN)
	if !response.OK || len(response.Checks) != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Checks[0].Key != "write" || response.Checks[0].Status != protocol.VerifyStatusOK {
		t.Fatalf("write check = %+v", response.Checks[0])
	}
	if response.Checks[1].Key != "cleanup" || response.Checks[1].Status != protocol.VerifyStatusOK {
		t.Fatalf("cleanup check = %+v", response.Checks[1])
	}
	if provider.presentCalls != 1 || provider.cleanupCalls != 1 {
		t.Fatalf("present=%d cleanup=%d", provider.presentCalls, provider.cleanupCalls)
	}
	if provider.domain != "example.com" || provider.token == "" || !strings.HasPrefix(provider.keyAuth, "lactssl-verify-") {
		t.Fatalf("unexpected challenge values: domain=%q token=%q keyAuth=%q", provider.domain, provider.token, provider.keyAuth)
	}
}

func TestVerifyWriteStopsWhenPresentFails(t *testing.T) {
	provider := &fakeWriteProvider{presentErr: errors.New("denied")}
	response := verifyWrite(provider, "example.com", i18n.ZhCN)
	if response.OK || len(response.Checks) != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Checks[0].Key != "write" || response.Checks[0].Status != protocol.VerifyStatusFailed {
		t.Fatalf("write check = %+v", response.Checks[0])
	}
	if provider.cleanupCalls != 0 {
		t.Fatalf("present 失败后不应执行 cleanup: %d", provider.cleanupCalls)
	}
}

func TestVerifyWriteReportsCleanupFailure(t *testing.T) {
	provider := &fakeWriteProvider{cleanupErr: errors.New("cleanup denied")}
	response := verifyWrite(provider, "example.com", i18n.ZhCN)
	if response.OK || len(response.Checks) != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Checks[1].Key != "cleanup" || response.Checks[1].Status != protocol.VerifyStatusFailed {
		t.Fatalf("cleanup check = %+v", response.Checks[1])
	}
}

func TestProviderCapabilities(t *testing.T) {
	if caps := providerCapabilities("not-verifiable", true, false); len(caps) != 0 {
		t.Fatalf("未启用写验证时不应声明能力: %v", caps)
	}
	caps := providerCapabilities("not-verifiable", true, true)
	if len(caps) != 1 || caps[0] != protocol.FeatureVerifyWrite {
		t.Fatalf("应只声明写验证能力: %v", caps)
	}
	dns.MarkVerifiable("cap-test")
	caps = providerCapabilities("cap-test", true, true)
	if len(caps) != 2 || caps[0] != protocol.FeatureVerifyWrite || caps[1] != protocol.FeatureVerify {
		t.Fatalf("应同时声明只读与写验证能力: %v", caps)
	}
}
