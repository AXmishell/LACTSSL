// Package cdnfly 通过 CDNfly 管理 API 更新站点证书。
//
// 对应 certd 的「cdnfly-部署证书到cdnfly」插件。
package cdnfly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "cdnfly" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	return deployCDNfly(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.Register(plugin{}) }

type client struct {
	base      string
	authType  string
	apiKey    string
	apiSecret string
	username  string
	password  string
	token     string
	http      *http.Client
}

type envelope struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type site struct {
	ID          int64           `json:"id"`
	Domain      string          `json:"domain"`
	HTTPSListen json.RawMessage `json:"https_listen"`
}

type httpsListen struct {
	Cert any `json:"cert"`
}

func deployCDNfly(cert *deploy.Certificate, cfg map[string]string) error {
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	var errs []string
	certIDs := util.SplitList(cfg["cert_ids"])
	for _, raw := range certIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return i18n.E("service.deploy.cdnfly_cert_id_invalid", "value", raw)
		}
		if err := c.updateCert(id, cert); err != nil {
			errs = append(errs, raw+": "+err.Error())
		}
	}

	var sites []site
	if len(certIDs) == 0 || len(util.SplitList(cfg["sites"])) > 0 {
		sites, err = c.resolveSites(cfg["sites"], cert)
		if err != nil {
			return err
		}
	}
	if len(sites) == 0 && len(certIDs) == 0 {
		return i18n.E("service.deploy.cdnfly_sites_empty")
	}
	for _, item := range sites {
		if err := c.updateSite(item, cert, cfg["cert_name"]); err != nil {
			errs = append(errs, fmt.Sprintf("%d(%s): %s", item.ID, item.Domain, err.Error()))
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func newClient(cfg map[string]string) (*client, error) {
	base, err := util.BaseURL(cfg["url"])
	if err != nil {
		return nil, err
	}
	authType := strings.ToLower(strings.TrimSpace(cfg["auth_type"]))
	if authType == "" {
		authType = "apikey"
	}
	c := &client{
		base: base, authType: authType,
		apiKey: strings.TrimSpace(cfg["api_key"]), apiSecret: cfg["api_secret"],
		username: strings.TrimSpace(cfg["username"]), password: cfg["password"],
		http: util.Client(cfg),
	}
	switch authType {
	case "apikey":
		if c.apiKey == "" || c.apiSecret == "" {
			return nil, i18n.E("service.deploy.cdnfly_key_required")
		}
	case "password":
		if c.username == "" || c.password == "" {
			return nil, i18n.E("service.deploy.cdnfly_password_required")
		}
	default:
		return nil, i18n.E("service.deploy.cdnfly_auth_invalid", "type", authType)
	}
	return c, nil
}

func (c *client) do(method, path string, query map[string]string, payload any) (json.RawMessage, error) {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
	}
	endpoint := util.JoinURL(c.base, path)
	if len(query) > 0 {
		endpoint += "?" + util.RawQuery(query)
	}
	headers := map[string]string{}
	if c.authType == "password" {
		if err := c.login(); err != nil {
			return nil, err
		}
		headers["Access-Token"] = c.token
	} else {
		headers["api-key"] = c.apiKey
		headers["api-secret"] = c.apiSecret
	}
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.cdnfly_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, i18n.E("service.deploy.cdnfly_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code == nil || *result.Code != 0 {
		if result.Msg != "" {
			return nil, i18n.E("service.deploy.cdnfly_error", "status", resp.StatusCode, "message", result.Msg)
		}
		return nil, i18n.E("service.deploy.cdnfly_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return result.Data, nil
}

func (c *client) login() error {
	if c.token != "" {
		return nil
	}
	data, err := c.doRaw(http.MethodPost, "/v1/login", map[string]any{
		"account":  c.username,
		"password": c.password,
	})
	if err != nil {
		return err
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.AccessToken == "" {
		return i18n.E("service.deploy.cdnfly_login_failed")
	}
	c.token = result.AccessToken
	return nil
}

// doRaw 用于登录请求，避免 login 自身再次调用 login 造成递归。
func (c *client) doRaw(method, path string, payload any) (json.RawMessage, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(method, util.JoinURL(c.base, path), bytes.NewReader(body))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.cdnfly_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, i18n.E("service.deploy.cdnfly_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code == nil || *result.Code != 0 {
		if result.Msg != "" {
			return nil, i18n.E("service.deploy.cdnfly_error", "status", resp.StatusCode, "message", result.Msg)
		}
		return nil, i18n.E("service.deploy.cdnfly_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return result.Data, nil
}

func (c *client) listSites() ([]site, error) {
	all := make([]site, 0)
	seen := make(map[int64]bool)
	for page := 1; page <= 20; page++ {
		data, err := c.do(http.MethodGet, "/v1/sites", map[string]string{
			"limit": "100", "page": strconv.Itoa(page),
		}, nil)
		if err != nil {
			return nil, err
		}
		var sites []site
		if err := json.Unmarshal(data, &sites); err != nil {
			return nil, i18n.E("service.deploy.cdnfly_response_invalid", "error", err.Error())
		}
		added := 0
		for _, item := range sites {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			all = append(all, item)
			added++
		}
		// 空页、短页或 API 忽略 page 参数时停止，避免重复请求。
		if len(sites) < 100 || added == 0 {
			break
		}
	}
	return all, nil
}

func (c *client) resolveSites(raw string, cert *deploy.Certificate) ([]site, error) {
	sites, err := c.listSites()
	if err != nil {
		return nil, err
	}
	selected := util.SplitList(raw)
	if len(selected) == 0 {
		selected = util.Domains(cert)
	}
	if len(selected) == 0 {
		return nil, i18n.E("service.deploy.cdnfly_sites_empty")
	}
	seen := map[int64]bool{}
	out := make([]site, 0, len(selected))
	for _, value := range selected {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			for _, item := range sites {
				if item.ID == id && !seen[id] {
					seen[id] = true
					out = append(out, item)
				}
			}
			continue
		}
		matched := false
		for _, item := range sites {
			if util.MatchAny([]string{value}, strings.Fields(item.Domain)) && !seen[item.ID] {
				seen[item.ID] = true
				out = append(out, item)
				matched = true
			}
		}
		if !matched {
			return nil, i18n.E("service.deploy.cdnfly_site_not_found", "site", value)
		}
	}
	return out, nil
}

func (c *client) updateCert(id int64, cert *deploy.Certificate) error {
	_, err := c.do(http.MethodPut, fmt.Sprintf("/v1/certs/%d", id), nil, map[string]any{
		"cert": cert.FullchainPEM,
		"key":  cert.KeyPEM,
	})
	return err
}

func (c *client) updateSite(item site, cert *deploy.Certificate, certName string) error {
	// 站点详情里的 https_listen 可能是对象，也可能是 JSON 字符串。
	detail := site{ID: item.ID, Domain: item.Domain, HTTPSListen: item.HTTPSListen}
	if data, err := c.do(http.MethodGet, fmt.Sprintf("/v1/sites/%d", item.ID), nil, nil); err == nil && len(data) > 0 {
		var raw struct {
			ID          int64           `json:"id"`
			Domain      string          `json:"domain"`
			HTTPSListen json.RawMessage `json:"https_listen"`
		}
		if json.Unmarshal(data, &raw) == nil {
			if raw.ID > 0 {
				detail.ID = raw.ID
			}
			if raw.Domain != "" {
				detail.Domain = raw.Domain
			}
			detail.HTTPSListen = raw.HTTPSListen
		}
	}

	if certID := parseCertID(detail.HTTPSListen); certID > 0 {
		return c.updateCert(certID, cert)
	}
	name := strings.TrimSpace(certName)
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	certID, err := c.createCert(name, cert)
	if err != nil {
		return err
	}
	_, err = c.do(http.MethodPut, "/v1/sites", nil, map[string]any{
		"id": detail.ID,
		"https_listen": map[string]any{
			"cert": certID,
		},
	})
	return err
}

func (c *client) createCert(name string, cert *deploy.Certificate) (int64, error) {
	data, err := c.do(http.MethodPost, "/v1/certs", nil, map[string]any{
		"name": name,
		"type": "custom",
		"cert": cert.FullchainPEM,
		"key":  cert.KeyPEM,
	})
	if err != nil {
		return 0, err
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(data, &created) == nil && created.ID > 0 {
		return created.ID, nil
	}
	certs, err := c.do(http.MethodGet, "/v1/certs", map[string]string{"name": name}, nil)
	if err != nil {
		return 0, err
	}
	var list []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(certs, &list); err != nil {
		return 0, i18n.E("service.deploy.cdnfly_response_invalid", "error", err.Error())
	}
	if len(list) == 0 || list[0].ID <= 0 {
		return 0, i18n.E("service.deploy.cdnfly_cert_id_missing")
	}
	return list[0].ID, nil
}

func parseCertID(raw json.RawMessage) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			var nested httpsListen
			if json.Unmarshal([]byte(text), &nested) == nil {
				return toInt64(nested.Cert)
			}
		}
		return 0
	}
	var listen httpsListen
	if json.Unmarshal(raw, &listen) == nil {
		return toInt64(listen.Cert)
	}
	return 0
}

func toInt64(value any) int64 {
	switch item := value.(type) {
	case float64:
		return int64(item)
	case int64:
		return item
	case json.Number:
		number, _ := item.Int64()
		return number
	case string:
		number, _ := strconv.ParseInt(item, 10, 64)
		return number
	default:
		return 0
	}
}
