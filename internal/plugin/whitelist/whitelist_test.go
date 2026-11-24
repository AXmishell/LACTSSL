package whitelist

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
)

type fakeSettings struct {
	values  map[string]string
	setErr  error
	getErr  error
	deleted []string
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: map[string]string{}}
}

func (f *fakeSettings) GetSetting(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.values[key], nil
}

func (f *fakeSettings) SetSetting(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	return nil
}

func (f *fakeSettings) DeleteSetting(key string) error {
	f.deleted = append(f.deleted, key)
	delete(f.values, key)
	return nil
}

func newTestService(st *fakeSettings) *Service {
	return New(Spec{
		Kind:       "dns",
		SettingKey: "plugins.dns.enabled",
		LegacyKeys: []string{"plugins.dns.legacy"},
		Defaults:   func() map[string]bool { return map[string]bool{"default": true} },
		Valid: func(id string) bool {
			return id == "a" || id == "b" || id == "a-b"
		},
		Known: func() map[string]bool {
			return map[string]bool{"a": true, "a-b": true}
		},
		Retain: func(id string) bool { return id == "default" },
	}, st)
}

func TestEnabledSetDefaultsOnMissingOrInvalid(t *testing.T) {
	st := newFakeSettings()
	svc := newTestService(st)
	if got := svc.EnabledSet(); !reflect.DeepEqual(got, map[string]bool{"default": true}) {
		t.Fatalf("missing setting should use defaults: %#v", got)
	}
	st.values["plugins.dns.enabled"] = "not-json"
	if got := svc.EnabledSet(); !reflect.DeepEqual(got, map[string]bool{"default": true}) {
		t.Fatalf("invalid setting should use defaults: %#v", got)
	}
}

func TestEnabledSetNormalizesAndAfterLoad(t *testing.T) {
	st := newFakeSettings()
	st.values["plugins.dns.enabled"] = `[" A ", "b", "a"]`
	svc := New(Spec{
		Kind:       "dns",
		SettingKey: "plugins.dns.enabled",
		Defaults:   func() map[string]bool { return nil },
		AfterLoad: func(set map[string]bool) {
			if set["a"] {
				set["a-b"] = true
			}
		},
	}, st)
	got := svc.EnabledSet()
	want := map[string]bool{"a": true, "b": true, "a-b": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestUpdateSkipsUnknownAndPersists(t *testing.T) {
	st := newFakeSettings()
	svc := newTestService(st)
	updated, skipped, err := svc.Update([]string{" A ", "missing", "a"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, []string{"a", "a"}) {
		t.Fatalf("updated = %#v", updated)
	}
	if !reflect.DeepEqual(skipped, []string{"missing"}) {
		t.Fatalf("skipped = %#v", skipped)
	}
	if got := st.values["plugins.dns.enabled"]; got != `["a","default"]` {
		t.Fatalf("setting = %s", got)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "plugins.dns.legacy" {
		t.Fatalf("legacy key not deleted: %#v", st.deleted)
	}
}

func TestUpdateDisableRunsAfterDisable(t *testing.T) {
	st := newFakeSettings()
	st.values["plugins.dns.enabled"] = `["a","a-b"]`
	svc := New(Spec{
		Kind:       "deploy",
		SettingKey: "plugins.dns.enabled",
		Defaults:   func() map[string]bool { return nil },
		Valid:      func(id string) bool { return id == "a-b" },
		Known:      func() map[string]bool { return map[string]bool{"a": true, "a-b": true} },
		AfterDisable: func(set map[string]bool, id string) {
			delete(set, "a")
		},
	}, st)
	if _, _, err := svc.Update([]string{"a-b"}, false); err != nil {
		t.Fatal(err)
	}
	if got := st.values["plugins.dns.enabled"]; got != `[]` {
		t.Fatalf("setting = %s", got)
	}
}

func TestSaveDropsStaleButRetainsDefaultAndKnown(t *testing.T) {
	st := newFakeSettings()
	svc := newTestService(st)
	err := svc.Save(map[string]bool{
		"a":       true,
		"a-b":     true,
		"default": true,
		"stale":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := jsonUnmarshal(st.values["plugins.dns.enabled"], &ids); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	want := []string{"a", "a-b", "default"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %#v, want %#v", ids, want)
	}
}

func TestSaveUsesLookupKeyButKeepsOriginalID(t *testing.T) {
	st := newFakeSettings()
	svc := New(Spec{
		Kind:       "deploy",
		SettingKey: "plugins.deploy.enabled",
		Defaults:   func() map[string]bool { return nil },
		Normalize:  normalizeID,
		LookupKey:  func(id string) string { return "canonical" },
		Known:      func() map[string]bool { return map[string]bool{"canonical": true} },
	}, st)
	if err := svc.Save(map[string]bool{"legacy": true}); err != nil {
		t.Fatal(err)
	}
	if got := st.values["plugins.deploy.enabled"]; got != `["legacy"]` {
		t.Fatalf("setting = %s", got)
	}
}

func TestSaveErrorDoesNotDeleteLegacyKeys(t *testing.T) {
	st := newFakeSettings()
	svc := newTestService(st)
	st.setErr = errors.New("boom")
	if err := svc.Save(map[string]bool{"a": true}); err == nil {
		t.Fatal("expected error")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("legacy keys should not be deleted on save failure: %#v", st.deleted)
	}
}

func TestEnabledReturnsSorted(t *testing.T) {
	st := newFakeSettings()
	st.values["plugins.dns.enabled"] = `["b","a"]`
	svc := New(Spec{
		Kind:       "dns",
		SettingKey: "plugins.dns.enabled",
		Defaults:   func() map[string]bool { return nil },
	}, st)
	if got := svc.Enabled(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("enabled = %#v", got)
	}
}

func jsonUnmarshal(value string, out *[]string) error {
	return json.Unmarshal([]byte(value), out)
}
