package pluginproto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadFixture[T any](t *testing.T, name string, target *T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func TestDNSManifestFixture(t *testing.T) {
	var manifest DNSManifest
	loadFixture(t, "dns-manifest.json", &manifest)
	if manifest.Protocol != ProtocolVersion {
		t.Fatalf("protocol = %q", manifest.Protocol)
	}
	if len(manifest.Providers) != 194 {
		t.Fatalf("providers = %d, want 194", len(manifest.Providers))
	}
	provider := manifest.Providers[0]
	if provider.ID == "" || provider.Name == "" {
		t.Fatalf("invalid provider: %+v", provider)
	}
	if len(provider.NameI18n) == 0 || len(provider.Fields) == 0 {
		t.Fatalf("provider %s is missing i18n or fields", provider.ID)
	}
	field := provider.Fields[0]
	if field.Key == "" || field.Label == "" || len(field.LabelI18n) == 0 {
		t.Fatalf("field is missing required legacy or i18n fields: %+v", field)
	}
}

func TestDeployManifestFixture(t *testing.T) {
	var manifest DeployManifest
	loadFixture(t, "deploy-manifest.json", &manifest)
	if manifest.Protocol != ProtocolVersion {
		t.Fatalf("protocol = %q", manifest.Protocol)
	}
	if len(manifest.Deployers) == 0 {
		t.Fatal("deploy manifest has no deployers")
	}
	deployer := manifest.Deployers[0]
	if deployer.ID == "" {
		t.Fatalf("invalid deployer: %+v", deployer)
	}
	if len(deployer.NameI18n) == 0 {
		t.Fatalf("deployer %s is missing name_i18n", deployer.ID)
	}
}

func TestNotifyManifestFixture(t *testing.T) {
	var manifest NotifyManifest
	loadFixture(t, "notify-manifest.json", &manifest)
	if manifest.Protocol != ProtocolVersion {
		t.Fatalf("protocol = %q", manifest.Protocol)
	}
	if len(manifest.Channels) != 23 {
		t.Fatalf("channels = %d, want 23", len(manifest.Channels))
	}
}
