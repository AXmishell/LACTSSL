package manager

import (
	"errors"
	"reflect"
	"testing"

	"lactssl/internal/model"
)

type fakeStore struct {
	settings      map[string]string
	installations []model.PluginInstallation
	fileMeta      map[string][]model.PluginFileMeta
	getErr        error
}

func (f *fakeStore) ListPluginInstallations() ([]model.PluginInstallation, error) {
	return f.installations, nil
}

func (f *fakeStore) ListPluginFileMeta(kind string) ([]model.PluginFileMeta, error) {
	return f.fileMeta[kind], nil
}

func (f *fakeStore) GetSetting(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.settings[key], nil
}

func TestDisabledHelpers(t *testing.T) {
	store := &fakeStore{settings: map[string]string{
		SettingDNSDisabledHelpers: `[" b ", "A", "a", ""]`,
	}}
	m := New(Options{}, store)
	if got := m.DisabledHelpers(KindDNS); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("disabled helpers = %#v", got)
	}
	store.getErr = errors.New("boom")
	if got := m.DisabledHelpers(KindDNS); got != nil {
		t.Fatalf("read error should return nil, got %#v", got)
	}
	if got := m.DisabledHelpers("unknown"); got != nil {
		t.Fatalf("unknown kind should return nil, got %#v", got)
	}
}

func TestExpectedPluginDigestsPrefersInstallation(t *testing.T) {
	store := &fakeStore{
		installations: []model.PluginInstallation{
			{Kind: KindDNS, Filename: "same", SHA256: "install"},
			{Kind: KindDeploy, Filename: "other", SHA256: "deploy"},
		},
		fileMeta: map[string][]model.PluginFileMeta{
			KindDNS: {
				{Filename: "same", SHA256: "meta-should-lose"},
				{Filename: "only-meta", SHA256: "meta"},
				{Filename: "empty", SHA256: ""},
			},
		},
	}
	m := New(Options{}, store)
	got := m.expectedPluginDigests(KindDNS)
	want := map[string]string{"same": "install", "only-meta": "meta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digests = %#v, want %#v", got, want)
	}
}

func TestDisabledHelpersSetting(t *testing.T) {
	cases := map[string]string{
		KindDNS:    SettingDNSDisabledHelpers,
		KindDeploy: SettingDeployDisabledHelpers,
		KindNotify: SettingNotifyDisabledHelpers,
		"other":    "",
	}
	for kind, want := range cases {
		if got := DisabledHelpersSetting(kind); got != want {
			t.Fatalf("%s setting = %q, want %q", kind, got, want)
		}
	}
}
