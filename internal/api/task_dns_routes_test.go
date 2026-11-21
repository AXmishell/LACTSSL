package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"lactssl/internal/model"
)

func TestCreateTaskRoutesDomainsToMultipleDNSAccounts(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{first, second} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceDNSZoneBindings(first.ID, []model.DNSZoneBinding{
		{DNSAccountID: first.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(second.ID, []model.DNSZoneBinding{
		{DNSAccountID: second.ID, Zone: "other.net", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	body := `{"name":"multi-dns","domains":["a.example.com","b.other.net"],"ca":"letsencrypt","key_type":"ecdsa","renew_days":30,"enabled":false}`
	resp := sessionRequest(t, router, http.MethodPost, "/api/tasks", body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("创建跨账户任务失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data model.Task `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.DNSAccountID != 0 {
		t.Fatalf("跨账户任务不应强制写单一账户: %+v", payload.Data)
	}
	if len(payload.Data.DNSRoutes) != 2 {
		t.Fatalf("跨账户任务应保存两条路由: %+v", payload.Data.DNSRoutes)
	}
	if payload.Data.DNSRoutes[0].DNSAccountID != first.ID || payload.Data.DNSRoutes[1].DNSAccountID != second.ID {
		t.Fatalf("DNS 路由账户不正确: %+v", payload.Data.DNSRoutes)
	}
}

func TestResolvePreviewReturnsMultipleDNSRoutes(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{first, second} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceDNSZoneBindings(first.ID, []model.DNSZoneBinding{
		{DNSAccountID: first.ID, Zone: "example.com", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceDNSZoneBindings(second.ID, []model.DNSZoneBinding{
		{DNSAccountID: second.ID, Zone: "other.net", MatchMode: "suffix", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	resp := sessionRequest(t, router, http.MethodPost, "/api/dns-accounts/resolve",
		`{"domains":["a.example.com","b.other.net"]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("解析预览失败: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			AccountID  int64             `json:"account_id"`
			AccountIDs []int64           `json:"account_ids"`
			Routes     []model.DNSRoute  `json:"routes"`
			Matches    []json.RawMessage `json:"matches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.AccountID != 0 || len(payload.Data.AccountIDs) != 2 || len(payload.Data.Routes) != 2 {
		t.Fatalf("跨账户预览应返回多条路由: %+v", payload.Data)
	}
}

func TestUpdateTaskPreservesMultiDNSRoutes(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	first := &model.DNSAccount{Name: "first", Provider: "test", Config: map[string]string{}}
	second := &model.DNSAccount{Name: "second", Provider: "test", Config: map[string]string{}}
	for _, account := range []*model.DNSAccount{first, second} {
		if err := st.CreateDNSAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	task := &model.Task{
		Name: "multi", Domains: []string{"a.example.com", "b.other.net"}, Challenge: "dns01",
		CA: "letsencrypt", KeyType: "ecdsa", RenewDays: 30, Enabled: false, Status: "pending",
		DNSRoutes: []model.DNSRoute{
			{Domain: "a.example.com", Zone: "example.com", DNSAccountID: first.ID},
			{Domain: "b.other.net", Zone: "other.net", DNSAccountID: second.ID},
		},
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	// 绑定被删除后，未改域名的更新仍应沿用任务快照。
	if err := st.ReplaceDNSZoneBindings(second.ID, nil); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"multi","domains":["a.example.com","b.other.net"],"ca":"letsencrypt","key_type":"ecdsa","renew_days":30,"enabled":false}`
	resp := sessionRequest(t, router, http.MethodPut, "/api/tasks/"+strconv.FormatInt(task.ID, 10), body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if resp.Code != http.StatusOK {
		t.Fatalf("更新任务应沿用已保存路由: %d %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data model.Task `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.DNSRoutes) != 2 || payload.Data.DNSRoutes[1].DNSAccountID != second.ID {
		t.Fatalf("更新后路由被意外改变: %+v", payload.Data.DNSRoutes)
	}
}
