package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newACMEAPIStore(t *testing.T) (*Server, *store.Store, *model.PrivateCA) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}, st, ca
}

func TestValidateACMEEndpoint(t *testing.T) {
	s, _, ca := newACMEAPIStore(t)
	ep := &model.ACMEEndpoint{
		Name:             "Internal",
		Slug:             "internal",
		CAID:             ca.ID,
		Enabled:          true,
		EABRequired:      true,
		HTTP01Enabled:    true,
		DNS01Enabled:     true,
		AllowedDomains:   []string{"example.com"},
		ValidityDays:     90,
		MaxOrdersPerHour: 60,
	}
	if err := s.validateACMEEndpoint(ep); err != nil {
		t.Fatalf("合法端点校验失败: %v", err)
	}

	ep.AllowedDomains = nil
	if err := s.validateACMEEndpoint(ep); err == nil {
		t.Fatal("启用端点但未配置域名白名单时应报错")
	}

	ep.AllowedDomains = []string{"example.com"}
	ep.Slug = "Bad Slug"
	if err := s.validateACMEEndpoint(ep); err == nil {
		t.Fatal("非法 slug 应报错")
	}

	ep.Slug = "internal"
	ep.HTTP01Enabled = false
	ep.DNS01Enabled = false
	if err := s.validateACMEEndpoint(ep); err == nil {
		t.Fatal("未启用任何挑战类型时应报错")
	}
}

func TestCleanupACMEDiagnosticsEndpoint(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	acmeToken := secondaryVerifyToken(t, router, session, secondaryActionACMEWrite, "password1234", "")

	ep := &model.ACMEEndpoint{PublicID: "ep", Name: "Internal", Slug: "internal", CAID: 1, Enabled: true, AllowedDomains: []string{"example.com"}}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	account := &model.ACMEAccount{PublicID: "account", EndpointID: ep.ID, Kid: "kid", PublicJWK: "{}", Thumbprint: "thumb", Status: "valid"}
	if err := st.CreateACMEAccount(account, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	createOrder := func(name, status string) *model.ACMEOrder {
		order := &model.ACMEOrder{PublicID: "order-" + name, EndpointID: ep.ID, AccountID: account.ID, Status: status, Identifiers: []string{"example.com"}, ExpiresAt: &now}
		authz := &model.ACMEAuthorization{PublicID: "authz-" + name, Identifier: "example.com", IdentifierType: "dns", Status: status, ExpiresAt: &now, Challenges: []model.ACMEChallenge{{PublicID: "challenge-" + name, Type: "http-01", Token: "token-" + name, Status: status}}}
		if err := st.CreateACMEOrder(order, []*model.ACMEAuthorization{authz}); err != nil {
			t.Fatal(err)
		}
		return order
	}
	invalid := createOrder("invalid", "invalid")
	valid := createOrder("valid", "valid")
	if _, err := st.DB.Exec(`UPDATE acme_orders SET expires_at=? WHERE id=?`, now.Add(time.Hour), valid.ID); err != nil {
		t.Fatal(err)
	}

	w := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/acme-diagnostics/cleanup/preview",
		`{"target":"orders","scope":"stale"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"orders":1`) {
		t.Fatalf("清理预览不正确: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetACMEOrderByID(invalid.ID); err != nil {
		t.Fatalf("预览不应删除订单: %v", err)
	}

	w = sessionRequest(t, router, http.MethodPost, "/secret-entry/api/acme-diagnostics/cleanup",
		`{"target":"orders","scope":"stale"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: acmeToken})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"orders":1`) {
		t.Fatalf("清理失效订单失败: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetACMEOrderByID(invalid.ID); err == nil {
		t.Fatal("失效订单未被清理")
	}
	if _, err := st.GetACMEOrderByID(valid.ID); err != nil {
		t.Fatalf("有效订单不应被清理: %v", err)
	}

	w = sessionRequest(t, router, http.MethodPost, "/secret-entry/api/acme-diagnostics/cleanup",
		`{"target":"unknown","scope":"stale"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: acmeToken})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法清理对象应返回 400: %d %s", w.Code, w.Body.String())
	}
}

func TestACMEDirectoryURL(t *testing.T) {
	s := &Server{ACMEBaseURL: "https://pki.example.com:14000/"}
	if got := s.acmeDirectoryURL(nil, "internal"); got != "https://pki.example.com:14000/acme/internal/directory" {
		t.Fatalf("directory URL 不正确: %s", got)
	}
}
