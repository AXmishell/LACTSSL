package edgegrid

import (
	"net/http"
	"testing"
)

func TestCanonicalHeadersPreservesValueCase(t *testing.T) {
	header := http.Header{"X-Test": {"AbC"}}
	if got := canonicalHeaders(header, []string{"x-test"}); got != "x-test:AbC" {
		t.Fatalf("canonicalHeaders = %q", got)
	}
}
