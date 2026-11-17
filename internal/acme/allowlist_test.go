package acme

import "testing"

func TestDomainAllowed(t *testing.T) {
	cases := []struct {
		name     string
		allowed  []string
		value    string
		expected bool
	}{
		{"apex allowed by bare entry", []string{"example.com"}, "example.com", true},
		{"subdomain allowed by bare entry", []string{"example.com"}, "a.example.com", true},
		{"wildcard identity allowed by bare entry", []string{"example.com"}, "*.example.com", true},
		{"deep subdomain allowed", []string{"example.com"}, "a.b.example.com", true},
		{"other domain rejected", []string{"example.com"}, "example.net", false},
		{"suffix without dot rejected", []string{"example.com"}, "notexample.com", false},
		{"wildcard entry rejects apex", []string{"*.example.com"}, "example.com", false},
		{"wildcard entry allows subdomain", []string{"*.example.com"}, "a.example.com", true},
		{"wildcard entry allows wildcard identity", []string{"*.example.com"}, "*.example.com", true},
		{"wildcard entry allows deep wildcard identity", []string{"*.example.com"}, "*.a.example.com", true},
		{"empty entry ignored", []string{"", "  "}, "example.com", false},
		{"case insensitive", []string{"Example.COM"}, "A.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domainAllowed(tc.value, tc.allowed); got != tc.expected {
				t.Fatalf("domainAllowed(%q, %v) = %v, want %v", tc.value, tc.allowed, got, tc.expected)
			}
		})
	}
}
