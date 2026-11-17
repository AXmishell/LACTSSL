package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func accountThumbprint(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	jwk := jose.JSONWebKey{Key: key.Public()}
	sum, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(sum)
}

func newOrderTestServer(t *testing.T) (*Server, *store.Store, *model.ACMEEndpoint, *httptest.Server, *model.PrivateCA) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-order-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	ca, err := service.GeneratePrivateCA(service.PrivateCARequest{Name: "Issuing CA", KeyType: "ecdsa", ValidDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	ep := &model.ACMEEndpoint{
		PublicID:           "endpoint-order",
		Name:               "Order Test",
		Slug:               "test",
		CAID:               ca.ID,
		Enabled:            true,
		EABRequired:        false,
		HTTP01Enabled:      true,
		DNS01Enabled:       true,
		AllowWildcard:      true,
		AllowIP:            true,
		AllowedDomains:     []string{"localhost"},
		AllowedIPCIDRs:     []string{"127.0.0.0/8", "::1/128"},
		HTTP01AllowedCIDRs: []string{"127.0.0.0/8", "::1/128"},
		ValidityDays:       90,
		MaxOrdersPerHour:   60,
	}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, "")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	s.BaseURL = ts.URL
	return s, st, ep, ts, ca
}

func createTestAccount(t *testing.T, ts *httptest.Server) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	target := ts.URL + "/acme/test/new-account"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, "", []byte(`{}`))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建账户失败: %d %s", resp.StatusCode, data)
	}
	kid := resp.Header.Get("Location")
	if kid == "" {
		t.Fatal("账户缺少 Location")
	}
	return key, kid
}

func startHTTP01ChallengeServer(t *testing.T, thumbprint string) int {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/.well-known/acme-challenge/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, prefix)
		_, _ = w.Write([]byte(token + "." + thumbprint))
	}))
	t.Cleanup(ts.Close)
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestHTTP01OrderIssuanceFlow(t *testing.T) {
	s, _, _, ts, ca := newOrderTestServer(t)
	key, kid := createTestAccount(t, ts)
	port := startHTTP01ChallengeServer(t, accountThumbprint(t, key))
	s.HTTP01.Port = port
	s.HTTP01.AllowedPorts = []string{strconv.Itoa(port)}
	s.HTTP01.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}

	orderTarget := ts.URL + "/acme/test/new-order"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, orderTarget, nonce, kid, []byte(`{"identifiers":[{"type":"dns","value":"localhost"}]}`))
	resp, data := postJWS(t, orderTarget, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建订单失败: %d %s", resp.StatusCode, data)
	}
	var order orderView
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "pending" || len(order.Authorizations) != 1 {
		t.Fatalf("订单内容不正确: %+v", order)
	}
	orderURL := resp.Header.Get("Location")
	if orderURL == "" {
		t.Fatal("订单缺少 Location")
	}

	// 查询订单，获取授权 URL。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询订单失败: %d %s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	authzURL := order.Authorizations[0]

	// 查询授权，找到 HTTP-01 挑战。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, authzURL, nonce, kid, []byte{})
	resp, data = postJWS(t, authzURL, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询授权失败: %d %s", resp.StatusCode, data)
	}
	var authz authorizationView
	if err := json.Unmarshal(data, &authz); err != nil {
		t.Fatal(err)
	}
	var challenge challengeView
	for _, item := range authz.Challenges {
		if item.Type == "http-01" {
			challenge = item
			break
		}
	}
	if challenge.URL == "" {
		t.Fatal("授权中没有 HTTP-01 挑战")
	}

	// 触发 HTTP-01 验证。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, challenge.URL, nonce, kid, []byte(`{}`))
	resp, data = postJWS(t, challenge.URL, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("挑战验证失败: %d %s", resp.StatusCode, data)
	}
	var challengeResult challengeView
	if err := json.Unmarshal(data, &challengeResult); err != nil {
		t.Fatal(err)
	}
	if challengeResult.Status != "valid" {
		detail := ""
		if challengeResult.Error != nil {
			detail = challengeResult.Error.Detail
		}
		t.Fatalf("挑战未通过: %+v detail=%s body=%s", challengeResult, detail, data)
	}

	// 订单应进入 ready。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "ready" {
		t.Fatalf("订单状态应为 ready: %s %s", order.Status, data)
	}

	// 生成 CSR 并 finalize。
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"},
	}, leafKey)
	if err != nil {
		t.Fatal(err)
	}
	finalizeURL := order.Finalize
	payload, _ := json.Marshal(map[string]string{"csr": base64.RawURLEncoding.EncodeToString(csrDER)})
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, finalizeURL, nonce, kid, payload)
	resp, data = postJWS(t, finalizeURL, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finalize 失败: %d %s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "valid" || order.Certificate == "" {
		t.Fatalf("订单签发后状态不正确: %+v", order)
	}

	// 下载证书链。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, order.Certificate, nonce, kid, []byte{})
	req, _ := http.NewRequest(http.MethodPost, order.Certificate, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/jose+json")
	certResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer certResp.Body.Close()
	if certResp.StatusCode != http.StatusOK {
		t.Fatalf("下载证书失败: %d", certResp.StatusCode)
	}
	certPEM, err := io.ReadAll(certResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("证书响应不是 PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" {
		t.Fatalf("证书 SAN 不正确: %v", leaf.DNSNames)
	}
	if len(leaf.CRLDistributionPoints) == 0 || len(leaf.OCSPServer) == 0 {
		t.Fatalf("证书缺少 CRL/OCSP 扩展: crl=%v ocsp=%v", leaf.CRLDistributionPoints, leaf.OCSPServer)
	}
	caCertBlock, _ := pem.Decode([]byte(ca.CertPEM))
	caCert, _ := x509.ParseCertificate(caCertBlock.Bytes)
	if err := leaf.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("证书不是由指定私有 CA 签发: %v", err)
	}

	// 通过 ACME revoke-cert 撤销证书，并确认 CRL 已更新。
	revokePayload, _ := json.Marshal(map[string]any{
		"certificate": base64.RawURLEncoding.EncodeToString(leaf.Raw),
		"reason":      1,
	})
	revokeURL := ts.URL + "/acme/test/revoke-cert"
	nonce = certResp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, revokeURL, nonce, kid, revokePayload)
	revokeResp, revokeData := postJWS(t, revokeURL, body)
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("撤销证书失败: %d %s", revokeResp.StatusCode, revokeData)
	}
	crlResp, err := http.Get(ts.URL + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/crl")
	if err != nil {
		t.Fatal(err)
	}
	defer crlResp.Body.Close()
	crlDER, _ := io.ReadAll(crlResp.Body)
	crl, err := x509.ParseRevocationList(crlDER)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("撤销后 CRL 未包含证书序列号")
	}
}

func TestNewOrderRejectsDomainOutsideAllowlist(t *testing.T) {
	_, _, _, ts, _ := newOrderTestServer(t)
	key, kid := createTestAccount(t, ts)
	target := ts.URL + "/acme/test/new-order"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, kid, []byte(`{"identifiers":[{"type":"dns","value":"evil.example.com"}]}`))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "rejectedIdentifier") {
		t.Fatalf("白名单外域名未被拒绝: %d %s", resp.StatusCode, data)
	}
}

func TestOrderExpires(t *testing.T) {
	order := &model.ACMEOrder{ExpiresAt: func() *time.Time { value := time.Now().Add(-time.Minute); return &value }()}
	s := NewServer(nil, "")
	if !s.orderExpired(order) {
		t.Fatal("过期订单应被识别")
	}
}

func TestWildcardDNS01OrderIssuanceFlow(t *testing.T) {
	s, _, _, ts, _ := newOrderTestServer(t)
	key, kid := createTestAccount(t, ts)
	thumbprint := accountThumbprint(t, key)

	orderTarget := ts.URL + "/acme/test/new-order"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, orderTarget, nonce, kid, []byte(`{"identifiers":[{"type":"dns","value":"*.localhost"}]}`))
	resp, data := postJWS(t, orderTarget, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建通配符订单失败: %d %s", resp.StatusCode, data)
	}
	var order orderView
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	orderURL := resp.Header.Get("Location")
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	authzURL := order.Authorizations[0]
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, authzURL, nonce, kid, []byte{})
	resp, data = postJWS(t, authzURL, body)
	var authz authorizationView
	if err := json.Unmarshal(data, &authz); err != nil {
		t.Fatal(err)
	}
	if !authz.Wildcard || len(authz.Challenges) != 1 || authz.Challenges[0].Type != "dns-01" {
		t.Fatalf("通配符授权应只有 DNS-01: %+v", authz)
	}
	challenge := authz.Challenges[0]
	sum := sha256.Sum256([]byte(challenge.Token + "." + thumbprint))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	s.DNS01.LookupTXT = func(ctx context.Context, name string) ([]string, error) {
		if name != "_acme-challenge.localhost" {
			t.Fatalf("DNS-01 查询名称不正确: %s", name)
		}
		return []string{expected}, nil
	}
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, challenge.URL, nonce, kid, []byte(`{}`))
	resp, data = postJWS(t, challenge.URL, body)
	var challengeResult challengeView
	if err := json.Unmarshal(data, &challengeResult); err != nil {
		t.Fatal(err)
	}
	if challengeResult.Status != "valid" {
		t.Fatalf("DNS-01 挑战未通过: %s", data)
	}

	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "ready" {
		t.Fatalf("通配符订单应 ready: %s", order.Status)
	}
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "*.localhost"},
		DNSNames: []string{"*.localhost"},
	}, leafKey)
	payload, _ := json.Marshal(map[string]string{"csr": base64.RawURLEncoding.EncodeToString(csrDER)})
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, order.Finalize, nonce, kid, payload)
	resp, data = postJWS(t, order.Finalize, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "valid" {
		t.Fatalf("通配符订单签发失败: %s %s", order.Status, data)
	}
}

func TestHTTP01IPIdentifierIssuanceFlow(t *testing.T) {
	s, _, _, ts, ca := newOrderTestServer(t)
	key, kid := createTestAccount(t, ts)
	port := startHTTP01ChallengeServer(t, accountThumbprint(t, key))
	s.HTTP01.Port = port
	s.HTTP01.AllowedPorts = []string{strconv.Itoa(port)}
	s.HTTP01.AllowedCIDRs = []string{"127.0.0.0/8"}

	target := ts.URL + "/acme/test/new-order"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, kid, []byte(`{"identifiers":[{"type":"ip","value":"127.0.0.1"}]}`))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建 IP 订单失败: %d %s", resp.StatusCode, data)
	}
	var order orderView
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	orderURL := resp.Header.Get("Location")
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	authzURL := order.Authorizations[0]
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, authzURL, nonce, kid, []byte{})
	resp, data = postJWS(t, authzURL, body)
	var authz authorizationView
	if err := json.Unmarshal(data, &authz); err != nil {
		t.Fatal(err)
	}
	if authz.Identifier.Type != "ip" || authz.Identifier.Value != "127.0.0.1" {
		t.Fatalf("IP 授权标识不正确: %+v", authz.Identifier)
	}
	var challenge challengeView
	for _, item := range authz.Challenges {
		if item.Type == "http-01" {
			challenge = item
		}
	}
	if challenge.URL == "" {
		t.Fatal("IP 授权缺少 HTTP-01 挑战")
	}
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, challenge.URL, nonce, kid, []byte(`{}`))
	resp, data = postJWS(t, challenge.URL, body)
	var challengeResult challengeView
	if err := json.Unmarshal(data, &challengeResult); err != nil {
		t.Fatal(err)
	}
	if challengeResult.Status != "valid" {
		t.Fatalf("IP HTTP-01 挑战失败: %s", data)
	}
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, orderURL, nonce, kid, []byte{})
	resp, data = postJWS(t, orderURL, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "ready" {
		t.Fatalf("IP 订单应 ready: %s", order.Status)
	}
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}, leafKey)
	payload, _ := json.Marshal(map[string]string{"csr": base64.RawURLEncoding.EncodeToString(csrDER)})
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, order.Finalize, nonce, kid, payload)
	resp, data = postJWS(t, order.Finalize, body)
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Status != "valid" || order.Certificate == "" {
		t.Fatalf("IP 订单签发失败: %s %s", order.Status, data)
	}
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, order.Certificate, nonce, kid, []byte{})
	req, _ := http.NewRequest(http.MethodPost, order.Certificate, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/jose+json")
	certResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer certResp.Body.Close()
	certPEM, _ := io.ReadAll(certResp.Body)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("IP 证书不是 PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("IP 证书 SAN 不正确: %v", leaf.IPAddresses)
	}
	_ = ca
}

func TestTLSALPNOrderChallenge(t *testing.T) {
	s, st, ep, ts, _ := newOrderTestServer(t)
	ep.TLSALPN01Enabled = true
	ep.TLSALPN01AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	if err := st.UpdateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	key, kid := createTestAccount(t, ts)
	thumbprint := accountThumbprint(t, key)

	orderTarget := ts.URL + "/acme/test/new-order"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, orderTarget, nonce, kid, []byte(`{"identifiers":[{"type":"dns","value":"localhost"}]}`))
	resp, data := postJWS(t, orderTarget, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建订单失败: %d %s", resp.StatusCode, data)
	}
	var order orderView
	_ = json.Unmarshal(data, &order)
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, resp.Header.Get("Location"), nonce, kid, []byte{})
	resp, data = postJWS(t, resp.Header.Get("Location"), body)
	_ = json.Unmarshal(data, &order)
	authzURL := order.Authorizations[0]
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, authzURL, nonce, kid, []byte{})
	resp, data = postJWS(t, authzURL, body)
	var authz authorizationView
	_ = json.Unmarshal(data, &authz)
	var challenge challengeView
	for _, item := range authz.Challenges {
		if item.Type == "tls-alpn-01" {
			challenge = item
		}
	}
	if challenge.URL == "" {
		t.Fatalf("授权中没有 TLS-ALPN-01 挑战: %+v", authz.Challenges)
	}
	port := startTLSALPNChallengeServer(t, "localhost", challenge.Token+"."+thumbprint)
	s.TLSALPN01.Port = port
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, challenge.URL, nonce, kid, []byte(`{}`))
	resp, data = postJWS(t, challenge.URL, body)
	var result challengeView
	_ = json.Unmarshal(data, &result)
	if result.Status != "valid" {
		t.Fatalf("TLS-ALPN-01 挑战失败: %s", data)
	}
}
