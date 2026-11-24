package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dnsplugin "lactssl/internal/plugin/host/dns"
)

func putBatchToggle(t *testing.T, r interface {
	ServeHTTP(w http.ResponseWriter, req *http.Request)
}, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestBatchEnableAllBuiltinDNSWithoutIDs(t *testing.T) {
	s, r, _ := newPluginTestServer(t)
	s.BuiltinPluginsEnabled = true
	s.DiscoverPlugins()
	if dnsplugin.Has("cloudflare") {
		t.Fatal("cloudflare should start disabled")
	}
	// 模拟前端“全部启用”按钮：请求体不带 ids。
	rec := putBatchToggle(t, r, "/entry/api/plugins/providers", `{"enabled":true}`)
	if rec.Code != 200 {
		t.Fatalf("enable all status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !dnsplugin.Has("cloudflare") || !dnsplugin.Has("abion") {
		t.Fatal("all builtin providers should be enabled by bulk enable")
	}
	if len(dnsplugin.BuiltinProviders()) < 200 {
		t.Fatalf("unexpected metadata count: %d", len(dnsplugin.BuiltinProviders()))
	}
}
