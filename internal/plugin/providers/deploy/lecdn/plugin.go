// Package lecdn 通过 LeCDN HTTP API 上传证书并绑定站点域名。
//
// 对应 ALLinSSL 的 lecdn 部署目标。
package lecdn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "lecdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployLeCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base  string
	token string
	http  *http.Client
}

type response struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func deployLeCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["url"])
	if err != nil {
		return err
	}
	username := strings.TrimSpace(cfg["username"])
	if username == "" || cfg["password"] == "" {
		return i18n.E("service.deploy.lecdn_credentials_required")
	}
	domain := strings.TrimSpace(cfg["domain"])
	if domain == "" {
		domain = util.FirstDomain(cert)
	}
	if domain == "" {
		return i18n.E("service.deploy.lecdn_domain_required")
	}
	siteID, err := strconv.Atoi(strings.TrimSpace(cfg["site_id"]))
	if err != nil || siteID <= 0 {
		return i18n.E("service.deploy.lecdn_site_required")
	}
	c := &client{base: base, http: util.Client(cfg)}
	if err := c.login(username, cfg["password"]); err != nil {
		return err
	}
	certID, err := c.ensureCertificate(cert)
	if err != nil {
		return err
	}
	domainID, err := c.domainID(siteID, domain)
	if err != nil {
		return err
	}
	return c.deploy(siteID, domainID, certID)
}

func (c *client) login(username, password string) error {
	var result response
	if err := c.do(http.MethodPost, "/prod-api/login", map[string]any{
		"username": username, "password": password,
	}, &result); err != nil {
		return i18n.E("service.deploy.lecdn_login_failed", "error", err.Error())
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil || data.Token == "" {
		return i18n.E("service.deploy.lecdn_login_failed", "error", "token missing")
	}
	c.token = data.Token
	return nil
}

func (c *client) ensureCertificate(cert *deploy.NativeCertificate) (int, error) {
	sum := sha256.Sum256([]byte(cert.FullchainPEM))
	name := "lactssl-" + hex.EncodeToString(sum[:])
	var list response
	if err := c.do(http.MethodGet, "/prod-api/certificate?current_page=1&total=9999&page_size=9999", nil, &list); err == nil {
		var items struct {
			Items []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		_ = json.Unmarshal(list.Data, &items)
		for _, item := range items.Items {
			if item.Name == name {
				return item.ID, nil
			}
		}
	}
	var uploaded response
	payload := map[string]any{
		"ssl_key":      base64.StdEncoding.EncodeToString([]byte(cert.KeyPEM)),
		"ssl_pem":      base64.StdEncoding.EncodeToString([]byte(cert.FullchainPEM)),
		"type":         "upload",
		"auto_renewal": false,
		"name":         name,
	}
	if err := c.do(http.MethodPost, "/prod-api/certificate", payload, &uploaded); err != nil {
		return 0, err
	}
	var data struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(uploaded.Data, &data); err != nil || data.ID <= 0 {
		return 0, i18n.E("service.deploy.lecdn_cert_id_missing")
	}
	return data.ID, nil
}

func (c *client) domainID(siteID int, domain string) (int, error) {
	var result response
	if err := c.do(http.MethodGet, fmt.Sprintf("/prod-api/site/%d/domain_name", siteID), nil, &result); err != nil {
		return 0, err
	}
	var list []struct {
		ID         int    `json:"id"`
		DomainName string `json:"domain_name"`
	}
	if err := json.Unmarshal(result.Data, &list); err != nil {
		return 0, i18n.E("service.deploy.lecdn_response_invalid", "error", err.Error())
	}
	for _, item := range list {
		if item.DomainName == domain {
			return item.ID, nil
		}
	}
	return 0, i18n.E("service.deploy.lecdn_domain_not_found", "domain", domain)
}

func (c *client) deploy(siteID, domainID, certID int) error {
	return c.do(http.MethodPut, fmt.Sprintf("/prod-api/site/%d/domain_name/certificate", siteID), map[string]any{
		"certificate_enable": true,
		"certificate_id":     certID,
		"domain_name_id":     domainID,
	}, nil)
}

func (c *client) do(method, path string, payload any, out any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
	}
	req, err := http.NewRequest(method, util.JoinURL(c.base, path), bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("cookie", "LeCDN-Client="+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.lecdn_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		return i18n.E("service.deploy.lecdn_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 200 {
		if result.Msg != "" {
			return i18n.E("service.deploy.lecdn_error", "status", resp.StatusCode, "message", result.Msg)
		}
		return i18n.E("service.deploy.lecdn_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return i18n.E("service.deploy.lecdn_response_invalid", "error", err.Error())
	}
	return nil
}
