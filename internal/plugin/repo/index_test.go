package pluginrepo

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lactssl/internal/i18n"
)

const testSHA = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func testPlugin(kind, id, version string) Plugin {
	return Plugin{
		Kind: kind, ID: id, Version: version, Protocol: "v1",
		Assets: map[string]Asset{
			"linux/amd64": {URL: "https://example.invalid/" + id + ".tgz", SHA256: testSHA, Size: 1},
		},
	}
}

func testIndex(t *testing.T, plugins ...Plugin) []byte {
	t.Helper()
	return mustJSON(t, Index{Schema: 1, Channel: "stable", Protocol: "v1", Plugins: plugins})
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseIndexAcceptsValidIndex(t *testing.T) {
	result, err := ParseIndex(testIndex(t, testPlugin("dns", "cloudflare", "1.0.0")), ParseOptions{
		Channel: "stable", IndexURL: "https://example.invalid/index.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Index.Plugins) != 1 || len(result.Rejected) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseIndexIsolatesBadEntry(t *testing.T) {
	good := testPlugin("dns", "good", "1.0.0")
	bad := testPlugin("dns", "bad", "1.0.0")
	bad.Assets["linux/amd64"] = Asset{URL: "https://example.invalid/bad.tgz", Size: 1}
	result, err := ParseIndex(testIndex(t, bad, good), ParseOptions{
		Channel: "stable", IndexURL: "https://example.invalid/index.json",
	})
	if err != nil {
		t.Fatalf("entry issue must not fail the whole index: %v", err)
	}
	if len(result.Index.Plugins) != 1 || result.Index.Plugins[0].ID != "good" {
		t.Fatalf("valid entry should survive: %+v", result.Index.Plugins)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Plugin.ID != "bad" {
		t.Fatalf("bad entry should be quarantined: %+v", result.Rejected)
	}
	if result.Rejected[0].Reason.Code != "plugin.repo.no_valid_sha256" {
		t.Fatalf("unexpected reason: %+v", result.Rejected[0].Reason)
	}
}

func TestParseIndexDuplicateIsFatal(t *testing.T) {
	plugin := testPlugin("dns", "cloudflare", "1.0.0")
	_, err := ParseIndex(testIndex(t, plugin, plugin), ParseOptions{})
	if err == nil || !strings.Contains(err.Error(), "插件重复") {
		t.Fatalf("duplicate plugin must be fatal, got %v", err)
	}
}

func TestParseIndexChannelMismatch(t *testing.T) {
	_, err := ParseIndex(testIndex(t, testPlugin("dns", "cloudflare", "1.0.0")), ParseOptions{
		Channel: "beta",
	})
	if err == nil {
		t.Fatal("channel mismatch should be fatal")
	}
}

func TestParseIndexRevisionRollback(t *testing.T) {
	raw := mustJSON(t, Index{
		Schema: 1, Channel: "stable", Protocol: "v1", Revision: 2,
		Plugins: []Plugin{testPlugin("dns", "cloudflare", "1.0.0")},
	})
	_, err := ParseIndex(raw, ParseOptions{LastRevision: 3})
	if err == nil {
		t.Fatal("lower revision should be rejected")
	}
	if _, err := ParseIndex(raw, ParseOptions{LastRevision: 2}); err != nil {
		t.Fatalf("equal revision should be accepted: %v", err)
	}
}

func TestParseIndexMissingRevisionAfterBaselineIsFatal(t *testing.T) {
	raw := mustJSON(t, Index{
		Schema: 1, Channel: "stable", Protocol: "v1",
		Plugins: []Plugin{testPlugin("dns", "cloudflare", "1.0.0")},
	})
	if _, err := ParseIndex(raw, ParseOptions{LastRevision: 3}); err == nil {
		t.Fatal("missing revision after accepting a revision must be fatal")
	}
}

func TestParseIndexStrictExpiry(t *testing.T) {
	raw := mustJSON(t, Index{
		Schema: 1, Channel: "stable", Protocol: "v1",
		ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		Plugins:   []Plugin{testPlugin("dns", "cloudflare", "1.0.0")},
	})
	result, err := ParseIndex(raw, ParseOptions{})
	if err != nil {
		t.Fatalf("expired index should only warn by default: %v", err)
	}
	if len(result.Issues) == 0 || result.Issues[0].Code != "plugin.repo.index_expired" {
		t.Fatalf("expected expiry warning: %+v", result.Issues)
	}
	if _, err := ParseIndex(raw, ParseOptions{StrictExpiry: true}); err == nil {
		t.Fatal("strict expiry should reject the index")
	}
}

func TestParseIndexFieldValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Plugin)
		code   string
	}{
		{"version", func(p *Plugin) { p.Version = "not-a-version" }, "plugin.repo.invalid_version"},
		{"min_host", func(p *Plugin) { p.MinHost = "abc" }, "plugin.repo.invalid_min_host"},
		{"protocol", func(p *Plugin) { p.Protocol = "v9" }, "plugin.repo.entry_protocol_unsupported"},
		{"platform", func(p *Plugin) {
			p.Assets = map[string]Asset{"linux/amd65": {URL: "https://example.invalid/a.tgz", SHA256: testSHA, Size: 1}}
		}, "plugin.repo.invalid_platform"},
		{"asset size", func(p *Plugin) {
			p.Assets["linux/amd64"] = Asset{URL: "https://example.invalid/a.tgz", SHA256: testSHA, Size: -1}
		}, "plugin.repo.invalid_asset_size"},
		{"asset url", func(p *Plugin) {
			p.Assets["linux/amd64"] = Asset{URL: "ftp://example.invalid/a.tgz", SHA256: testSHA, Size: 1}
		}, "plugin.repo.invalid_asset_url"},
		{"single bundle", func(p *Plugin) {
			p.Bundle, p.Single, p.Providers = true, true, []string{"cloudflare"}
		}, "plugin.repo.invalid_single"},
		{"variant self", func(p *Plugin) { p.VariantOf = p.ID }, "plugin.repo.invalid_variant"},
		{"conflict self", func(p *Plugin) { p.ConflictsWith = []string{p.ID} }, "plugin.repo.invalid_conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := testPlugin("dns", "cloudflare", "1.0.0")
			tc.mutate(&plugin)
			result, err := ParseIndex(testIndex(t, plugin), ParseOptions{
				Channel: "stable", IndexURL: "https://example.invalid/index.json",
			})
			if err != nil {
				t.Fatalf("entry issue should be quarantined: %v", err)
			}
			if len(result.Rejected) != 1 {
				t.Fatalf("expected one rejected entry: %+v", result)
			}
			if result.Rejected[0].Reason.Code != tc.code {
				t.Fatalf("reason = %s, want %s", result.Rejected[0].Reason.Code, tc.code)
			}
		})
	}
}

func TestParseIndexHTTPAssetUnderHTTPSIndex(t *testing.T) {
	plugin := testPlugin("dns", "cloudflare", "1.0.0")
	plugin.Assets["linux/amd64"] = Asset{URL: "http://example.invalid/a.tgz", SHA256: testSHA, Size: 1}
	result, err := ParseIndex(testIndex(t, plugin), ParseOptions{
		Channel: "stable", IndexURL: "https://example.invalid/index.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Reason.Code != "plugin.repo.invalid_asset_url" {
		t.Fatalf("HTTP asset should be rejected under HTTPS index: %+v", result)
	}
	if detail := result.Rejected[0].Reason.Detail; detail == nil || detail.Code != "plugin.repo.asset_http_forbidden" {
		t.Fatalf("unexpected asset URL detail: %+v", detail)
	}
}

func TestParseIndexIssueDetailsAreStructured(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Plugin)
		detail string
	}{
		{"missing providers", func(p *Plugin) { p.Bundle = true; p.Providers = nil }, "plugin.repo.detail.providers_missing"},
		{"invalid provider id", func(p *Plugin) { p.Bundle = true; p.Providers = []string{"BAD ID"} }, "plugin.repo.detail.provider_id_invalid"},
		{"variant self", func(p *Plugin) { p.VariantOf = p.ID }, "plugin.repo.detail.variant_self"},
		{"conflict self", func(p *Plugin) { p.ConflictsWith = []string{p.ID} }, "plugin.repo.detail.conflict_self"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := testPlugin("dns", "cloudflare", "1.0.0")
			tc.mutate(&plugin)
			result, err := ParseIndex(testIndex(t, plugin), ParseOptions{
				Channel: "stable", IndexURL: "https://example.invalid/index.json",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Rejected) != 1 || result.Rejected[0].Reason.Detail == nil {
				t.Fatalf("expected structured detail: %+v", result.Rejected)
			}
			if got := result.Rejected[0].Reason.Detail.Code; got != tc.detail {
				t.Fatalf("detail = %q, want %q", got, tc.detail)
			}
		})
	}
}

func TestIssueDetailLocalization(t *testing.T) {
	issue := Issue{
		Code:   "plugin.repo.invalid_providers",
		Params: map[string]any{"plugin": "dns/cloudflare"},
		Detail: issueDetail("plugin.repo.detail.providers_missing"),
	}
	en := issue.Text(i18n.EnUS)
	if !strings.Contains(en, "providers is missing") {
		t.Fatalf("English detail missing: %q", en)
	}
	zh := issue.Text(i18n.ZhCN)
	if !strings.Contains(zh, "缺少 providers") {
		t.Fatalf("Chinese detail missing: %q", zh)
	}
}

func TestParseIndexWarnsUnknownField(t *testing.T) {
	raw := []byte(`{"schema":1,"channel":"stable","protocol":"v1","future":"x","plugins":[{"kind":"dns","id":"cloudflare","version":"1.0.0","protocol":"v1","future_field":true,"assets":{"linux/amd64":{"url":"https://example.invalid/a.tgz","sha256":"` + testSHA + `","size":1}}}]}`)
	result, err := ParseIndex(raw, ParseOptions{Channel: "stable", IndexURL: "https://example.invalid/index.json"})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, issue := range result.Issues {
		if issue.Code == "plugin.repo.unknown_field" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected two unknown-field warnings, got %+v", result.Issues)
	}
}

func TestParseIndexConflictAsymmetry(t *testing.T) {
	a := testPlugin("dns", "a", "1.0.0")
	b := testPlugin("dns", "b", "1.0.0")
	a.ConflictsWith = []string{"b"}
	result, err := ParseIndex(testIndex(t, a, b), ParseOptions{
		Channel: "stable", IndexURL: "https://example.invalid/index.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range result.Issues {
		if issue.Code == "plugin.repo.conflict_asymmetric" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected asymmetric conflict warning: %+v", result.Issues)
	}
}

func TestPluginDigestStableAndSensitive(t *testing.T) {
	plugin := testPlugin("dns", "cloudflare", "1.0.0")
	first := PluginDigest(plugin)
	if second := PluginDigest(plugin); first != second {
		t.Fatalf("digest is not stable: %s != %s", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("digest length = %d", len(first))
	}
	plugin.Version = "1.0.1"
	if PluginDigest(plugin) == first {
		t.Fatal("digest should change when the entry changes")
	}
}

func testEnvelope(t *testing.T, index []byte, revision uint64, pub ed25519.PublicKey, priv ed25519.PrivateKey) []byte {
	t.Helper()
	sum := sha256.Sum256(index)
	envelope := SignatureEnvelope{
		Schema: 1, IndexSHA256: hex.EncodeToString(sum[:]), Revision: revision,
		SignedAt: time.Now().UTC().Format(time.RFC3339),
		Signatures: []EnvelopeSignature{{
			Alg: "ed25519", KeyID: PublicKeyFingerprint(pub),
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, index)),
		}},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestVerifyLegacySignatureStartingWithBrace 验证旧式 64 字节签名不会因为
// 首字节恰好是 '{' 而被误判为 JSON 封套。
func TestVerifyLegacySignatureStartingWithBrace(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	for i := 0; i < 10000; i++ {
		raw := []byte(fmt.Sprintf("index-%d", i))
		signature := ed25519.Sign(priv, raw)
		if signature[0] != '{' {
			continue
		}
		if !VerifyIndexSignature(encoded, raw, signature) {
			t.Fatal("valid legacy signature starting with '{' was rejected")
		}
		return
	}
	t.Skip("no legacy signature starting with '{' generated")
}

func TestFetchPrefersSignatureEnvelope(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	index := mustJSON(t, Index{
		Schema: 1, Channel: "stable", Protocol: "v1", Revision: 3,
		Plugins: []Plugin{testPlugin("dns", "cloudflare", "1.0.0")},
	})
	envelope := testEnvelope(t, index, 3, pub, priv)
	legacyHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(index)
		case "/index.json.sig.json":
			_, _ = w.Write(envelope)
		case "/index.json.sig":
			legacyHits++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/index.json", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	result, _, _, _, _, err := client.FetchValidated(context.Background(), "", ParseOptions{Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 3 || result.KeyID != PublicKeyFingerprint(pub) {
		t.Fatalf("envelope metadata = revision %d key %q", result.Revision, result.KeyID)
	}
	if legacyHits != 0 {
		t.Fatalf("legacy signature should not be requested when envelope exists: %d", legacyHits)
	}
}

func TestFetchInvalidEnvelopeDoesNotFallBack(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	index := testIndex(t, testPlugin("dns", "cloudflare", "1.0.0"))
	validSig := ed25519.Sign(priv, index)
	badEnvelope := testEnvelope(t, []byte(`{"schema":1}`), 0, pub, priv)
	legacyHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(index)
		case "/index.json.sig.json":
			_, _ = w.Write(badEnvelope)
		case "/index.json.sig":
			legacyHits++
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(validSig)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/index.json", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := client.Fetch(context.Background(), ""); err == nil {
		t.Fatal("invalid envelope must fail closed")
	}
	if legacyHits != 0 {
		t.Fatalf("invalid envelope must not fall back to legacy signature: %d", legacyHits)
	}
}

func TestFetchSignatureEnvelopeErrorDoesNotFallBack(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	index := testIndex(t, testPlugin("dns", "cloudflare", "1.0.0"))
	legacyHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(index)
		case "/index.json.sig.json":
			http.Error(w, "upstream failure", http.StatusInternalServerError)
		case "/index.json.sig":
			legacyHits++
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, index))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/index.json", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := client.Fetch(context.Background(), ""); err == nil {
		t.Fatal("signature envelope transport error must fail closed")
	}
	if legacyHits != 0 {
		t.Fatalf("signature envelope transport error must not fall back to legacy: %d", legacyHits)
	}
}

func TestFetchSignatureURLKeepsQuery(t *testing.T) {
	index := testIndex(t, testPlugin("dns", "cloudflare", "1.0.0"))
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(priv, index)
	seenQuery := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(index)
		case "/index.json.sig":
			seenQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(signature)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/index.json?token=abc", "stable", base64.StdEncoding.EncodeToString(pub), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := client.Fetch(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if seenQuery != "token=abc" {
		t.Fatalf("signature query = %q, want token=abc", seenQuery)
	}
}
