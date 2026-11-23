package doge

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployDoge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha1.New, []byte("secret"))
		mac.Write([]byte(r.URL.Path + "\n" + string(body)))
		want := "TOKEN ak:" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("Authorization") != want {
			t.Errorf("authorization mismatch: %q", r.Header.Get("Authorization"))
		}
		values, _ := url.ParseQuery(string(body))
		switch r.URL.Path {
		case "/cdn/cert/upload.json":
			if values.Get("cert") != "FULLCHAIN" || values.Get("private") != "KEY" {
				t.Errorf("certificate missing: %v", values)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"id":21}}`))
		case "/cdn/cert/bind.json":
			if values.Get("id") != "21" || values.Get("domain") != "example.com" {
				t.Errorf("bind payload: %v", values)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	old := dogeBaseURL
	dogeBaseURL = server.URL
	t.Cleanup(func() { dogeBaseURL = old })

	cert := &deploy.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployDoge(cert, map[string]string{"access_key": "ak", "secret_key": "secret"}); err != nil {
		t.Fatal(err)
	}
}

func TestDogeValidation(t *testing.T) {
	if err := deployDoge(&deploy.Certificate{}, map[string]string{}); err == nil || !strings.Contains(err.Error(), "AccessKey") {
		t.Fatalf("unexpected error: %v", err)
	}
}
