package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

func TestValidateACMECA(t *testing.T) {
	s, _, _ := newACMEAPIStore(t)

	ca := &model.ACMECA{
		Name:         "Internal ACME",
		DirectoryURL: "https://ca.example.com/acme/directory",
		Protocol:     "v2",
	}
	if err := s.validateACMECA(ca, false); err != nil {
		t.Fatalf("合法通用 CA 校验失败: %v", err)
	}

	ca.DirectoryURL = "ftp://ca.example.com/directory"
	if err := s.validateACMECA(ca, false); err == nil {
		t.Fatal("非 http/https 目录地址应报错")
	}

	ca.DirectoryURL = "https://ca.example.com/acme/directory"
	ca.Protocol = "v3"
	if err := s.validateACMECA(ca, false); err == nil {
		t.Fatal("未知协议版本应报错")
	}

	ca.Protocol = "v1"
	ca.EABRequired = true
	ca.EABKid = "kid"
	ca.EABHMAC = "secret"
	if err := s.validateACMECA(ca, false); err == nil {
		t.Fatal("ACME v1 不应允许启用 EAB")
	}

	ca.Protocol = "v2"
	if err := s.validateACMECA(ca, false); err != nil {
		t.Fatalf("v2 + EAB 应通过校验: %v", err)
	}
	if err := s.validateACMECA(ca, true); err != nil {
		t.Fatalf("已有 EAB HMAC 时更新应通过校验: %v", err)
	}
}

func TestACMECACRUDAndMeta(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)

	w := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/acme-cas",
		`{"name":"Internal ACME","directory_url":"https://ca.example.com/acme/directory","protocol":"v2","eab_required":true,"eab_kid":"kid","eab_hmac":"secret"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK {
		t.Fatalf("创建通用 CA 失败: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("响应泄露了 EAB HMAC: %s", w.Body.String())
	}
	var created model.ACMECA
	if err := json.Unmarshal([]byte(extractData(t, w.Body.String())), &created); err != nil {
		t.Fatalf("解析创建结果失败: %v", err)
	}
	if created.ID == 0 || !created.EABHMACSet {
		t.Fatalf("创建结果不正确: %+v", created)
	}

	w = sessionRequest(t, router, http.MethodGet, "/secret-entry/api/meta", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Internal ACME") || !strings.Contains(w.Body.String(), service.ACMECAValue(created.ID)) {
		t.Fatalf("meta 未包含通用 CA: %d %s", w.Code, w.Body.String())
	}

	if err := st.SetSetting("acme.ca", service.ACMECAValue(created.ID)); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodDelete, "/secret-entry/api/acme-cas/"+jsonNumber(created.ID), "", []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusConflict {
		t.Fatalf("默认 CA 不应允许删除: %d %s", w.Code, w.Body.String())
	}

	if err := st.SetSetting("acme.ca", "letsencrypt"); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodDelete, "/secret-entry/api/acme-cas/"+jsonNumber(created.ID), "", []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if w.Code != http.StatusOK {
		t.Fatalf("删除通用 CA 失败: %d %s", w.Code, w.Body.String())
	}
}

func extractData(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return string(envelope.Data)
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
