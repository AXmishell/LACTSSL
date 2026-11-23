package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDoJSONAndError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") == "1" {
			http.Error(w, "boom", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := New(server.URL)
	resp, err := client.Do(context.Background(), http.MethodGet, "/", nil, nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var payload struct {
		OK bool `json:"ok"`
	}
	if err := JSON(resp.Body, &payload); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !payload.OK {
		t.Fatal("expected ok")
	}

	_, err = client.Do(context.Background(), http.MethodGet, "/?fail=1", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if IsNotFound(err) {
		t.Fatal("did not expect not found")
	}
}

func TestErrorRedactsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.Do(context.Background(), http.MethodGet, "/v1/records?AccessKeyId=AK&Signature=secret-signature&SecurityToken=temporary-token", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	for _, secret := range []string{"secret-signature", "temporary-token", "AccessKeyId"} {
		if strings.Contains(apiErr.URL, secret) {
			t.Fatalf("error URL leaked %q: %s", secret, apiErr.URL)
		}
	}
}

func TestNewRequestMergesExistingQuery(t *testing.T) {
	client := New("https://example.invalid")
	req, err := client.NewRequest(context.Background(), http.MethodGet, "/x?keep=1", url.Values{"add": {"2"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.Query(); got.Get("keep") != "1" || got.Get("add") != "2" {
		t.Fatalf("query = %v", got)
	}
}

func TestSendRedactsTransportError(t *testing.T) {
	client := New("http://127.0.0.1:1")
	_, err := client.Do(context.Background(), http.MethodGet, "/x?token=SECRET", nil, nil, nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("transport error leaked token: %s", err)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	var jumped atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jumped.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := New(source.URL)
	_, err := client.Do(context.Background(), http.MethodGet, "/", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("expected 307 error, got %v", err)
	}
	if jumped.Load() != 0 {
		t.Fatal("redirect target must not be contacted")
	}
}
