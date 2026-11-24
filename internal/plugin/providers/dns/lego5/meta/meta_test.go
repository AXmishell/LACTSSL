package meta

import "testing"

func TestAllV5ProvidersHaveMetadata(t *testing.T) {
	ids := IDs()
	if len(ids) != 235 {
		t.Fatalf("ids = %d, want 235", len(ids))
	}
	for _, id := range ids {
		def, ok := LookupDNS(id)
		if !ok {
			t.Fatalf("missing metadata for %s", id)
		}
		if len(def.Fields) == 0 {
			t.Fatalf("provider %s has no fields", id)
		}
		for _, field := range def.Fields {
			if field.Key == "" || field.Env == "" {
				t.Fatalf("provider %s has invalid field %+v", id, field)
			}
		}
	}
}
