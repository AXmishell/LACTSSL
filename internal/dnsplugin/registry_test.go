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
