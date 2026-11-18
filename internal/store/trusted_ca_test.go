package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestTrustedCACRUD(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	items := []*model.TrustedCA{
		{
			Name: "Root", Subject: "CN=Root", Issuer: "CN=Root", Serial: "01",
			Fingerprint: "AAAA", CertPEM: "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n",
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		},
		{
			Name: "Intermediate", Subject: "CN=Intermediate", Issuer: "CN=Root", Serial: "02",
			Fingerprint: "BBBB", CertPEM: "-----BEGIN CERTIFICATE-----\nBB==\n-----END CERTIFICATE-----\n",
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		},
	}
	if err := st.CreateTrustedCAs(items); err != nil {
		t.Fatalf("写入信任链失败: %v", err)
	}
	if items[0].ID == 0 || items[1].ID == 0 {
		t.Fatal("写入后应回填 ID")
	}
	list, err := st.ListTrustedCAs()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应返回 2 张 CA，实际 %d", len(list))
	}
	// 列表按新导入优先排序。
	if list[0].Fingerprint != "BBBB" {
		t.Fatalf("排序不正确: %+v", list)
	}
	got, err := st.GetTrustedCA(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Root" || got.CertPEM == "" {
		t.Fatalf("查询结果不正确: %+v", got)
	}
	if err := st.DeleteTrustedCA(items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTrustedCA(items[0].ID); err == nil {
		t.Fatal("删除后不应再查到 CA")
	}
}
