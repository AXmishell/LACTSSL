package tencentcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDoSignsRequestAndDecodesResponse(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T10:00:00Z")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") != "UploadCertificate" {
			t.Errorf("action = %q", r.Header.Get("X-TC-Action"))
		}
		if r.Header.Get("X-TC-Version") != "2019-12-05" {
			t.Errorf("version = %q", r.Header.Get("X-TC-Version"))
		}
		if r.Header.Get("X-TC-Timestamp") != "1794909600" {
			t.Errorf("timestamp = %q", r.Header.Get("X-TC-Timestamp"))
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "TC3-HMAC-SHA256 Credential=id/20261117/ssl/tc3_request") {
			t.Errorf("authorization = %q", auth)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("payload: %v", err)
		}
		if payload["Alias"] != "demo" {
			t.Errorf("payload = %v", payload)
		}
		_, _ = w.Write([]byte(`{"Response":{"CertificateId":"cert-1","RequestId":"req-1"}}`))
	}))
	defer server.Close()

	client := &Client{
		HTTP:        server.Client(),
		Endpoint:    server.URL,
		Credentials: Credentials{SecretID: "id", SecretKey: "key"},
		Now:         func() time.Time { return now },
	}
	var out struct {
		CertificateID string `json:"CertificateId"`
	}
	requestID, err := client.Do(context.Background(), "ssl", "", "UploadCertificate", "2019-12-05", map[string]any{"Alias": "demo"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if requestID != "req-1" || out.CertificateID != "cert-1" {
		t.Fatalf("requestID=%q certID=%q", requestID, out.CertificateID)
	}
}

func TestDoReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"AuthFailure","Message":"bad key"},"RequestId":"req-2"}}`))
	}))
	defer server.Close()
	client := &Client{
		HTTP:        server.Client(),
		Endpoint:    server.URL,
		Credentials: Credentials{SecretID: "id", SecretKey: "key"},
	}
	_, err := client.Do(context.Background(), "ssl", "", "UploadCertificate", "2019-12-05", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "AuthFailure") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoRequiresCredentials(t *testing.T) {
	client := &Client{}
	if _, err := client.Do(context.Background(), "ssl", "", "X", "v", nil, nil); err == nil {
		t.Fatal("expected credentials error")
	}
}
