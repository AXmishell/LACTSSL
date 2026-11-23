// Package azure 提供 Azure AD client credentials 与 ARM 端点。
package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Environment 描述 Azure 云环境端点。
type Environment struct {
	ManagementBaseURL string
	LoginBaseURL      string
	Scope             string
}

// EnvironmentByName 返回 catalog 中 azure_environment 对应的端点。
func EnvironmentByName(name string) (Environment, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "public":
		return Environment{
			ManagementBaseURL: "https://management.azure.com",
			LoginBaseURL:      "https://login.microsoftonline.com",
			Scope:             "https://management.azure.com/.default",
		}, nil
	case "china":
		return Environment{
			ManagementBaseURL: "https://management.chinacloudapi.cn",
			LoginBaseURL:      "https://login.chinacloudapi.cn",
			Scope:             "https://management.core.chinacloudapi.cn/.default",
		}, nil
	case "usgovernment":
		return Environment{
			ManagementBaseURL: "https://management.usgovcloudapi.net",
			LoginBaseURL:      "https://login.microsoftonline.us",
			Scope:             "https://management.core.usgovcloudapi.net/.default",
		}, nil
	default:
		return Environment{}, fmt.Errorf("azure: unknown environment %q", name)
	}
}

// TokenSource 使用 client credentials 获取 ARM token。
type TokenSource struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	Environment  Environment
	HTTP         *http.Client
	Now          func() time.Time

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// Token 返回有效 access token。
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
	if t.TenantID == "" || t.ClientID == "" || t.ClientSecret == "" {
		return "", errors.New("azure: tenant_id, client_id and client_secret are required")
	}
	env := t.Environment
	if env.LoginBaseURL == "" {
		env, _ = EnvironmentByName("public")
	}
	endpoint := strings.TrimRight(env.LoginBaseURL, "/") + "/" + url.PathEscape(t.TenantID) + "/oauth2/v2.0/token"
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {t.ClientID},
		"client_secret": {t.ClientSecret},
		"scope":         {env.Scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := t.HTTP
	if client == nil {
		client = outbound.NewClient(30*time.Second, false)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("azure: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("azure: token request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("azure: decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", errors.New("azure: token response is missing access_token")
	}
	ttl := time.Duration(payload.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	t.token = payload.AccessToken
	t.expiry = now().Add(ttl)
	return t.token, nil
}
