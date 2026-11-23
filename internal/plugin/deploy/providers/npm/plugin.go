// Package npm 通过 Nginx Proxy Manager 管理 API 上传自定义证书并绑定代理主机。
//
// 对应 certd 的「Nginx Proxy Manager-部署到主机」插件。
package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/otp"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "nginx_proxy_manager" }
func (plugin) Aliases() []string { return []string{"npm"} }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployNPM(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type npmClient struct {
	base   string
	email  string
	secret string
	totp   string
	http   *http.Client
	token  string
}

type npmTokenResponse struct {
	Token          string `json:"token"`
	Requires2FA    bool   `json:"requires_2fa"`
	ChallengeToken string `json:"challenge_token"`
}

type npmCertificate struct {
	ID        int      `json:"id"`
	Provider  string   `json:"provider"`
	NiceName  string   `json:"nice_name"`
	Domain    []string `json:"domain_names"`
	Proxy     []any    `json:"proxy_hosts"`
	Redirect  []any    `json:"redirection_hosts"`
	Dead      []any    `json:"dead_hosts"`
	Streams   []any    `json:"streams"`
	IsDeleted bool     `json:"is_deleted"`
}

type npmProxyHost struct {
	ID             int      `json:"id"`
	DomainNames    []string `json:"domain_names"`
	CertificateID  int      `json:"certificate_id"`
	Enabled        bool     `json:"enabled"`
	ForwardScheme  string   `json:"forward_scheme"`
	ForwardHost    string   `json:"forward_host"`
	ForwardPort    int      `json:"forward_port"`
	AdvancedConfig string   `json:"advanced_config"`
}

func deployNPM(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["panel_url"])
	if err != nil {
		return err
	}
	email := strings.TrimSpace(cfg["email"])
	secret := cfg["password"]
	if email == "" || secret == "" {
		return i18n.E("service.deploy.npm_credentials_required")
	}
	client := &npmClient{
		base: base, email: email, secret: secret,
		totp: strings.TrimSpace(cfg["totp_secret"]),
		http: util.Client(cfg),
	}
	if err := client.login(); err != nil {
		return err
	}

	label := strings.TrimSpace(cfg["certificate_label"])
	if label == "" {
		label = "lactssl_" + util.CleanName(util.FirstDomain(cert))
	}
	certificate, err := client.ensureCertificate(label, util.Domains(cert))
	if err != nil {
		return err
	}
	if err := client.uploadCertificate(certificate.ID, cert); err != nil {
		return err
	}

	hosts, err := client.resolveHosts(cfg["proxy_hosts"], cert)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return i18n.E("service.deploy.npm_hosts_empty")
	}
	var errs []string
	for _, host := range hosts {
		if err := client.assignCertificate(host.ID, certificate.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s(#%d): %s", strings.Join(host.DomainNames, ","), host.ID, err.Error()))
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *npmClient) login() error {
	initial, err := c.requestToken(c.base+"/api/tokens", map[string]any{
		"identity": c.email,
		"secret":   c.secret,
	})
	if err != nil {
		return err
	}
	if initial.Token != "" {
		c.token = initial.Token
		return nil
	}
	if !initial.Requires2FA || initial.ChallengeToken == "" {
		return i18n.E("service.deploy.npm_login_failed")
	}
	if c.totp == "" {
		return i18n.E("service.deploy.npm_totp_required")
	}
	code, err := otp.Code(c.totp, time.Now())
	if err != nil {
		return i18n.E("service.deploy.npm_totp_invalid", "error", err.Error())
	}
	completed, err := c.requestToken(c.base+"/api/tokens/2fa", map[string]any{
		"challenge_token": initial.ChallengeToken,
		"code":            code,
	})
	if err != nil {
		return err
	}
	if completed.Token == "" {
		return i18n.E("service.deploy.npm_login_failed")
	}
	c.token = completed.Token
	return nil
}

func (c *npmClient) requestToken(endpoint string, payload any) (npmTokenResponse, error) {
	var result npmTokenResponse
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return result, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return result, i18n.E("service.deploy.npm_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, i18n.E("service.deploy.npm_status", "status", resp.StatusCode, "body", util.Truncate(string(body), 300))
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return result, i18n.E("service.deploy.npm_status", "status", resp.StatusCode, "body", util.Truncate(string(body), 300))
	}
	return result, nil
}

func (c *npmClient) do(method, path string, payload any, out any) error {
	var reader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, util.JoinURL(c.base, path), reader)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.npm_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.npm_status", "status", resp.StatusCode, "body", util.Truncate(string(body), 300))
	}
	if out != nil && len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return i18n.E("service.deploy.npm_status", "status", resp.StatusCode, "body", util.Truncate(string(body), 300))
		}
	}
	return nil
}

func (c *npmClient) ensureCertificate(label string, domains []string) (npmCertificate, error) {
	var list []npmCertificate
	if err := c.do(http.MethodGet, "/api/nginx/certificates?query="+url.QueryEscape(label), nil, &list); err != nil {
		return npmCertificate{}, err
	}
	for _, item := range list {
		if item.Provider == "other" && item.NiceName == label {
			return item, nil
		}
	}
	payload := map[string]any{
		"provider":     "other",
		"nice_name":    label,
		"domain_names": domains,
	}
	var created npmCertificate
	if err := c.do(http.MethodPost, "/api/nginx/certificates", payload, &created); err != nil {
		return npmCertificate{}, err
	}
	return created, nil
}

func (c *npmClient) uploadCertificate(id int, cert *deploy.NativeCertificate) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	add := func(field, filename, content string) error {
		if strings.TrimSpace(content) == "" {
			return nil
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename))
		header.Set("Content-Type", "application/x-pem-file")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		_, err = part.Write([]byte(content))
		return err
	}
	if err := add("certificate", "fullchain.pem", cert.FullchainPEM); err != nil {
		return err
	}
	if err := add("certificate_key", "privkey.pem", cert.KeyPEM); err != nil {
		return err
	}
	if err := add("intermediate_certificate", "chain.pem", util.ChainPEM(cert.FullchainPEM)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, util.JoinURL(c.base, fmt.Sprintf("/api/nginx/certificates/%d/upload", id)), &body)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.npm_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.npm_status", "status", resp.StatusCode, "body", util.Truncate(string(data), 300))
	}
	return nil
}

func (c *npmClient) resolveHosts(raw string, cert *deploy.NativeCertificate) ([]npmProxyHost, error) {
	var hosts []npmProxyHost
	if err := c.do(http.MethodGet, "/api/nginx/proxy-hosts?expand=certificate", nil, &hosts); err != nil {
		return nil, err
	}
	selected := util.SplitList(raw)
	if len(selected) == 0 {
		domains := util.Domains(cert)
		out := make([]npmProxyHost, 0, len(hosts))
		for _, host := range hosts {
			if util.MatchAny(domains, host.DomainNames) {
				out = append(out, host)
			}
		}
		return out, nil
	}

	byID := make(map[int]npmProxyHost, len(hosts))
	for _, host := range hosts {
		byID[host.ID] = host
	}
	seen := map[int]bool{}
	out := make([]npmProxyHost, 0, len(selected))
	for _, item := range selected {
		if id, err := strconv.Atoi(item); err == nil && id > 0 {
			if host, ok := byID[id]; ok && !seen[id] {
				seen[id] = true
				out = append(out, host)
				continue
			}
			// 允许直接使用 ID，即使列表接口没有返回主机详情。
			if !seen[id] {
				seen[id] = true
				out = append(out, npmProxyHost{ID: id})
				continue
			}
			continue
		}
		matched := false
		for _, host := range hosts {
			if util.MatchAny([]string{item}, host.DomainNames) && !seen[host.ID] {
				seen[host.ID] = true
				out = append(out, host)
				matched = true
			}
		}
		if !matched {
			return nil, i18n.E("service.deploy.npm_host_not_found", "host", item)
		}
	}
	return out, nil
}

func (c *npmClient) assignCertificate(hostID, certificateID int) error {
	payload := map[string]any{"certificate_id": certificateID}
	return c.do(http.MethodPut, fmt.Sprintf("/api/nginx/proxy-hosts/%d", hostID), payload, nil)
}
