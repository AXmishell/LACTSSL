// Package acepanel 通过 AcePanel 管理 API 部署网站证书或面板证书。
//
// 对应 certd 的「AcePanel-部署到网站」与「AcePanel-面板证书」插件。
package acepanel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type sitePlugin struct{}
type panelPlugin struct{}

func (sitePlugin) ID() string        { return "acepanel" }
func (sitePlugin) Aliases() []string { return nil }
func (sitePlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return deploySite(cert, cfg)
	case "panel":
		return deployPanel(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

// Plan 读取 AcePanel 网站列表，供宿主按证书域名匹配。
func (sitePlugin) Plan(_ context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return acepanelPlan(cfg)
	default:
		return deploy.NativePlanResponse{}, nil
	}
}

func (panelPlugin) ID() string        { return "acepanel_panel" }
func (panelPlugin) Aliases() []string { return nil }
func (panelPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployPanel(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() {
	deploy.MarkNativeVerifiable("acepanel", "acepanel_panel")
	deploy.MarkNativePlannable("acepanel")
	deploy.RegisterNative(sitePlugin{})
	deploy.RegisterNative(panelPlugin{})
}

type client struct {
	base  string
	token string
	id    int64
	http  *http.Client
}

type apiResult struct {
	Code    *int            `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type website struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
}

func newClient(cfg map[string]string) (*client, error) {
	base, err := util.BaseURL(cfg["panel_url"])
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(cfg["token_id"]), 10, 64)
	if err != nil || id <= 0 {
		return nil, i18n.E("service.deploy.acepanel_token_id_invalid")
	}
	token := strings.TrimSpace(cfg["access_token"])
	if token == "" {
		return nil, i18n.E("service.deploy.acepanel_token_required")
	}
	return &client{base: base, token: token, id: id, http: util.Client(cfg)}, nil
}

// acepanelPlan 读取网站列表，返回主机可直接写入 websites 字段的对象。
func acepanelPlan(cfg map[string]string) (deploy.NativePlanResponse, error) {
	c, err := newClient(cfg)
	if err != nil {
		return deploy.NativePlanResponse{}, err
	}
	websites, err := c.getWebsites()
	if err != nil {
		return deploy.NativePlanResponse{}, err
	}
	objects := make([]deploy.NativePlanObject, 0, len(websites))
	for _, item := range websites {
		value := strings.TrimSpace(item.Name)
		if value == "" && item.ID > 0 {
			value = strconv.FormatInt(item.ID, 10)
		}
		if value == "" {
			continue
		}
		domains := append([]string(nil), item.Domains...)
		if name := strings.TrimSpace(item.Name); name != "" {
			domains = append(domains, name)
		}
		var identifiers []string
		if item.ID > 0 {
			identifiers = []string{strconv.FormatInt(item.ID, 10)}
		}
		objects = append(objects, deploy.NativePlanObject{
			Value:       value,
			Name:        item.Name,
			Domains:     domains,
			Identifiers: identifiers,
		})
	}
	return deploy.NativePlanResponse{Field: "websites", Multiple: true, Objects: objects}, nil
}

func deploySite(cert *deploy.NativeCertificate, cfg map[string]string) error {
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	certID, err := c.uploadCertificate(cert)
	if err != nil {
		return err
	}
	websites, err := c.getWebsites()
	if err != nil {
		return err
	}
	selected := util.SplitList(cfg["websites"])
	targets, err := resolveWebsites(websites, selected, util.Domains(cert))
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return i18n.E("service.deploy.acepanel_websites_empty")
	}
	enableHTTPS := true
	if strings.TrimSpace(cfg["enable_https"]) != "" {
		enableHTTPS = util.Bool(cfg["enable_https"])
	}
	version := c.panelVersion()
	if versionAtLeast(version, "3.3.2") {
		ids := make([]int64, 0, len(targets))
		for _, item := range targets {
			ids = append(ids, item.ID)
		}
		_, err = c.request(http.MethodPost, fmt.Sprintf("/api/cert/cert/%d/deploy", certID), map[string]any{
			"id":           certID,
			"website_ids":  ids,
			"enable_https": enableHTTPS,
		})
		return err
	}

	var errs []string
	for _, item := range targets {
		_, err := c.request(http.MethodPost, fmt.Sprintf("/api/cert/cert/%d/deploy", certID), map[string]any{
			"id":         certID,
			"website_id": item.ID,
		})
		if err != nil {
			errs = append(errs, item.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func deployPanel(cert *deploy.NativeCertificate, cfg map[string]string) error {
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	data, err := c.request(http.MethodGet, "/api/setting", nil)
	if err != nil {
		return err
	}
	settings := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return i18n.E("service.deploy.acepanel_response_invalid", "error", err.Error())
		}
	}
	settings["acme"] = false
	settings["https"] = true
	settings["cert"] = cert.FullchainPEM
	settings["key"] = cert.KeyPEM
	_, err = c.request(http.MethodPost, "/api/setting", settings)
	return err
}

func (c *client) uploadCertificate(cert *deploy.NativeCertificate) (int64, error) {
	data, err := c.request(http.MethodPost, "/api/cert/cert/upload", map[string]any{
		"cert": cert.FullchainPEM,
		"key":  cert.KeyPEM,
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.ID <= 0 {
		return 0, i18n.E("service.deploy.acepanel_cert_id_missing")
	}
	return result.ID, nil
}

func (c *client) getWebsites() ([]website, error) {
	var all []website
	seen := make(map[int64]bool)
	for page := 1; page <= 20; page++ {
		data, err := c.request(http.MethodGet, fmt.Sprintf("/api/website?limit=100&page=%d&type=all", page), nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Items []website `json:"items"`
			Total int       `json:"total"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, i18n.E("service.deploy.acepanel_response_invalid", "error", err.Error())
		}
		added := 0
		for _, item := range result.Items {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			all = append(all, item)
			added++
		}
		if len(result.Items) == 0 || added == 0 || (result.Total > 0 && len(all) >= result.Total) || len(result.Items) < 100 {
			break
		}
	}
	return all, nil
}

func (c *client) panelVersion() string {
	data, err := c.request(http.MethodGet, "/api/home/system_info", nil)
	if err != nil {
		return ""
	}
	var result struct {
		PanelVersion string `json:"panel_version"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.PanelVersion)
}

func resolveWebsites(websites []website, selected, certDomains []string) ([]website, error) {
	if len(selected) == 0 {
		out := make([]website, 0, len(websites))
		for _, item := range websites {
			if util.MatchAny(certDomains, item.Domains) || util.MatchAny(certDomains, []string{item.Name}) {
				out = append(out, item)
			}
		}
		return out, nil
	}
	seen := map[int64]bool{}
	out := make([]website, 0, len(selected))
	for _, value := range selected {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			found := false
			for _, item := range websites {
				if item.ID == id {
					if !seen[id] {
						seen[id] = true
						out = append(out, item)
					}
					found = true
					break
				}
			}
			if !found {
				return nil, i18n.E("service.deploy.acepanel_website_not_found", "website", value)
			}
			continue
		}
		matched := false
		for _, item := range websites {
			if util.MatchAny([]string{value}, item.Domains) || strings.EqualFold(item.Name, value) {
				if !seen[item.ID] {
					seen[item.ID] = true
					out = append(out, item)
				}
				matched = true
			}
		}
		if !matched {
			return nil, i18n.E("service.deploy.acepanel_website_not_found", "website", value)
		}
	}
	return out, nil
}

func (c *client) request(method, path string, payload any) (json.RawMessage, error) {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
	}
	fullURL := util.JoinURL(c.base, path)
	signature, timestamp := sign(method, fullURL, string(body), c.id, c.token)
	req, err := http.NewRequest(method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("Authorization", fmt.Sprintf("HMAC-SHA256 Credential=%d, Signature=%s", c.id, signature))
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.acepanel_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result apiResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, i18n.E("service.deploy.acepanel_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !aceOK(result.Code) {
		if result.Message != "" || result.Msg != "" {
			message := result.Message
			if message == "" {
				message = result.Msg
			}
			return nil, i18n.E("service.deploy.acepanel_error", "status", resp.StatusCode, "message", message)
		}
		return nil, i18n.E("service.deploy.acepanel_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return result.Data, nil
}

func aceOK(code *int) bool {
	return code != nil && (*code == 0 || *code == 200)
}

// sign 按 AcePanel 的 HMAC-SHA256 规则生成签名。
func sign(method, apiURL, body string, tokenID int64, token string) (string, int64) {
	parsed, _ := url.Parse(apiURL)
	path := parsed.Path
	if index := strings.Index(path, "/api"); index >= 0 {
		path = path[index:]
	}
	canonical := strings.Join([]string{
		method,
		path,
		parsed.RawQuery,
		sha256Hex([]byte(body)),
	}, "\n")
	timestamp := time.Now().Unix()
	stringToSign := strings.Join([]string{
		"HMAC-SHA256",
		strconv.FormatInt(timestamp, 10),
		sha256Hex([]byte(canonical)),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil)), timestamp
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func versionAtLeast(left, right string) bool {
	if strings.TrimSpace(left) == "" {
		return false
	}
	parse := func(value string) []int {
		parts := strings.Split(strings.TrimSpace(value), ".")
		out := make([]int, 0, len(parts))
		for _, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil {
				return nil
			}
			out = append(out, number)
		}
		return out
	}
	a, b := parse(left), parse(right)
	if a == nil || b == nil {
		return false
	}
	for len(a) < len(b) {
		a = append(a, 0)
	}
	for len(b) < len(a) {
		b = append(b, 0)
	}
	for index := range a {
		if a[index] > b[index] {
			return true
		}
		if a[index] < b[index] {
			return false
		}
	}
	return true
}

// Verify 只读检查 AcePanel 认证与网站/面板作用域。
func (sitePlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyAcepanel(ctx, req)
}

func (panelPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyAcepanel(ctx, req)
}

func verifyAcepanel(_ context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	c, err := newClient(cfg)
	if err != nil {
		return verifyFailed(loc, err), nil
	}
	target := strings.TrimSpace(cfg["target"])
	checks := make([]protocol.VerifyCheck, 0, 3)
	if target == "panel" {
		if _, err := c.request(http.MethodGet, "/api/setting", nil); err != nil {
			return verifyFailed(loc, err), nil
		}
		checks = append(checks,
			protocol.VerifyCheck{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
			protocol.VerifyCheck{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
			protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)},
		)
		return checks, nil
	}
	websites, err := c.getWebsites()
	if err != nil {
		return verifyFailed(loc, err), nil
	}
	checks = append(checks,
		protocol.VerifyCheck{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		protocol.VerifyCheck{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
	)
	selected := util.SplitList(cfg["websites"])
	if len(selected) == 0 {
		checks = append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)})
		return checks, nil
	}
	existing := make(map[string]bool, len(websites))
	for _, item := range websites {
		existing[item.Name] = true
	}
	var missing []string
	for _, name := range selected {
		if !existing[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		checks = append(checks, protocol.VerifyCheck{
			Key: "scope", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.target_missing", map[string]any{"target": strings.Join(missing, ", ")}),
		})
		return checks, nil
	}
	checks = append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.target_ok", nil)})
	return checks, nil
}

func verifyFailed(loc i18n.Locale, err error) []protocol.VerifyCheck {
	if err == nil {
		return nil
	}
	return []protocol.VerifyCheck{{
		Key: "verify", Status: protocol.VerifyStatusFailed,
		Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}),
	}}
}
