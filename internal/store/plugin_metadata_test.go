package store

import (
	"path/filepath"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func TestPluginMetadataLifecycle(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	item := model.PluginMetadata{
		Kind:     "dns",
		ID:       "cloudflare",
		Version:  "1.0.0",
		Name:     "Cloudflare",
		Manifest: `{"id":"cloudflare","fields":[{"key":"token","secret":true}]}`,
	}
	if err := st.UpsertPluginMetadata(item); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetPluginMetadata("dns", "cloudflare")
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if got.Manifest != item.Manifest || got.Version != item.Version {
		t.Fatalf("unexpected metadata: %+v", got)
	}

	item.Name = "Cloudflare DNS"
	if err := st.UpsertPluginMetadata(item); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListPluginMetadata("dns")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Cloudflare DNS" {
		t.Fatalf("list = %+v", list)
	}

	if err := st.DeletePluginMetadata("dns", "cloudflare"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.GetPluginMetadata("dns", "cloudflare"); err != nil || found {
		t.Fatalf("metadata should be deleted: found=%v err=%v", found, err)
	}
}
