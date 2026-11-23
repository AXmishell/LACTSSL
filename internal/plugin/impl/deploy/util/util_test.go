package util

import (
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

func TestBaseURLHTTPSPolicy(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "https://example.com"} {
		if _, err := BaseURL(value); err != nil {
			t.Fatalf("BaseURL(%q): %v", value, err)
		}
	}
	if _, err := BaseURL("http://198.51.100.1"); err == nil {
		t.Fatal("public http base URL must be rejected")
	}
}

func TestDomainMatchWildcardDepth(t *testing.T) {
	cases := []struct {
		left, right string
		want        bool
	}{
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "a.b.example.com", false},
		{"a.example.com", "*.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "notexample.com", false},
		{"a.example.com", "a.example.com", true},
	}
	for _, tc := range cases {
		if got := DomainMatch(tc.left, tc.right); got != tc.want {
			t.Errorf("DomainMatch(%q,%q)=%v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestMergeConfigWins(t *testing.T) {
	got := Merge(map[string]string{"a": "auth", "b": "keep"}, map[string]string{"a": "config"})
	if got["a"] != "config" || got["b"] != "keep" {
		t.Fatalf("Merge = %#v", got)
	}
}

func TestCertificateDomains(t *testing.T) {
	cert := Certificate(protocol.Certificate{
		Domain:       "example.com",
		Domains:      []string{"example.com", "www.example.com"},
		CertPEM:      "CERT",
		FullchainPEM: "FULLCHAIN",
		KeyPEM:       "KEY",
	})
	if cert.Domain != "example.com" || len(cert.Domains) != 2 {
		t.Fatalf("certificate = %#v", cert)
	}
}
