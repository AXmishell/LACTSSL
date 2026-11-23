// Package gcp 提供 Google Cloud service account JWT 与 OAuth2 token 获取。
//
// 只实现 DNS 插件需要的 JWT bearer flow，不依赖 google.golang.org/api。
package gcp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	defaultScope    = "https://www.googleapis.com/auth/ndev.clouddns.readwrite"
)

// ServiceAccount 是 service account JSON 的最小子集。
type ServiceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// TokenSource 缓存 OAuth2 access token。
type TokenSource struct {
	Account ServiceAccount
	Scope   string
	HTTP    *http.Client
	Now     func() time.Time

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// ParseServiceAccount 支持原始 JSON 或文件路径。
func ParseServiceAccount(raw string) (ServiceAccount, error) {
	raw = strings.TrimSpace(raw)
	var data []byte
	if raw == "" {
		return ServiceAccount{}, errors.New("gcp: service account is empty")
	}
	if strings.HasPrefix(raw, "{") {
		data = []byte(raw)
	} else {
		path := raw
		if strings.HasPrefix(path, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				path = home + strings.TrimPrefix(path, "~")
			}
		}
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return ServiceAccount{}, fmt.Errorf("gcp: read service account: %w", err)
		}
	}
	var account ServiceAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return ServiceAccount{}, fmt.Errorf("gcp: parse service account: %w", err)
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		return ServiceAccount{}, errors.New("gcp: service account is missing client_email or private_key")
	}
	return account, nil
}

// Token 返回有效 access token，过期前自动刷新。
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	if t.token != "" && now().Before(t.expiry.Add(-30*time.Second)) {
		return t.token, nil
	}

	scope := t.Scope
	if scope == "" {
		scope = defaultScope
	}
	tokenURL := strings.TrimSpace(t.Account.TokenURI)
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	if err := validateTokenURL(tokenURL); err != nil {
		return "", err
	}

	assertion, err := t.assertion(tokenURL, scope, now())
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	client := t.HTTP
	if client == nil {
		client = outbound.NewClient(30*time.Second, false)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gcp: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gcp: token request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("gcp: decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", errors.New("gcp: token response is missing access_token")
	}
	ttl := time.Duration(payload.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	t.token = payload.AccessToken
	t.expiry = now().Add(ttl)
	return t.token, nil
}

func (t *TokenSource) assertion(audience, scope string, now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   t.Account.ClientEmail,
		"scope": scope,
		"aud":   audience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)

	key, err := parsePrivateKey(t.Account.PrivateKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("gcp: sign JWT: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("gcp: invalid private key PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("gcp: private key is not RSA")
		}
		return rsaKey, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("gcp: unsupported private key format")
}

func validateTokenURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return errors.New("gcp: token_uri must be an https URL without userinfo")
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "oauth2.googleapis.com", "accounts.google.com":
		return nil
	default:
		return fmt.Errorf("gcp: unsupported token_uri host %q", parsed.Hostname())
	}
}
