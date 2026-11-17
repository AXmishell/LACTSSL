package acme

import "testing"

func FuzzParseJWS(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"protected":"eyJhbGciOiJFUzI1NiJ9","payload":"e30","signature":"AA"}`))
	f.Add([]byte("not json"))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = parseJWS(body, "https://example.com/acme/test/new-order", NewNonceStore())
	})
}

func FuzzDNSIdentifiers(f *testing.F) {
	f.Add("example.com")
	f.Add("*.example.com")
	f.Add("a_b.example.com")
	f.Fuzz(func(t *testing.T, value string) {
		_ = validDNSIdentifier(value)
		_, _ = normalizeDNS01Host(value)
		_, _, _, _ = normalizeTLSALPNIdentifier(value)
	})
}
