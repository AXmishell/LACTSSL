// Package proxmox 通过 Proxmox VE API 上传自定义节点证书。
//
// 对应 certd 的「Proxmox-上传证书到Proxmox」插件。
package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "proxmox" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployProxmox(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base   string
	ticket string
	csrf   string
	http   *http.Client
}

func deployProxmox(cert *deploy.NativeCertificate, cfg map[string]string) error {
	host := strings.TrimSpace(cfg["host"])
	username := strings.TrimSpace(cfg["username"])
	if host == "" || username == "" || cfg["password"] == "" {
		return i18n.E("service.deploy.proxmox_credentials_required")
	}
	if !strings.Contains(host, "://") {
		port := util.Int(cfg["port"], 8006)
		host = "https://" + strings.TrimRight(host, "/") + ":" + strconv.Itoa(port)
	}
	base, err := util.BaseURL(host)
	if err != nil {
		return err
	}
	nodes := util.SplitList(cfg["nodes"])
	if len(nodes) == 0 {
		return i18n.E("service.deploy.proxmox_nodes_required")
	}
	c := &client{base: base, http: util.Client(cfg)}
	if err := c.login(username, cfg["password"], cfg["realm"]); err != nil {
		return err
	}
	var errs []string
	for _, node := range nodes {
		if err := c.upload(node, cert); err != nil {
			errs = append(errs, node+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *client) login(username, password, realm string) error {
	realm = strings.TrimSpace(realm)
	if realm == "" {
		realm = "pam"
	}
	if !strings.Contains(username, "@") {
		username += "@" + realm
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	req, err := http.NewRequest(http.MethodPost, util.JoinURL(c.base, "/api2/json/access/ticket"), strings.NewReader(form.Encode()))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.proxmox_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.proxmox_login_failed", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	var result struct {
		Data struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Data.Ticket == "" {
		return i18n.E("service.deploy.proxmox_login_failed", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	c.ticket = result.Data.Ticket
	c.csrf = result.Data.CSRF
	return nil
}

func (c *client) upload(node string, cert *deploy.NativeCertificate) error {
	form := url.Values{}
	form.Set("certificates", cert.FullchainPEM)
	form.Set("key", cert.KeyPEM)
	form.Set("force", "1")
	form.Set("restart", "1")
	endpoint := util.JoinURL(c.base, fmt.Sprintf("/api2/json/nodes/%s/certificates/custom", url.PathEscape(node)))
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "PVEAuthCookie="+c.ticket)
	if c.csrf != "" {
		req.Header.Set("CSRFPreventionToken", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.proxmox_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.proxmox_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	if !json.Valid(raw) {
		return i18n.E("service.deploy.proxmox_response_invalid", "error", "empty or invalid JSON response")
	}
	return nil
}
