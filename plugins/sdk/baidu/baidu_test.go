package baidu

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignWithoutBodyOmitsContentLength(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://cdn.baidubce.com/v2/example.com/certificates", nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := Signer{AccessKey: "ak", SecretKey: "sk", Now: func() time.Time { return time.Unix(1700000000, 0) }}
	if err := signer.Sign(req, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.Header.Get("Authorization"), "content-length") {
		t.Fatalf("GET signature must not include content-length: %s", req.Header.Get("Authorization"))
	}
}

func TestSignBuildsBCEV1Authorization(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T10:00:00Z")
	req, _ := http.NewRequest(http.MethodPut, "https://cdn.baidubce.com/v2/example.com/certificates", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	signer := Signer{AccessKey: "ak", SecretKey: "sk", Now: func() time.Time { return now }}
	if err := signer.Sign(req, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "bce-auth-v1/ak/2026-11-17T10:00:00Z/1800/") {
		t.Fatalf("authorization = %q", auth)
	}
	if !strings.Contains(auth, "content-length;content-type;host;x-bce-date") {
		t.Fatalf("signed headers missing: %q", auth)
	}
	if req.Header.Get("x-bce-date") != "2026-11-17T10:00:00Z" {
		t.Fatalf("date = %q", req.Header.Get("x-bce-date"))
	}
}
