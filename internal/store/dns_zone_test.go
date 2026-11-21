package store

import (
	"errors"
	"testing"

	"lactssl/internal/model"
)

func TestDNSZoneBindingsReplaceAndList(t *testing.T) {
	st := newTestStore(t)
	account := &model.DNSAccount{Name: "dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	bindings := []model.DNSZoneBinding{
		{DNSAccountID: account.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
		{DNSAccountID: account.ID, Zone: "exact.net", MatchMode: "exact", Priority: 5, Enabled: true},
		{DNSAccountID: account.ID, Zone: "disabled.org", MatchMode: "suffix", Enabled: false},
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, bindings); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListDNSZoneBindingsByAccount(account.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("unexpected list: %+v %v", list, err)
	}
	enabled, err := st.ListEnabledDNSZoneBindings()
	if err != nil || len(enabled) != 2 {
		t.Fatalf("unexpected enabled list: %+v %v", enabled, err)
	}

	// 替换为更少的绑定后旧记录应消失。
	if err := st.ReplaceDNSZoneBindings(account.ID, bindings[:1]); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListDNSZoneBindingsByAccount(account.ID)
	if len(list) != 1 || list[0].Zone != "example.com" {
		t.Fatalf("replace did not remove old bindings: %+v", list)
	}
}

func TestDNSZoneBindingsRejectCrossAccountConflict(t *testing.T) {
	st := newTestStore(t)
	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(first); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDNSAccount(second); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(first.ID, []model.DNSZoneBinding{
		{DNSAccountID: first.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	err := st.ReplaceDNSZoneBindings(second.ID, []model.DNSZoneBinding{
		{DNSAccountID: second.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	})
	var conflict *DNSZoneConflictError
	if !errors.As(err, &conflict) || conflict.AccountID != first.ID {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestUpsertDNSZoneBinding(t *testing.T) {
	st := newTestStore(t)
	account := &model.DNSAccount{Name: "dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	item, err := st.UpsertDNSZoneBinding(account.ID, model.DNSZoneBinding{
		DNSAccountID: account.ID, Zone: "example.com", MatchMode: "suffix", Source: "verify", Enabled: true,
	})
	if err != nil || item.Source != "verify" {
		t.Fatalf("unexpected upsert: %+v %v", item, err)
	}
	updated, err := st.UpsertDNSZoneBinding(account.ID, model.DNSZoneBinding{
		DNSAccountID: account.ID, Zone: "example.com", MatchMode: "exact", Source: "manual", Priority: 2, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != item.ID || updated.MatchMode != "exact" || updated.Priority != 2 {
		t.Fatalf("upsert did not update existing binding: %+v", updated)
	}
}

func TestDeleteDNSAccountRemovesBindings(t *testing.T) {
	st := newTestStore(t)
	account := &model.DNSAccount{Name: "dns", Provider: "test", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(account.ID, []model.DNSZoneBinding{
		{DNSAccountID: account.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDNSAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListDNSZoneBindingsByAccount(account.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("bindings should be deleted with account: %+v %v", list, err)
	}
}
