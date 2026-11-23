// Package upyun 通过又拍云控制台 API 上传证书并部署到 CDN/USS 域名。
//
// 对应 certd 的「又拍云-部署证书到CDN/USS」插件。
package upyun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "upyun_cdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	return deployUpyun(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.Register(plugin{}) }

var upyunBaseURL = "https://console.upyun.com"

type client struct {
	base     string
	username string
	password string
	cookie   string
	http     *http.Client
}

type apiResponse struct {
	Msg  json.RawMessage `json:"msg"`
	Data struct {
		ErrorCode int    `json:"error_code"`
		Message   string `json:"message"`
		Result    struct {
			CertificateID util.FlexInt64 `json:"certificate_id"`
		} `json:"result"`
	} `json:"data"`
}

func deployUpyun(cert *deploy.Certificate, cfg map[string]string) error {
	username := strings.TrimSpace(cfg["username"])
	password := cfg["password"]
	if username == "" || password == "" {
		return i18n.E("service.deploy.upyun_credentials_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	c := &client{base: upyunBaseURL, username: username, password: password, http: util.Client(cfg)}
	if err := c.login(); err != nil {
		return err
	}
	certID, err := c.uploadCertificate(cert)
	if err != nil {
		return err
	}
	var errs []string
	for _, domain := range domains {
		if err := c.deployDomain(domain, certID, cfg["force_https"], cfg["https"]); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *client) login() error {
	status, headers, raw, err := c.post("/accounts/signin/", map[string]any{
		"username": c.username,
		"password": c.password,
	}, false)
	if err != nil {
		return err
	}
	cookie := strings.Join(headers.Values("Set-Cookie"), "; ")
	if status < 200 || status >= 300 || cookie == "" {
		return i18n.E("service.deploy.upyun_login_failed", "body", util.Truncate(string(raw), 300))
	}
	c.cookie = cookie
	return nil
}

func (c *client) uploadCertificate(cert *deploy.Certificate) (int64, error) {
	status, _, raw, err := c.post("/api/https/certificate/", map[string]any{
		"certificate": cert.FullchainPEM,
		"private_key": cert.KeyPEM,
	}, true)
	if err != nil {
		return 0, err
	}
	var result apiResponse
	_ = json.Unmarshal(raw, &result)
	if result.Data.ErrorCode != 0 {
		return 0, i18n.E("service.deploy.upyun_error", "code", result.Data.ErrorCode, "message", result.Data.Message)
	}
	id := result.Data.Result.CertificateID.Int64()
	if status < 200 || status >= 300 || id <= 0 {
		return 0, i18n.E("service.deploy.upyun_cert_id_missing", "body", util.Truncate(string(raw), 300))
	}
	return id, nil
}

func (c *client) deployDomain(domain string, certID int64, forceHTTPS, https string) error {
	status, _, raw, err := c.post("/api/https/migrate/domain", map[string]any{
		"crt_id":      certID,
		"domain_name": domain,
	}, true)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return i18n.E("service.deploy.upyun_status", "status", status, "body", util.Truncate(string(raw), 300))
	}
	payload := map[string]any{
		"certificate_id": certID,
		"domain":         domain,
	}
	if value := parseTriState(forceHTTPS); value != nil {
		payload["force_https"] = *value
	}
	if value := parseTriState(https); value != nil {
		payload["https"] = *value
	}
	status, _, raw, err = c.post("/api/https/certificate/manager", payload, true)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return i18n.E("service.deploy.upyun_status", "status", status, "body", util.Truncate(string(raw), 300))
	}
	return nil
}

func (c *client) post(path string, payload any, withCookie bool) (int, http.Header, []byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req, err := http.NewRequest(http.MethodPost, util.JoinURL(c.base, path), bytes.NewReader(data))
	if err != nil {
		return 0, nil, nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LACTSSL/1.0")
	if withCookie && c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, i18n.E("service.deploy.upyun_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result apiResponse
		if json.Unmarshal(raw, &result) == nil && result.Data.ErrorCode != 0 {
			return resp.StatusCode, resp.Header, raw, i18n.E("service.deploy.upyun_error", "code", result.Data.ErrorCode, "message", result.Data.Message)
		}
	}
	return resp.StatusCode, resp.Header, raw, nil
}

func parseTriState(value string) *bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "enable":
		result := true
		return &result
	case "false", "0", "off", "disable":
		result := false
		return &result
	default:
		return nil
	}
}
