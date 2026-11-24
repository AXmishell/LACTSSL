// Package synology 通过 Synology DSM Web API 更新证书。
//
// 对应 certd 的「群晖-部署证书到群晖面板」插件。使用 DSM 的
// SYNO.API.Auth 与 SYNO.Core.Certificate 接口，不依赖 SSH。
package synology

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
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "synology" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deploySynology(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base       string
	username   string
	password   string
	otpSecret  string
	deviceID   string
	deviceName string
	sid        string
	synoToken  string
	authPath   string
	authVer    int
	http       *http.Client
}

type authResponse struct {
	Success bool `json:"success"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error"`
	Data json.RawMessage `json:"data"`
}

type apiInfoResponse struct {
	Success bool `json:"success"`
	Data    map[string]struct {
		Path       string `json:"path"`
		MaxVersion int    `json:"maxVersion"`
	} `json:"data"`
}

type certificate struct {
	ID        string `json:"id"`
	Desc      string `json:"desc"`
	IsDefault bool   `json:"is_default"`
	Subject   struct {
		CommonName string `json:"common_name"`
	} `json:"subject"`
	Domains []string `json:"domains"`
}

type listResponse struct {
	Success bool `json:"success"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error"`
	Data struct {
		Certificates []certificate `json:"certificates"`
	} `json:"data"`
}

func deploySynology(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["panel_url"])
	if err != nil {
		return err
	}
	username := strings.TrimSpace(cfg["username"])
	if username == "" || cfg["password"] == "" {
		return i18n.E("service.deploy.synology_credentials_required")
	}
	c := &client{
		base: base, username: username, password: cfg["password"],
		otpSecret: strings.TrimSpace(cfg["otp_secret"]),
		deviceID:  strings.TrimSpace(cfg["device_id"]),
		deviceName: func() string {
			value := strings.TrimSpace(cfg["device_name"])
			if value == "" {
				return "LACTSSL"
			}
			return value
		}(),
		http: util.Client(cfg),
	}
	if err := c.login(); err != nil {
		return err
	}
	defer c.logout()

	certificates, err := c.listCertificates()
	if err != nil {
		return err
	}
	description := strings.TrimSpace(cfg["certificate"])
	create := util.Bool(cfg["create"])
	targets := resolveCertificates(certificates, description, create, cert)
	if len(targets) == 0 {
		if description != "" {
			return i18n.E("service.deploy.synology_cert_not_found", "name", description)
		}
		return i18n.E("service.deploy.synology_cert_empty")
	}

	var errs []string
	for _, item := range targets {
		if err := c.importCertificate(cert, item.ID, item.Desc, item.IsDefault); err != nil {
			label := item.Desc
			if label == "" {
				label = item.ID
			}
			errs = append(errs, label+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func resolveCertificates(list []certificate, description string, create bool, cert *deploy.NativeCertificate) []certificate {
	if description != "" {
		for _, item := range list {
			if item.Desc == description || item.Subject.CommonName == description || item.ID == description {
				return []certificate{item}
			}
		}
		if create {
			return []certificate{{Desc: description}}
		}
		return nil
	}
	// 未指定描述时只更新与证书域名匹配的证书，避免把同一张证书导入
	// NAS 上所有证书（包括无关域名和 DSM 默认证书）。
	domains := util.Domains(cert)
	out := make([]certificate, 0, len(list))
	for _, item := range list {
		candidates := append([]string{item.Subject.CommonName}, item.Domains...)
		if util.MatchAny(domains, candidates) {
			out = append(out, item)
		}
	}
	return out
}

func (c *client) apiURL(path string, query url.Values) string {
	endpoint := util.JoinURL(c.base, path)
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return endpoint
}

func (c *client) discoverAuth() {
	c.authPath = "auth.cgi"
	c.authVer = 6
	query := url.Values{}
	query.Set("api", "SYNO.API.Info")
	query.Set("version", "1")
	query.Set("method", "query")
	query.Set("query", "SYNO.API.Auth")
	resp, err := c.http.Get(c.apiURL("/webapi/query.cgi", query))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var info apiInfoResponse
	if json.Unmarshal(raw, &info) != nil {
		return
	}
	entry, ok := info.Data["SYNO.API.Auth"]
	if !ok || entry.Path == "" {
		return
	}
	c.authPath = entry.Path
	if entry.MaxVersion > 0 {
		c.authVer = entry.MaxVersion
	}
}

func (c *client) login() error {
	c.discoverAuth()
	query := url.Values{}
	query.Set("api", "SYNO.API.Auth")
	query.Set("version", strconv.Itoa(c.authVer))
	query.Set("method", "login")
	query.Set("format", "sid")
	query.Set("account", c.username)
	query.Set("passwd", c.password)
	query.Set("enable_syno_token", "yes")
	if c.deviceID != "" {
		query.Set("device_name", c.deviceName)
		query.Set("device_id", c.deviceID)
	} else if c.otpSecret != "" {
		code, err := otp.Code(c.otpSecret, time.Now())
		if err != nil {
			return i18n.E("service.deploy.synology_otp_invalid", "error", err.Error())
		}
		query.Set("enable_device_token", "yes")
		query.Set("device_name", c.deviceName)
		query.Set("otp_code", code)
	}

	resp, err := c.http.PostForm(c.apiURL("/webapi/"+c.authPath, nil), query)
	if err != nil {
		return i18n.E("service.deploy.synology_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result authResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return i18n.E("service.deploy.synology_response_invalid", "error", err.Error())
	}
	if !result.Success {
		code := 0
		if result.Error != nil {
			code = result.Error.Code
		}
		if code == 403 && c.otpSecret == "" && c.deviceID == "" {
			return i18n.E("service.deploy.synology_otp_required")
		}
		return i18n.E("service.deploy.synology_login_failed", "code", code)
	}
	var data struct {
		SID       string `json:"sid"`
		SynoToken string `json:"synotoken"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil || data.SID == "" {
		return i18n.E("service.deploy.synology_login_failed", "code", 0)
	}
	c.sid = data.SID
	c.synoToken = data.SynoToken
	return nil
}

func (c *client) logout() {
	if c.sid == "" {
		return
	}
	query := url.Values{}
	query.Set("api", "SYNO.API.Auth")
	query.Set("version", strconv.Itoa(c.authVer))
	query.Set("method", "logout")
	query.Set("_sid", c.sid)
	resp, err := c.http.PostForm(c.apiURL("/webapi/"+c.authPath, nil), query)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (c *client) listCertificates() ([]certificate, error) {
	form := url.Values{}
	form.Set("api", "SYNO.Core.Certificate.CRT")
	form.Set("method", "list")
	form.Set("version", "1")
	form.Set("_sid", c.sid)
	resp, err := c.http.PostForm(c.apiURL("/webapi/entry.cgi", nil), form)
	if err != nil {
		return nil, i18n.E("service.deploy.synology_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result listResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, i18n.E("service.deploy.synology_response_invalid", "error", err.Error())
	}
	if !result.Success {
		code := 0
		if result.Error != nil {
			code = result.Error.Code
		}
		return nil, i18n.E("service.deploy.synology_list_failed", "code", code)
	}
	return result.Data.Certificates, nil
}

func (c *client) importCertificate(cert *deploy.NativeCertificate, id, description string, asDefault bool) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	addFile := func(field, filename, content string) error {
		if strings.TrimSpace(content) == "" {
			return nil
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename))
		header.Set("Content-Type", "application/octet-stream")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		_, err = part.Write([]byte(content))
		return err
	}
	if err := addFile("key", "key.pem", cert.KeyPEM); err != nil {
		return err
	}
	if err := addFile("cert", "cert.pem", cert.FullchainPEM); err != nil {
		return err
	}
	if err := addFile("inter_cert", "inter_cert.pem", util.ChainPEM(cert.FullchainPEM)); err != nil {
		return err
	}
	_ = writer.WriteField("api", "SYNO.Core.Certificate")
	_ = writer.WriteField("method", "import")
	_ = writer.WriteField("version", "1")
	_ = writer.WriteField("_sid", c.sid)
	if c.synoToken != "" {
		_ = writer.WriteField("SynoToken", c.synoToken)
	}
	_ = writer.WriteField("id", id)
	_ = writer.WriteField("desc", description)
	if asDefault {
		_ = writer.WriteField("as_default", "true")
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.apiURL("/webapi/entry.cgi", nil), &body)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.synoToken != "" {
		req.Header.Set("X-SYNO-TOKEN", c.synoToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.synology_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result authResponse
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.Success {
		code := 0
		if result.Error != nil {
			code = result.Error.Code
		}
		return i18n.E("service.deploy.synology_import_failed", "status", resp.StatusCode, "code", code, "body", util.Truncate(string(raw), 300))
	}
	return nil
}
