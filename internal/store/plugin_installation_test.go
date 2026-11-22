package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestPluginInstallationAuditFields(t *testing.T) {
	st := newTestStore(t)
	item := model.PluginInstallation{
		Kind: "dns", ID: "cloudflare", Version: "1.0.0", Filename: "lactssl-dns-cloudflare",
		SHA256: "binary", AssetSHA256: "asset", Source: "repo", RepoURL: "https://example.invalid/index.json",
		IndexRevision: 7, IndexDigest: "index-digest", EntryDigest: "entry-digest",
	}
	if err := st.UpsertPluginInstallation(item); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetPluginInstallation("dns", "cloudflare")
	if err != nil || !found {
		t.Fatalf("get installation = %+v, %v, %v", got, found, err)
	}
	if got.IndexRevision != 7 || got.IndexDigest != "index-digest" || got.EntryDigest != "entry-digest" {
		t.Fatalf("audit fields = %+v", got)
	}
	if got.InstalledAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = %+v", got)
	}

	item.IndexRevision = 8
	item.IndexDigest = "index-digest-2"
	item.EntryDigest = "entry-digest-2"
	if err := st.UpsertPluginInstallation(item); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.GetPluginInstallation("dns", "cloudflare")
	if err != nil || got.IndexRevision != 8 || got.EntryDigest != "entry-digest-2" {
		t.Fatalf("updated audit fields = %+v, %v", got, err)
	}
	if got.InstalledAt.IsZero() || time.Since(got.UpdatedAt) > time.Minute {
		t.Fatalf("timestamps after update = %+v", got)
	}

	list, err := st.ListPluginInstallations()
	if err != nil || len(list) != 1 || list[0].IndexDigest != "index-digest-2" {
		t.Fatalf("list installations = %+v, %v", list, err)
	}
}
