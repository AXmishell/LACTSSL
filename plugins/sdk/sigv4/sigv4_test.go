package sigv4

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignMatchesAWSReference(t *testing.T) {
	now, _ := time.Parse("20060102T150405Z", "20150830T123600Z")
	body := []byte(`{"a":1}`)
	req, _ := http.NewRequest(http.MethodPost, "https://example.amazonaws.com/foo/bar?b=2&a=1", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	signer := Signer{
		Region:  "us-east-1",
		Service: "service",
		Credentials: Credentials{
			AccessKeyID:     "AKIDEXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		},
		Now: func() time.Time { return now },
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-date, Signature=84dd7a4b5bbfdcb2f10240665532621973bfddcb315d8effa8e004f43ddcdc71"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization mismatch:\n got %s\nwant %s", got, want)
	}
}
