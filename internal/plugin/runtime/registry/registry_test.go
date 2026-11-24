package registry

import "testing"

type item struct {
	id      string
	aliases []string
	source  Source
}

func (i item) ID() string        { return i.id }
func (i item) Aliases() []string { return i.aliases }
func (i item) Source() Source    { return i.source }

func newRegistry() *Registry[item] {
	return New(Adapter[item]{
		ID:      func(i item) string { return i.id },
		Aliases: func(i item) []string { return i.aliases },
		Source:  func(i item) Source { return i.source },
	})
}

func TestRegisterLookupAndAlias(t *testing.T) {
	r := newRegistry()
	p := item{id: "alpha", aliases: []string{"a"}, source: Exec}
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "A", " alpha "} {
		got, ok := r.Lookup(name)
		if !ok || got.id != "alpha" {
			t.Fatalf("Lookup(%q) = %+v, %v", name, got, ok)
		}
	}
	if err := r.Register(item{id: "a", source: Exec}); err == nil {
		t.Fatal("id must not shadow an existing alias")
	}
}

func TestReplaceBuiltinAtomic(t *testing.T) {
	r := newRegistry()
	external := item{id: "external", aliases: []string{"ext"}, source: Exec}
	if err := r.Register(external); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceBuiltin([]item{{id: "one", source: Builtin}}); err != nil {
		t.Fatal(err)
	}
	conflicting := []item{
		{id: "conflict", source: Builtin},
		{id: "other", aliases: []string{"conflict"}, source: Builtin},
	}
	if err := r.ReplaceBuiltin(conflicting); err == nil {
		t.Fatal("conflicting set should fail")
	}
	if !r.Has("one") || r.Has("conflict") || !r.Has("external") {
		t.Fatal("failed replace must keep previous builtin entries and external entries")
	}
	if err := r.ReplaceBuiltin([]item{external}); err == nil {
		t.Fatal("source mismatch must fail")
	}
}
