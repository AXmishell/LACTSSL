package notifyplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testManifest = `{"protocol":"v1","name":"test notify helper","version":"1.0.0","channels":[{"id":"fakepush","name":"Fake Push","aliases":["fakep"],"fields":[{"key":"token","label":"Token","type":"password","secret":true,"required":true},{"key":"url","label":"URL","type":"text","required":true}]}]}`

func writeHelper(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return path
}

func resetExternalState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		UnregisterSource(SourceExec)
		externalState.Lock()
		externalState.channels = make(map[string]ExternalChannel)
		externalState.helpers = 0
		externalState.Unlock()
	})
}

func TestDiscoverAndSend(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "request.json")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
manifest)
  cat <<'JSON'
%s
JSON
  ;;
send)
  cat > %q
  printf '{"ok":true}'
  ;;
*)
  exit 2
  ;;
esac
`, testManifest, output)
	writeHelper(t, dir, "fake-helper", script, 0o700)

	result := Discover(DiscoverConfig{Dir: dir, MemoryLimitMB: 128, Timeout: 2 * time.Second, EnabledChannels: []string{"fakepush"}})
	if result.Helpers != 1 || result.Channels != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	plugin, ok := Lookup("fakep")
	if !ok || plugin.Definition().ID != "fakepush" || plugin.Source() != SourceExec {
		t.Fatalf("lookup failed: %v %v", plugin, ok)
	}
	if fields := SecretFields("fakepush"); len(fields) != 1 || fields[0] != "token" {
		t.Fatalf("unexpected secret fields: %v", fields)
	}
	if err := Send("fakepush", map[string]any{"url": "https://push.example.com", "token": "secret-token"}, Message{Title: "title", Body: "body"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	for _, want := range []string{`"action":"send"`, `"channel":"fakepush"`, `"token":"secret-token"`, `"title":"title"`, `"body":"body"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("request missing %s: %s", want, data)
		}
	}
}

func TestDiscoverSkipsDisabledAndReserved(t *testing.T) {
	resetExternalState(t)
	dir := t.TempDir()
	writeHelper(t, dir, "fake-helper", fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nmanifest) printf '%%s' %q ;;\n*) exit 2 ;;\nesac\n", testManifest), 0o700)

	result := Discover(DiscoverConfig{
		Dir:             dir,
		Timeout:         2 * time.Second,
		EnabledChannels: nil,
		KnownIDs:        map[string]bool{"mail": true},
	})
	if result.Channels != 0 || len(result.Disabled) != 1 || result.Disabled[0] != "fakepush" {
		t.Fatalf("unexpected disabled result: %+v", result)
	}
	if Has("fakepush") {
		t.Fatal("disabled channel should not be registered")
	}
	if _, found := ExternalChannelInfo("fakepush"); !found {
		t.Fatal("disabled channel metadata should remain available")
	}
}
