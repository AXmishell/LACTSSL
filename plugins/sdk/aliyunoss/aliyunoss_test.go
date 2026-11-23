package aliyunoss

import (
	"net/http"
	"testing"
	"time"
)

func TestCanonicalResource(t *testing.T) {
	got := CanonicalResource("demo", "/", map[string]string{"cname": "", "comp": "add"})
	if got != "/demo/?cname&comp=add" {
		t.Fatalf("resource = %q", got)
	}
}

func TestSignV1Stable(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-oss-security-token", "token")
	now, _ := time.Parse(http.TimeFormat, "Tue, 17 Nov 2026 10:00:00 GMT")
	signature := SignV1(http.MethodPost, "md5", "application/xml", now.Format(http.TimeFormat), "/demo/?cname&comp=add", headers, "secret")
	if signature == "" || signature == "OSS " {
		t.Fatalf("unexpected signature %q", signature)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://demo.example.com/", nil)
	first := SetHeaders(request, Credentials{
		AccessKeyID: "ak", AccessKeySecret: "secret",
	}, "/demo/?cname&comp=add", "md5", "application/xml", now)
	if first == "" {
		t.Fatal("empty authorization")
	}
}
