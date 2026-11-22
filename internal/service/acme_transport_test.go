package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestACMECompatibilityAddsMissingUpLink(t *testing.T) {
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/authz/1":
			_, _ = io.WriteString(w, `{"status":"pending","challenges":[{"type":"dns-01","url":"`+base+`/chall/1"}]}`)
		case "/chall/1":
			_, _ = io.WriteString(w, `{"type":"dns-01","status":"processing"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL

	client := withACMECompatibility(&http.Client{})

	authzResp := doPost(t, client, server.URL+"/authz/1")
	body, err := io.ReadAll(authzResp.Body)
	authzResp.Body.Close()
	if err != nil {
		t.Fatalf("读取 authorization 响应失败: %v", err)
	}
	if !strings.Contains(string(body), "/chall/1") {
		t.Fatalf("authorization 响应体被破坏: %s", body)
	}

	challengeResp := doPost(t, client, server.URL+"/chall/1")
	defer challengeResp.Body.Close()
	if got := challengeResp.Header.Get("Link"); !strings.Contains(got, `<`+server.URL+`/authz/1>;rel="up"`) {
		t.Fatalf("未补全 up 链接，Link = %q", got)
	}
}

func TestACMECompatibilityKeepsExistingUpLink(t *testing.T) {
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/authz/1":
			_, _ = io.WriteString(w, `{"challenges":[{"type":"http-01","url":"`+base+`/chall/1"}]}`)
		case "/chall/1":
			w.Header().Set("Link", `<`+base+`/authz/1>;rel="up"`)
			_, _ = io.WriteString(w, `{"status":"processing"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL

	client := withACMECompatibility(&http.Client{})
	doPost(t, client, server.URL+"/authz/1").Body.Close()

	resp := doPost(t, client, server.URL+"/chall/1")
	defer resp.Body.Close()
	if got := len(resp.Header.Values("Link")); got != 1 {
		t.Fatalf("已有 up 链接不应重复添加，Link 数量 = %d", got)
	}
}

func TestACMECompatibilityWithoutAuthorizationMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"processing"}`)
	}))
	defer server.Close()

	client := withACMECompatibility(&http.Client{})
	resp := doPost(t, client, server.URL+"/chall/unknown")
	defer resp.Body.Close()
	if got := resp.Header.Get("Link"); got != "" {
		t.Fatalf("未知 challenge 不应补 up 链接，Link = %q", got)
	}
}

func TestACMECompatibilityResolvesRelativeChallengeURL(t *testing.T) {
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/acme/authz/1":
			_, _ = io.WriteString(w, `{"challenges":[{"type":"dns-01","url":"../chall/1"}]}`)
		case "/acme/chall/1":
			_, _ = io.WriteString(w, `{"status":"processing"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL

	client := withACMECompatibility(&http.Client{})
	doPost(t, client, server.URL+"/acme/authz/1").Body.Close()

	resp := doPost(t, client, server.URL+"/acme/chall/1")
	defer resp.Body.Close()
	if got := resp.Header.Get("Link"); !strings.Contains(got, `<`+base+`/acme/authz/1>;rel="up"`) {
		t.Fatalf("相对 challenge URL 未正确补全，Link = %q", got)
	}
}

func doPost(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(""))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
}
