package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	notify "lactssl/internal/plugin/notify"
)

func TestRegistryIDs(t *testing.T) {
	ids := IDs()
	if len(ids) != 23 {
		t.Fatalf("channels = %d, want 23", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate channel %q", id)
		}
		seen[id] = true
	}
}

func TestRegisterByIDAndAll(t *testing.T) {
	notify.ResetNativeForTest()
	defer notify.ResetNativeForTest()

	if !RegisterByID("telegram") {
		t.Fatal("telegram should be registered")
	}
	if got := len(notify.ListNative()); got != 1 {
		t.Fatalf("registered = %d, want 1", got)
	}

	notify.ResetNativeForTest()
	RegisterAll()
	if got := len(notify.ListNative()); got != 23 {
		t.Fatalf("registered = %d, want 23", got)
	}
}

func TestCheckNotifyResponse(t *testing.T) {
	ok := []string{
		``,
		`{}`,
		`{"errcode":0,"errmsg":"ok"}`,
		`{"code":0}`,
		`{"code":1}`,
		`{"code":200}`,
		`{"code":1000,"data":[]}`,
		`{"retcode":0,"status":"ok"}`,
		`{"ok":true}`,
		`{"success":true}`,
		`ok`,
	}
	for _, body := range ok {
		if err := checkNotifyResponse([]byte(body), true); err != nil {
			t.Fatalf("expected success for %q: %v", body, err)
		}
	}
	bad := []string{
		`{"errcode":310000,"errmsg":"invalid token"}`,
		`{"code":19021,"msg":"sign mismatch"}`,
		`{"retcode":100,"wording":"failed"}`,
		`{"ok":false,"description":"chat not found"}`,
		`{"success":false}`,
		`{"status":"error"}`,
		`{"error":"boom"}`,
	}
	for _, body := range bad {
		if err := checkNotifyResponse([]byte(body), true); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
	if err := checkNotifyResponse([]byte("<html>maintenance</html>"), true); err == nil {
		t.Fatal("non-JSON response must fail for JSON endpoints")
	}
	if err := checkNotifyResponse([]byte("<html>maintenance</html>"), false); err != nil {
		t.Fatalf("plain form response should be accepted: %v", err)
	}
}

func TestNotifyJSONRejectsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"invalid token"}`))
	}))
	defer server.Close()
	err := notifyJSON(server.URL, map[string]any{"text": "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("expected business error, got %v", err)
	}
}

func TestNotifyFormRejectsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"push failed"}`))
	}))
	defer server.Close()
	err := notifyForm(server.URL, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "push failed") {
		t.Fatalf("expected business error, got %v", err)
	}
}

func TestSendDingTalkBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"keywords not in content"}`))
	}))
	defer server.Close()
	err := sendDingTalk(map[string]any{"webhook": server.URL}, notify.NativeMessage{Title: "t", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "keywords") {
		t.Fatalf("expected dingtalk business error, got %v", err)
	}
}

func TestEscapeMarkdown(t *testing.T) {
	got := escapeMarkdown("[click](https://evil) @all *bold*")
	for _, token := range []string{`\[`, `\]`, `\(`, `\)`, `\@`, `\*`} {
		if !strings.Contains(got, token) {
			t.Fatalf("escaped markdown missing %q: %s", token, got)
		}
	}
}

func TestCustomChannelRejectsInvalidHeaders(t *testing.T) {
	err := sendCustom(map[string]any{
		"url":     "http://127.0.0.1:1/hook",
		"headers": `{"Authorization": 1}`,
	}, notify.NativeMessage{Title: "t", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "headers") {
		t.Fatalf("expected headers error, got %v", err)
	}
}

func TestCustomChannelPlainTextIsNotJSONEscaped(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
	}))
	defer server.Close()
	err := sendCustom(map[string]any{
		"url":     server.URL,
		"headers": `{"Content-Type":"text/plain"}`,
		"body":    `{title}: {content}`,
	}, notify.NativeMessage{Title: `a"b`, Body: "line1\nline2"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\"b: line1\nline2" {
		t.Fatalf("body was unexpectedly JSON-escaped: %q", got)
	}
}

func TestNotifyHTTPValidationErrorRedactsToken(t *testing.T) {
	err := notifyHTTP(http.MethodGet, "http://192.0.2.1/path?token=SECRET", nil, "", nil)
	if err == nil {
		t.Fatal("public HTTP should be rejected")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("validation error leaked token: %s", err)
	}
}

func TestServerChanInvalidJSONIsError(t *testing.T) {
	if err := checkNotifyResponse([]byte(`<html>maintenance</html>`), true); err == nil {
		t.Fatal("invalid JSON should fail")
	}
}

func TestEscapeJSONStringPreservesTrailingQuote(t *testing.T) {
	for _, value := range []string{`a"`, `""`, `x\`, "正常"} {
		escaped := escapeJSONString(value)
		rendered := `{"v":"` + escaped + `"}`
		var decoded map[string]string
		if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
			t.Fatalf("escapeJSONString(%q) produced invalid JSON %q: %v", value, rendered, err)
		}
		if decoded["v"] != value {
			t.Fatalf("round trip %q -> %q", value, decoded["v"])
		}
	}
}
