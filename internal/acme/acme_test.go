package acme

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newTestACMEServer(t *testing.T, eab bool) (*Server, *store.Store, *model.ACMEEndpoint, *httptest.Server) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "acme.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-acme-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	ep := &model.ACMEEndpoint{
		PublicID:         "endpoint-public",
		Name:             "Test ACME",
		Slug:             "test",
		CAID:             1,
		Enabled:          true,
		EABRequired:      eab,
		HTTP01Enabled:    true,
		DNS01Enabled:     true,
		AllowedDomains:   []string{"example.com"},
		ValidityDays:     90,
		MaxOrdersPerHour: 60,
	}
	if err := st.CreateACMEEndpoint(ep); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, "")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	s.BaseURL = ts.URL
	return s, st, ep, ts
}

func newAccountKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signJWS(t *testing.T, key *ecdsa.PrivateKey, alg jose.SignatureAlgorithm, targetURL, nonce, kid string, payload []byte) []byte {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithHeader("nonce", nonce).WithHeader("url", targetURL)
	if kid == "" {
		jwk := jose.JSONWebKey{Key: key.Public()}
		opts = opts.WithHeader("jwk", jwk)
	} else {
		opts = opts.WithHeader("kid", kid)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(obj.FullSerialize())
}

func signEAB(t *testing.T, secret []byte, kid, targetURL string, accountKey *ecdsa.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(jose.JSONWebKey{Key: accountKey.Public()})
	if err != nil {
		t.Fatal(err)
	}
	opts := (&jose.SignerOptions{}).WithHeader("kid", kid).WithHeader("url", targetURL)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: secret}, opts)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(obj.FullSerialize())
}

func getNonce(t *testing.T, ts *httptest.Server, slug string) string {
	t.Helper()
	resp, err := http.Head(ts.URL + "/acme/" + slug + "/new-nonce")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new-nonce 状态不正确: %d", resp.StatusCode)
	}
	nonce := resp.Header.Get("Replay-Nonce")
	if nonce == "" {
		t.Fatal("new-nonce 未返回 Replay-Nonce")
	}
	return nonce
}

func postJWS(t *testing.T, url string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, data
}

func TestBaseURLRejectsMalformedHost(t *testing.T) {
	s := NewServer(nil, "")
	request := &http.Request{Host: "evil.example\r\nX-Injected: 1"}
	got := s.baseURL(request)
	if strings.ContainsAny(got, "\r\n") || strings.Contains(got, "evil.example") {
		t.Fatalf("非法 Host 不应进入资源 URL: %q", got)
	}
	for _, host := range []string{"example.com", "example.com:8443", "127.0.0.1:14000", "[::1]:14000"} {
		if !validRequestHost(host) {
			t.Fatalf("合法 Host 被拒绝: %s", host)
		}
	}
	for _, host := range []string{"", "example.com/path", "user@example.com", "example.com:abc", "example.com:70000"} {
		if validRequestHost(host) {
			t.Fatalf("非法 Host 应被拒绝: %s", host)
		}
	}
}

func TestDirectoryAndNonce(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	resp, err := http.Get(ts.URL + "/acme/test/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("directory 状态不正确: %d", resp.StatusCode)
	}
	var dir map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&dir); err != nil {
		t.Fatal(err)
	}
	if dir["newNonce"] != ts.URL+"/acme/test/new-nonce" {
		t.Fatalf("newNonce 地址不正确: %v", dir["newNonce"])
	}
	meta := dir["meta"].(map[string]any)
	if meta["externalAccountRequired"] != false {
		t.Fatalf("EAB 开关未反映到 directory: %v", meta)
	}
	if getNonce(t, ts, "test") == "" {
		t.Fatal("nonce 为空")
	}
}

func TestDirectoryReportsEABRequired(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, true)
	resp, err := http.Get(ts.URL + "/acme/test/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var dir map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&dir); err != nil {
		t.Fatal(err)
	}
	meta := dir["meta"].(map[string]any)
	if meta["externalAccountRequired"] != true {
		t.Fatalf("EAB 开关未反映到 directory: %v", meta)
	}
}

func TestNewAccountAndPostAsGet(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	key := newAccountKey(t)
	nonce := getNonce(t, ts, "test")
	target := ts.URL + "/acme/test/new-account"
	body := signJWS(t, key, jose.ES256, target, nonce, "", []byte(`{"contact":["mailto:user@example.com"]}`))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建账户失败: %d %s", resp.StatusCode, data)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("创建账户未返回 Location")
	}
	if resp.Header.Get("Replay-Nonce") == "" {
		t.Fatal("创建账户未返回新的 Replay-Nonce")
	}

	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, key, jose.ES256, location, nonce, location, []byte{})
	resp, data = postJWS(t, location, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询账户失败: %d %s", resp.StatusCode, data)
	}
	var account accountView
	if err := json.Unmarshal(data, &account); err != nil {
		t.Fatal(err)
	}
	if account.Status != "valid" || len(account.Contact) != 1 {
		t.Fatalf("账户内容不正确: %+v", account)
	}

	// 同一个 nonce 重放必须返回 badNonce。
	resp, data = postJWS(t, location, body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "badNonce") {
		t.Fatalf("nonce 重放未被拒绝: %d %s", resp.StatusCode, data)
	}
}

func TestNewAccountRequiresEAB(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, true)
	key := newAccountKey(t)
	target := ts.URL + "/acme/test/new-account"
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, "", []byte(`{}`))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "externalAccountRequired") {
		t.Fatalf("缺少 EAB 未被拒绝: %d %s", resp.StatusCode, data)
	}
}

func TestNewAccountWithEABAndOneTimeUse(t *testing.T) {
	_, st, ep, ts := newTestACMEServer(t, true)
	secret := bytes.Repeat([]byte{0x42}, 32)
	credential := &model.ACMEEABCredential{
		PublicID:   "eab-public",
		EndpointID: ep.ID,
		Kid:        "eab_test",
		Label:      "test",
		HMAC:       base64.RawURLEncoding.EncodeToString(secret),
	}
	if err := st.CreateACMEEABCredential(credential); err != nil {
		t.Fatal(err)
	}
	key := newAccountKey(t)
	target := ts.URL + "/acme/test/new-account"
	eab := signEAB(t, secret, credential.Kid, target, key)
	payload, _ := json.Marshal(map[string]any{
		"contact":                []string{"mailto:eab@example.com"},
		"externalAccountBinding": json.RawMessage(eab),
	})
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, "", payload)
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("EAB 注册失败: %d %s", resp.StatusCode, data)
	}

	otherKey := newAccountKey(t)
	eab = signEAB(t, secret, credential.Kid, target, otherKey)
	payload, _ = json.Marshal(map[string]any{"externalAccountBinding": json.RawMessage(eab)})
	nonce = getNonce(t, ts, "test")
	body = signJWS(t, otherKey, jose.ES256, target, nonce, "", payload)
	resp, data = postJWS(t, target, body)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(data), "unauthorized") {
		t.Fatalf("一次性 EAB 被重复使用: %d %s", resp.StatusCode, data)
	}
}

func TestJWSURLMismatch(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	key := newAccountKey(t)
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, ts.URL+"/acme/test/other", nonce, "", []byte(`{}`))
	resp, data := postJWS(t, ts.URL+"/acme/test/new-account", body)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(data), "unauthorized") {
		t.Fatalf("JWS url 不匹配未被拒绝: %d %s", resp.StatusCode, data)
	}
}

func TestHandlerCanBeMountedOnMainMux(t *testing.T) {
	s, _, _, _ := newTestACMEServer(t, false)
	root := http.NewServeMux()
	root.Handle("/acme/", s.Handler())
	root.Handle("/", http.NotFoundHandler())
	ts := httptest.NewServer(root)
	t.Cleanup(ts.Close)
	s.BaseURL = ts.URL

	resp, err := http.Get(ts.URL + "/acme/test/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("挂载到主 mux 后 directory 应返回 200: %d", resp.StatusCode)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	resp, err := http.Get(ts.URL + "/acme/missing/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知端点应返回 404: %d", resp.StatusCode)
	}
}
