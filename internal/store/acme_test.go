package store

import (
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-store-key"))
	if err != nil {
		t.Fatal(err)
	}
	return New(conn, cipher)
}

func TestACMEEndpointAndAccountLifecycle(t *testing.T) {
	st := newTestStore(t)
	ep := &model.ACMEEndpoint{
		PublicID:         "endpoint-public",
		Name:             "Internal",
		Slug:             "internal",
		CAID:             1,
		Enabled:          true,
		EABRequired:      true,
		AllowedDomains:   []string{"example.com"},
		HTTP01Enabled:    true,
		DNS01Enabled:     true,
		ValidityDays:     90,
		MaxOrdersPerHour: 60,
	}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetACMEEndpointBySlug("internal")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ep.ID || !got.EABRequired || len(got.AllowedDomains) != 1 {
		t.Fatalf("端点读取不正确: %+v", got)
	}

	credential := &model.ACMEEABCredential{
		PublicID:   "eab-public",
		EndpointID: ep.ID,
		Kid:        "eab_kid",
		Label:      "one",
		HMAC:       "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0",
	}
	if err := st.CreateACMEEABCredential(credential); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetACMEEABCredentialByKid("eab_kid")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HMAC != credential.HMAC || !loaded.HMACSet {
		t.Fatalf("EAB HMAC 读取不正确: %+v", loaded)
	}

	account := &model.ACMEAccount{
		PublicID:   "account-public",
		EndpointID: ep.ID,
		Kid:        "https://example.com/acme/internal/account/account-public",
		PublicJWK:  `{"kty":"EC","crv":"P-256","x":"x","y":"y"}`,
		Thumbprint: "thumb",
		Contact:    []string{"mailto:user@example.com"},
		Status:     "valid",
	}
	if err := st.CreateACMEAccount(account, credential.ID); err != nil {
		t.Fatal(err)
	}
	used, err := st.GetACMEEABCredentialByKid("eab_kid")
	if err != nil {
		t.Fatal(err)
	}
	if used.UsedAt == nil {
		t.Fatal("EAB 凭据未被标记为已使用")
	}
	if _, err := st.GetACMEAccountByThumbprint(ep.ID, "thumb"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteACMEEndpoint(ep.ID); err == nil {
		t.Fatal("存在账户时不应允许删除端点")
	}
}

// createACMETestOrder 创建带授权与挑战的测试订单。
func createACMETestOrder(t *testing.T, st *Store, endpointID, accountID int64, name, status string, expires *time.Time) *model.ACMEOrder {
	t.Helper()
	order := &model.ACMEOrder{
		PublicID:    "order-" + name,
		EndpointID:  endpointID,
		AccountID:   accountID,
		Status:      status,
		Identifiers: []string{"example.com"},
		ExpiresAt:   expires,
	}
	authz := &model.ACMEAuthorization{
		PublicID:       "authz-" + name,
		Identifier:     "example.com",
		IdentifierType: "dns",
		Status:         status,
		ExpiresAt:      expires,
		Challenges: []model.ACMEChallenge{{
			PublicID: "challenge-" + name,
			Type:     "http-01",
			Token:    "token-" + name,
			Status:   status,
		}},
	}
	if err := st.CreateACMEOrder(order, []*model.ACMEAuthorization{authz}); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestCleanupACMEDiagnostics(t *testing.T) {
	st := newTestStore(t)
	ep := &model.ACMEEndpoint{PublicID: "ep", Name: "Internal", Slug: "internal", CAID: 1, Enabled: true, AllowedDomains: []string{"example.com"}}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -200)

	active := &model.ACMEAccount{PublicID: "active", EndpointID: ep.ID, Kid: "kid-active", PublicJWK: "{}", Thumbprint: "active", Status: "valid"}
	stale := &model.ACMEAccount{PublicID: "stale", EndpointID: ep.ID, Kid: "kid-stale", PublicJWK: "{}", Thumbprint: "stale", Status: "valid"}
	orphan := &model.ACMEAccount{PublicID: "orphan", EndpointID: ep.ID, Kid: "kid-orphan", PublicJWK: "{}", Thumbprint: "orphan", Status: "valid"}
	recent := &model.ACMEAccount{PublicID: "recent", EndpointID: ep.ID, Kid: "kid-recent", PublicJWK: "{}", Thumbprint: "recent", Status: "valid"}
	for _, account := range []*model.ACMEAccount{active, stale, orphan, recent} {
		if err := st.CreateACMEAccount(account, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.Exec(`UPDATE acme_accounts SET last_used_at=?, created_at=? WHERE id=?`, old, old, stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE acme_accounts SET created_at=? WHERE id=?`, old, orphan.ID); err != nil {
		t.Fatal(err)
	}

	pending := createACMETestOrder(t, st, ep.ID, active.ID, "pending", "pending", &now)
	invalid := createACMETestOrder(t, st, ep.ID, stale.ID, "invalid", "invalid", &now)
	expired := createACMETestOrder(t, st, ep.ID, stale.ID, "expired", "ready", &old)
	valid := createACMETestOrder(t, st, ep.ID, stale.ID, "valid", "valid", &now)
	recentOrder := createACMETestOrder(t, st, ep.ID, recent.ID, "recent", "valid", &now)
	for _, order := range []*model.ACMEOrder{invalid, expired, valid} {
		if _, err := st.DB.Exec(`UPDATE acme_orders SET created_at=? WHERE id=?`, old, order.ID); err != nil {
			t.Fatal(err)
		}
	}

	filter := ACMECleanupFilter{Orders: true, Accounts: true, Scope: "stale", InactiveDays: 90}
	count, err := st.CountACMECleanup(filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if count.Orders != 3 || count.Accounts != 2 {
		t.Fatalf("清理预览不正确: %+v", count)
	}

	deleted, err := st.CleanupACME(filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Orders != 3 || deleted.Accounts != 2 {
		t.Fatalf("清理数量不正确: %+v", deleted)
	}
	for _, id := range []int64{invalid.ID, expired.ID, valid.ID} {
		if _, err := st.GetACMEOrderByID(id); err == nil {
			t.Fatalf("订单 %d 未被清理", id)
		}
	}
	for _, id := range []int64{pending.ID, recentOrder.ID} {
		if _, err := st.GetACMEOrderByID(id); err != nil {
			t.Fatalf("订单 %d 不应被清理: %v", id, err)
		}
	}
	if _, err := st.GetACMEAccountByID(active.ID); err != nil {
		t.Fatalf("有进行中订单的账户不应被清理: %v", err)
	}
	if _, err := st.GetACMEAccountByID(recent.ID); err != nil {
		t.Fatalf("近期账户不应被清理: %v", err)
	}
	for _, id := range []int64{stale.ID, orphan.ID} {
		if _, err := st.GetACMEAccountByID(id); err == nil {
			t.Fatalf("账户 %d 未被清理", id)
		}
	}
	var authzLeft, challengesLeft int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM acme_authorizations`).Scan(&authzLeft); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM acme_challenges`).Scan(&challengesLeft); err != nil {
		t.Fatal(err)
	}
	if authzLeft != 2 || challengesLeft != 2 {
		t.Fatalf("授权或挑战未级联清理: authz=%d challenges=%d", authzLeft, challengesLeft)
	}

	// 全部范围会连同有效订单和账户一起清理。
	all := ACMECleanupFilter{Orders: true, Accounts: true, Scope: "all"}
	count, err = st.CountACMECleanup(all, now)
	if err != nil {
		t.Fatal(err)
	}
	if count.Orders != 2 || count.Accounts != 2 {
		t.Fatalf("全部范围预览不正确: %+v", count)
	}
	deleted, err = st.CleanupACME(all, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Orders != 2 || deleted.Accounts != 2 {
		t.Fatalf("全部范围清理数量不正确: %+v", deleted)
	}
}

func TestACMEAccountRejectsReusedEAB(t *testing.T) {
	st := newTestStore(t)
	ep := &model.ACMEEndpoint{PublicID: "ep", Name: "Internal", Slug: "internal", CAID: 1, Enabled: true, AllowedDomains: []string{"example.com"}}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	credential := &model.ACMEEABCredential{PublicID: "eab", EndpointID: ep.ID, Kid: "eab_kid", HMAC: "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0"}
	if err := st.CreateACMEEABCredential(credential); err != nil {
		t.Fatal(err)
	}
	first := &model.ACMEAccount{PublicID: "a1", EndpointID: ep.ID, Kid: "kid1", PublicJWK: "{}", Thumbprint: "t1", Status: "valid"}
	if err := st.CreateACMEAccount(first, credential.ID); err != nil {
		t.Fatal(err)
	}
	second := &model.ACMEAccount{PublicID: "a2", EndpointID: ep.ID, Kid: "kid2", PublicJWK: "{}", Thumbprint: "t2", Status: "valid"}
	if err := st.CreateACMEAccount(second, credential.ID); err == nil {
		t.Fatal("重复使用 EAB 应失败")
	}
}
