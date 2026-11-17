package store

import (
	"testing"

	"lactssl/internal/model"
)

func TestPluginFileMetaLifecycle(t *testing.T) {
	st := newTestStore(t)
	item := model.PluginFileMeta{
		Kind:     "dns",
		Filename: "lactssl-dns-core",
		Name:     "DNS Core",
		Version:  "1.0.0",
		SHA256:   "abc",
		Entries: []model.PluginFileEntry{
			{ID: "cloudflare", Name: "Cloudflare"},
			{ID: "route53", Name: "Route 53"},
		},
	}
	if err := st.UpsertPluginFileMeta(item); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetPluginFileMeta("dns", item.Filename)
	if err != nil || !found {
		t.Fatalf("get meta = %+v, %v, %v", got, found, err)
	}
	if got.Version != item.Version || len(got.Entries) != 2 || got.Entries[1].Name != "Route 53" {
		t.Fatalf("meta = %+v", got)
	}

	item.Version = "1.0.1"
	item.Entries = item.Entries[:1]
	if err := st.UpsertPluginFileMeta(item); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.GetPluginFileMeta("dns", item.Filename)
	if err != nil || got.Version != "1.0.1" || len(got.Entries) != 1 {
		t.Fatalf("updated meta = %+v, %v", got, err)
	}

	list, err := st.ListPluginFileMeta("dns")
	if err != nil || len(list) != 1 || list[0].Filename != item.Filename {
		t.Fatalf("list meta = %+v, %v", list, err)
	}
	if err := st.DeletePluginFileMeta("dns", item.Filename); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.GetPluginFileMeta("dns", item.Filename); err != nil || found {
		t.Fatalf("deleted meta found=%v err=%v", found, err)
	}
}
