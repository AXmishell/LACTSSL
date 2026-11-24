package dnsplugin

import (
	"testing"

	"github.com/go-acme/lego/v4/challenge"
)

type fakePlugin struct {
	id      string
	aliases []string
	source  Source
}

func (p fakePlugin) ID() string        { return p.id }
func (p fakePlugin) Aliases() []string { return p.aliases }
func (p fakePlugin) Source() Source    { return p.source }
func (p fakePlugin) New(map[string]string) (challenge.Provider, error) {
	return nil, nil
}

func TestRegistryLookupAndAliases(t *testing.T) {
	plugin := fakePlugin{id: "test-provider", aliases: []string{"test-alias"}, source: SourceExec}
	if err := RegisterExternal(plugin); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { UnregisterSource(SourceExec) })

	for _, name := range []string{"test-provider", "TEST-ALIAS", " test-provider "} {
		found, ok := Lookup(name)
		if !ok || found.ID() != "test-provider" {
			t.Fatalf("Lookup(%q) = %v, %v", name, found, ok)
		}
	}
	if Has("missing-provider") {
		t.Fatal("unexpected lookup hit")
	}
}

func TestRegistryDuplicate(t *testing.T) {
	plugin := fakePlugin{id: "duplicate-provider", source: SourceExec}
	if err := RegisterExternal(plugin); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if err := RegisterExternal(plugin); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := RegisterExternal(fakePlugin{id: "alias-holder", aliases: []string{"duplicate-provider"}, source: SourceExec}); err == nil {
		t.Fatal("expected alias conflict")
	}
}

func TestUnregisterSource(t *testing.T) {
	if err := RegisterExternal(fakePlugin{id: "ephemeral", source: SourceExec}); err != nil {
		t.Fatalf("register: %v", err)
	}
	UnregisterSource(SourceExec)
	if Has("ephemeral") {
		t.Fatal("plugin should be removed")
	}
}

// TestRegistryRejectsIDShadowingAlias 验证新插件 ID 不能覆盖已有插件别名，
// 否则同名 DNS 凭据会被错误 helper 接收。
func TestRegistryRejectsIDShadowingAlias(t *testing.T) {
	UnregisterSource(SourceExec)
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	first := fakePlugin{id: "first-provider", aliases: []string{"shared-name"}, source: SourceExec}
	if err := RegisterExternal(first); err != nil {
		t.Fatalf("register first: %v", err)
	}
	second := fakePlugin{id: "shared-name", source: SourceExec}
	if err := RegisterExternal(second); err == nil {
		t.Fatal("new plugin ID must not shadow an existing alias")
	}
	found, ok := Lookup("shared-name")
	if !ok || found.ID() != "first-provider" {
		t.Fatalf("alias should still resolve to the first plugin: %v %v", found, ok)
	}
}

func TestReplaceBuiltinIsAtomic(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	external := fakePlugin{id: "external-provider", aliases: []string{"external-alias"}, source: SourceExec}
	if err := RegisterExternal(external); err != nil {
		t.Fatalf("register external: %v", err)
	}
	first := fakePlugin{id: "builtin-one", aliases: []string{"builtin-alias"}, source: SourceBuiltin}
	if err := ReplaceBuiltin([]Plugin{first}); err != nil {
		t.Fatalf("replace first: %v", err)
	}
	if !Has("builtin-one") || !Has("builtin-alias") {
		t.Fatal("first builtin set should be registered")
	}

	// 替换后旧内置 ID 与别名必须消失，外部插件保持不变。
	if err := ReplaceBuiltin([]Plugin{fakePlugin{id: "builtin-two", source: SourceBuiltin}}); err != nil {
		t.Fatalf("replace second: %v", err)
	}
	if Has("builtin-one") || Has("builtin-alias") {
		t.Fatal("previous builtin entries should be removed")
	}
	for _, name := range []string{"builtin-two", "external-provider", "external-alias"} {
		if !Has(name) {
			t.Fatalf("%s should be registered", name)
		}
	}

	// 新集合内部冲突时必须整体失败，保留上一版内置集合。
	conflicting := []Plugin{
		fakePlugin{id: "conflict-a", source: SourceBuiltin},
		fakePlugin{id: "conflict-b", aliases: []string{"conflict-a"}, source: SourceBuiltin},
	}
	if err := ReplaceBuiltin(conflicting); err == nil {
		t.Fatal("conflicting builtin set should fail")
	}
	if !Has("builtin-two") || Has("conflict-a") || Has("conflict-b") {
		t.Fatal("failed replacement must keep the previous registry")
	}

	// 来源不匹配的条目不能被当作内置插件替换。
	if err := ReplaceBuiltin([]Plugin{external}); err == nil {
		t.Fatal("external plugin must not be replaceable as builtin")
	}
}
