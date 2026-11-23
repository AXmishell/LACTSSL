package notifyplugin

import "testing"

type fakeNotifier struct {
	def    Definition
	source Source
}

func (n fakeNotifier) Definition() Definition               { return n.def }
func (n fakeNotifier) Source() Source                       { return n.source }
func (n fakeNotifier) Notify(map[string]any, Message) error { return nil }

func TestReplaceBuiltinIsAtomic(t *testing.T) {
	UnregisterSource(SourceBuiltin)
	UnregisterSource(SourceExec)
	t.Cleanup(func() {
		UnregisterSource(SourceBuiltin)
		UnregisterSource(SourceExec)
	})

	external := fakeNotifier{
		def:    Definition{ID: "external-channel", Aliases: []string{"external-alias"}},
		source: SourceExec,
	}
	if err := RegisterExternal(external); err != nil {
		t.Fatalf("register external: %v", err)
	}
	first := fakeNotifier{def: Definition{ID: "builtin-one", Aliases: []string{"builtin-alias"}}, source: SourceBuiltin}
	if err := ReplaceBuiltin([]Notifier{first}); err != nil {
		t.Fatalf("replace first: %v", err)
	}
	if !Has("builtin-alias") {
		t.Fatal("builtin alias should resolve")
	}
	if err := ReplaceBuiltin([]Notifier{fakeNotifier{def: Definition{ID: "builtin-two"}, source: SourceBuiltin}}); err != nil {
		t.Fatalf("replace second: %v", err)
	}
	if Has("builtin-one") || Has("builtin-alias") {
		t.Fatal("previous builtin entries should be removed")
	}
	for _, name := range []string{"builtin-two", "external-channel", "external-alias"} {
		if !Has(name) {
			t.Fatalf("%s should be registered", name)
		}
	}

	conflicting := []Notifier{
		fakeNotifier{def: Definition{ID: "conflict-a"}, source: SourceBuiltin},
		fakeNotifier{def: Definition{ID: "conflict-b", Aliases: []string{"conflict-a"}}, source: SourceBuiltin},
	}
	if err := ReplaceBuiltin(conflicting); err == nil {
		t.Fatal("conflicting builtin set should fail")
	}
	if !Has("builtin-two") || Has("conflict-a") || Has("conflict-b") {
		t.Fatal("failed replacement must keep the previous registry")
	}
	if err := ReplaceBuiltin([]Notifier{external}); err == nil {
		t.Fatal("external channel must not be replaceable as builtin")
	}
}
