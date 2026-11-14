package store

import (
	"path/filepath"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func newPasskeyTestStore(t *testing.T) (*Store, *model.User) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "passkey.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)
	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	return st, user
}

func TestPasskeyLifecycle(t *testing.T) {
	st, user := newPasskeyTestStore(t)
	item := &model.Passkey{
		UserID:       user.ID,
		Name:         "Laptop",
		CredentialID: "credential-1",
		Credential:   `{"id":"credential-1"}`,
	}
	if err := st.CreatePasskey(item); err != nil {
		t.Fatal(err)
	}
	if item.ID == 0 {
		t.Fatal("创建通行密钥后应返回 ID")
	}
	items, err := st.ListPasskeys(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Laptop" {
		t.Fatalf("通行密钥列表不正确: %+v", items)
	}
	loaded, err := st.GetPasskeyByCredentialID("credential-1")
	if err != nil || loaded.UserID != user.ID {
		t.Fatalf("按凭据 ID 查询失败: %v %+v", err, loaded)
	}
	if err := st.UpdatePasskeyUsage(item.ID, `{"id":"credential-1","counter":2}`); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.GetPasskeyByCredentialID("credential-1")
	if err != nil || loaded.Credential == "" || loaded.LastUsedAt == nil {
		t.Fatalf("更新通行密钥使用记录失败: %v %+v", err, loaded)
	}
	deleted, err := st.DeletePasskey(user.ID, item.ID)
	if err != nil || !deleted {
		t.Fatalf("删除通行密钥失败: %v deleted=%v", err, deleted)
	}
	count, err := st.CountPasskeys(user.ID)
	if err != nil || count != 0 {
		t.Fatalf("删除后数量应为 0: %v count=%d", err, count)
	}
}

func TestResetPasswordDeletesPasskeys(t *testing.T) {
	st, user := newPasskeyTestStore(t)
	if err := st.CreatePasskey(&model.Passkey{
		UserID:       user.ID,
		Name:         "Phone",
		CredentialID: "credential-2",
		Credential:   `{"id":"credential-2"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetPassword(user.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	count, err := st.CountPasskeys(user.ID)
	if err != nil || count != 0 {
		t.Fatalf("密码重置后应删除全部通行密钥: %v count=%d", err, count)
	}
}
