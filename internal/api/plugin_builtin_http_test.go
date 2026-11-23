package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/fstest"

	"lactssl/internal/auth"
	"lactssl/internal/service"
)

func TestHTTPBatchEnableBuiltinDNS(t *testing.T) {
	st := newSessionTestStore(t)
	s := New(st, auth.New([]byte("jwt-secret")), service.New(st), "entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}}, nil)
	s.AllowInsecureSetup = true
	s.BuiltinPluginsEnabled = true
	s.PluginsEnabled = true
	s.DiscoverPlugins()
	s.SetSetupToken("setup-token")
	router := s.Router()

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil,
		map[string]string{setupTokenHeader: "setup-token"})
	if setup.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	// CSRF 双提交：测试 helper 会自动补 csrf cookie。
	enable := sessionRequest(t, router, http.MethodPut, "/api/plugins/providers",
		`{"enabled":true}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if enable.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enable.Code, enable.Body.String())
	}
	var payload struct {
		Data struct {
			Updated int `json:"updated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(enable.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Updated < 200 {
		t.Fatalf("updated=%d body=%s", payload.Data.Updated, enable.Body.String())
	}
}
