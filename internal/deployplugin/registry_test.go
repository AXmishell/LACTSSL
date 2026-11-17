package deployplugin

import (
	"context"
	"testing"
)

type fakeDeployer struct {
	id      string
	aliases []string
	source  Source
}

func (p fakeDeployer) ID() string                            { return p.id }
func (p fakeDeployer) Aliases() []string                     { return p.aliases }
func (p fakeDeployer) Source() Source                        { return p.source }
func (p fakeDeployer) Deploy(context.Context, Request) error { return nil }

func TestRegistryLookupAndAliases(t *testing.T) {
	deployer := fakeDeployer{id: "test-target", aliases: []string{"test-alias"}, source: SourceExec}
	if err := RegisterExternal(deployer); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { UnregisterSource(SourceExec) })

	for _, name := range []string{"test-target", "TEST-ALIAS", " test-target "} {
		found, ok := Lookup(name)
		if !ok || found.ID() != "test-target" {
			t.Fatalf("Lookup(%q) = %v, %v", name, found, ok)
		}
	}
	if Has("missing-target") {
		t.Fatal("unexpected lookup hit")
	}
}

func TestRegistryDuplicate(t *testing.T) {
	deployer := fakeDeployer{id: "duplicate-target", source: SourceExec}
	if err := RegisterExternal(deployer); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { UnregisterSource(SourceExec) })
	if err := RegisterExternal(deployer); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := RegisterExternal(fakeDeployer{id: "alias-holder", aliases: []string{"duplicate-target"}, source: SourceExec}); err == nil {
		t.Fatal("expected alias conflict")
	}
	if err := RegisterExternal(fakeDeployer{id: "Invalid ID", source: SourceExec}); err == nil {
		t.Fatal("expected invalid id error")
	}

	// 别名与后续注册的 ID 也不能冲突。
	if err := RegisterExternal(fakeDeployer{id: "alias-source", aliases: []string{"taken-id"}, source: SourceExec}); err != nil {
		t.Fatalf("register alias source: %v", err)
	}
	if err := RegisterExternal(fakeDeployer{id: "taken-id", source: SourceExec}); err == nil {
		t.Fatal("expected id/alias conflict")
	}
}

func TestReplaceExternal(t *testing.T) {
	first := fakeDeployer{id: "first-target", source: SourceExec}
	if err := RegisterExternal(first); err != nil {
		t.Fatalf("register: %v", err)
	}
	loaded, errs := replaceExternal([]Deployer{fakeDeployer{id: "second-target", source: SourceExec}})
	if len(errs) > 0 {
		t.Fatalf("replace: %v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d", len(loaded))
	}
	if Has("first-target") {
		t.Fatal("old deployer should be removed")
	}
	if !Has("second-target") {
		t.Fatal("new deployer should be registered")
	}
	UnregisterSource(SourceExec)
}
