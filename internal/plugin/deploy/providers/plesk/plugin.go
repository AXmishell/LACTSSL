// Package plesk 通过 Plesk 的 HTTP 接口部署网站证书。
//
// 对应 certd 的「Plesk-部署Plesk网站证书」插件。先登录获取
// PLESKSESSID，再调用 sslit 模块上传一体化证书（证书 + 私钥）。
package plesk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "plesk" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployPlesk(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base     string
	username string
	password string
	session  string
	http     *http.Client
}

type domain struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var forgeryTokenPattern = regexp.MustCompile(`forgery_protection_token" content="([^"]+)"`)

func deployPlesk(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["url"])
	if err != nil {
		return err
	}
	username := strings.TrimSpace(cfg["username"])
	if username == "" || cfg["password"] == "" {
		return i18n.E("service.deploy.plesk_credentials_required")
	}
	c := &client{
		base: base, username: username, password: cfg["password"],
		http: util.Client(cfg),
	}
	if err := c.login(); err != nil {
		return err
	}
	domains, err := c.resolveDomains(cfg["domains"], cert)
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.plesk_domains_empty")
	}
	var errs []string
	for _, item := range domains {
		if err := c.upload(item.ID, cert); err != nil {
			errs = append(errs, fmt.Sprintf("%d(%s): %s", item.ID, item.Name, err.Error()))
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *client) login() error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("login_name", c.username)
	_ = writer.WriteField("passwd", c.password)
	_ = writer.WriteField("locale_id", "default")
	if err := writer.Close(); err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req, err := http.NewRequest(http.MethodPost, util.JoinURL(c.base, "/login_up.php"), &body)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", c.base)

	client := *c.http
	// Plesk 登录成功返回 303 + Set-Cookie，不跟随跳转才能拿到会话。
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.plesk_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "PLESKSESSID" && cookie.Value != "" {
			c.session = cookie.Value
			return nil
		}
	}
	return i18n.E("service.deploy.plesk_login_failed", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
}

func (c *client) resolveDomains(raw string, cert *deploy.NativeCertificate) ([]domain, error) {
	list, err := c.listDomains()
	if err != nil {
		return nil, err
	}
	selected := util.SplitList(raw)
	if len(selected) == 0 {
		selected = util.Domains(cert)
	}
	if len(selected) == 0 {
		return nil, i18n.E("service.deploy.plesk_domains_empty")
	}
	seen := map[int64]bool{}
	out := make([]domain, 0, len(selected))
	for _, value := range selected {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			for _, item := range list {
				if item.ID == id && !seen[id] {
					seen[id] = true
					out = append(out, item)
				}
			}
			continue
		}
		matched := false
		for _, item := range list {
			if util.DomainMatch(item.Name, value) && !seen[item.ID] {
				seen[item.ID] = true
				out = append(out, item)
				matched = true
			}
		}
		if !matched {
			return nil, i18n.E("service.deploy.plesk_domain_not_found", "domain", value)
		}
	}
	return out, nil
}

func (c *client) listDomains() ([]domain, error) {
	req, err := http.NewRequest(http.MethodGet, util.JoinURL(c.base, "/api/v2/domains"), nil)
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.plesk_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, i18n.E("service.deploy.plesk_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	var list []domain
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, i18n.E("service.deploy.plesk_response_invalid", "error", err.Error())
	}
	return list, nil
}

func (c *client) upload(domainID int64, cert *deploy.NativeCertificate) error {
	detailURL := util.JoinURL(c.base, fmt.Sprintf("/modules/sslit/index.php/index/certificate/id/%d", domainID))
	req, err := http.NewRequest(http.MethodGet, detailURL, nil)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Cookie", "PLESKSESSID="+c.session)
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.plesk_request_failed", "error", outbound.SafeError(err))
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.plesk_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	match := forgeryTokenPattern.FindSubmatch(raw)
	if len(match) != 2 {
		return i18n.E("service.deploy.plesk_token_missing")
	}
	token := string(match[1])

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("id", strconv.FormatInt(domainID, 10))
	_ = writer.WriteField("fileName", "cert.pem")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="fileContent"; filename="cert.pem"`)
	header.Set("Content-Type", "application/x-pem-file")
	part, err := writer.CreatePart(header)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	one := strings.TrimSpace(cert.FullchainPEM) + "\n" + strings.TrimSpace(cert.KeyPEM) + "\n"
	if _, err := part.Write([]byte(one)); err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	if err := writer.Close(); err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}

	uploadURL := util.JoinURL(c.base, "/modules/sslit/index.php/index/upload/")
	req, err = http.NewRequest(http.MethodPost, uploadURL, &body)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", "PLESKSESSID="+c.session)
	req.Header.Set("Origin", c.base)
	req.Header.Set("Referer", detailURL)
	req.Header.Set("X-Forgery-Protection-Token", token)
	resp, err = c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.plesk_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.plesk_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return checkPleskUploadResponse(raw)
}

func checkPleskUploadResponse(raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var result struct {
		Status        string          `json:"status"`
		ActionMessage json.RawMessage `json:"actionMessages"`
		StatusMessage json.RawMessage `json:"statusMessages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return i18n.E("service.deploy.plesk_upload_failed", "body", util.Truncate(string(raw), 300))
	}
	if result.Status != "success" {
		return i18n.E("service.deploy.plesk_upload_failed", "body", util.Truncate(string(raw), 300))
	}
	return nil
}
