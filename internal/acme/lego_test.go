package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

type testUser struct {
	key crypto.PrivateKey
	reg *registration.Resource
}

func (u *testUser) GetEmail() string                        { return "lego@example.com" }
func (u *testUser) GetRegistration() *registration.Resource { return u.reg }
func (u *testUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

type memoryHTTPProvider struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (p *memoryHTTPProvider) Present(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens[token] = keyAuth
	return nil
}
func (p *memoryHTTPProvider) CleanUp(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.tokens, token)
	return nil
}
func (p *memoryHTTPProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/.well-known/acme-challenge/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, prefix)
	p.mu.Lock()
	value := p.tokens[token]
	p.mu.Unlock()
	if value == "" {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(value))
}

var _ challenge.Provider = (*memoryHTTPProvider)(nil)

func TestLegoHTTP01Issuance(t *testing.T) {
	s, _, _, _, _ := newOrderTestServer(t)
	tlsTS := httptest.NewTLSServer(s.Handler())
	defer tlsTS.Close()
	s.BaseURL = tlsTS.URL
	provider := &memoryHTTPProvider{tokens: map[string]string{}}
	challengeServer := httptest.NewServer(provider)
	defer challengeServer.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(challengeServer.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	s.HTTP01.Port = port
	s.HTTP01.AllowedPorts = []string{portText}
	s.HTTP01.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	user := &testUser{key: key}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = tlsTS.URL + "/acme/test/directory"
	cfg.HTTPClient = tlsTS.Client()
	client, err := lego.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Challenge.SetHTTP01Provider(provider); err != nil {
		t.Fatal(err)
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		t.Fatalf("lego 注册失败: %v", err)
	}
	user.reg = reg
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{"localhost"}, Bundle: true})
	if err != nil {
		t.Fatalf("lego 签发失败: %v", err)
	}
	block, _ := pem.Decode(resource.Certificate)
	if block == nil {
		t.Fatal("lego 未返回证书")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" {
		t.Fatalf("lego 证书 SAN 不正确: %v", leaf.DNSNames)
	}
}
