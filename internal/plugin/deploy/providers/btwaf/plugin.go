// Package btwaf 通过宝塔 WAF API 为站点部署证书。
//
// 对应 ALLinSSL 的 btwaf-site 部署目标。
package btwaf

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "btwaf" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployBtWAF(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base   string
	apiKey string
	http   *http.Client
}

type response struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Res  json.RawMessage `json:"res"`
}

func deployBtWAF(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["url"])
	if err != nil {
		return err
	}
	apiKey := strings.TrimSpace(cfg["api_key"])
	if apiKey == "" {
		return i18n.E("service.deploy.btwaf_key_required")
	}
	sites := util.SplitList(cfg["sites"])
	if len(sites) == 0 {
		return i18n.E("service.deploy.btwaf_sites_required")
	}
	c := &client{base: base, apiKey: apiKey, http: util.Client(cfg)}
	var errs []string
	for _, site := range sites {
		if err := c.deploySite(site, cert); err != nil {
			errs = append(errs, site+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *client) request(path string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req, err := http.NewRequest(http.MethodPost, util.JoinURL(c.base, path), bytes.NewReader(body))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	req.Header.Set("waf_request_time", timestamp)
	req.Header.Set("waf_request_token", token(timestamp, c.apiKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.btwaf_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, i18n.E("service.deploy.btwaf_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code == nil || *result.Code != 0 {
		message := result.Msg
		if message == "" {
			message = util.Truncate(string(raw), 300)
		}
		return nil, i18n.E("service.deploy.btwaf_error", "status", resp.StatusCode, "message", message)
	}
	return result.Res, nil
}

func (c *client) deploySite(siteName string, cert *deploy.NativeCertificate) error {
	var listResponse struct {
		List []struct {
			SiteID   string `json:"site_id"`
			SiteName string `json:"site_name"`
		} `json:"list"`
	}
	data, err := c.request("/api/wafmastersite/get_site_list", map[string]any{
		"p": 1, "p_size": 100, "site_name": siteName,
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &listResponse); err != nil {
		return i18n.E("service.deploy.btwaf_response_invalid", "error", err.Error())
	}
	siteID := ""
	for _, item := range listResponse.List {
		if item.SiteName == siteName {
			siteID = item.SiteID
			break
		}
	}
	if siteID == "" {
		return i18n.E("service.deploy.btwaf_site_not_found", "site", siteName)
	}
	_, err = c.request("/api/wafmastersite/modify_site", map[string]any{
		"site_id": siteID,
		"types":   "openCert",
		"server": map[string]any{
			"listen_ssl_port": []string{"443"},
			"ssl": map[string]any{
				"full_chain":  cert.FullchainPEM,
				"private_key": cert.KeyPEM,
				"is_ssl":      1,
			},
		},
	})
	return err
}

func token(timestamp, apiKey string) string {
	keyHash := md5.Sum([]byte(apiKey))
	sign := md5.Sum([]byte(timestamp + hex.EncodeToString(keyHash[:])))
	return hex.EncodeToString(sign[:])
}
