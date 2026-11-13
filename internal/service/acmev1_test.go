package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
)

type stubChallengeProvider struct {
	presented []string
	cleaned   []string
}

func (s *stubChallengeProvider) Present(domain, token, keyAuth string) error {
	s.presented = append(s.presented, domain)
	return nil
}

func (s *stubChallengeProvider) CleanUp(domain, token, keyAuth string) error {
	s.cleaned = append(s.cleaned, domain)
	return nil
}

func TestACMEV1Obtain(t *testing.T) {
	oldTimeout := acmeV1DNSCheckTimeout
	acmeV1DNSCheckTimeout = 0
	defer func() { acmeV1DNSCheckTimeout = oldTimeout }()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test ACME v1 CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	var server *httptest.Server
	writeNonce := func(w http.ResponseWriter) {
		w.Header().Set("Replay-Nonce", "nonce-"+time.Now().Format("150405.000000000"))
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeNonce(w)
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/directory":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/directory":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"new-reg":     server.URL + "/new-reg",
				"new-authz":   server.URL + "/new-authz",
				"new-cert":    server.URL + "/new-cert",
				"revoke-cert": server.URL + "/revoke",
			})
		case r.URL.Path == "/new-reg":
			w.Header().Set("Location", server.URL+"/reg/1")
			w.Header().Add("Link", "<"+server.URL+"/new-authz>;rel=\"next\"")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"resource":"reg"}`))
		case r.URL.Path == "/new-authz":
			w.Header().Set("Location", server.URL+"/authz/1")
			w.Header().Add("Link", "<"+server.URL+"/new-cert>;rel=\"next\"")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"pending","identifier":{"type":"dns","value":"example.com"},"challenges":[{"type":"dns-01","uri":"` + server.URL + `/challenge/1","token":"token-1","status":"pending"}]}`))
		case r.URL.Path == "/challenge/1":
			_, _ = w.Write([]byte(`{"type":"dns-01","token":"token-1","status":"valid"}`))
		case r.URL.Path == "/new-cert":
			payload := decodeJWSPayload(t, r)
			var body struct {
				CSR string `json:"csr"`
			}
			if err := json.Unmarshal(payload, &body); err != nil {
				t.Errorf("decode new-cert payload: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			csrDER, err := base64.RawURLEncoding.DecodeString(body.CSR)
			if err != nil {
				t.Errorf("decode csr: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			csr, err := x509.ParseCertificateRequest(csrDER)
			if err != nil {
				t.Errorf("parse csr: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			leafTemplate := &x509.Certificate{
				SerialNumber: big.NewInt(42),
				Subject:      csr.Subject,
				DNSNames:     csr.DNSNames,
				NotBefore:    time.Now().Add(-time.Minute),
				NotAfter:     time.Now().Add(24 * time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, csr.PublicKey, caKey)
			if err != nil {
				t.Errorf("sign cert: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Add("Link", "<"+server.URL+"/issuer>;rel=\"up\"")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(leafDER)
		case r.URL.Path == "/issuer":
			_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	accountKey, err := certcrypto.GeneratePrivateKey(certcrypto.EC256)
	if err != nil {
		t.Fatal(err)
	}
	provider := &stubChallengeProvider{}
	result, err := (&Manager{}).obtainV1(CADef{URL: server.URL + "/directory"}, &acmeUser{email: "test@example.com", key: accountKey}, ACMERequest{
		Domains: []string{"example.com", "www.example.com"},
		Email:   "test@example.com",
	}, provider, certcrypto.EC256)
	if err != nil {
		t.Fatalf("obtainV1: %v", err)
	}
	if result.Issuer != "Test ACME v1 CA" {
		t.Fatalf("unexpected issuer: %q", result.Issuer)
	}
	if !strings.Contains(result.CertPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("missing certificate PEM: %q", result.CertPEM)
	}
	if !strings.Contains(result.KeyPEM, "PRIVATE KEY") {
		t.Fatalf("missing private key PEM")
	}
	if !strings.Contains(result.FullchainPEM, "Test ACME v1 CA") {
		// The issuer common name is not part of PEM text; check the chain has two certificates.
		if strings.Count(result.FullchainPEM, "BEGIN CERTIFICATE") != 2 {
			t.Fatalf("expected leaf and issuer in fullchain, got %q", result.FullchainPEM)
		}
	}
	if len(provider.presented) != 2 {
		t.Fatalf("expected 2 DNS challenges, got %d", len(provider.presented))
	}
}

func decodeJWSPayload(t *testing.T, r *http.Request) []byte {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read JWS body: %v", err)
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		t.Fatalf("invalid JWS: %q", string(raw))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWS payload: %v", err)
	}
	return payload
}
