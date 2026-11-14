package panels

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/deployplugin"
	"lactssl/internal/deployplugin/pluginutil"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/outbound"
)

// 以下部署目标都由部署插件提供：宝塔、1Panel 与雷池 WAF。

type baotaPlugin struct{}
type onePanelPlugin struct{}
type baotaSitePlugin struct{}
type baotaPanelPlugin struct{}
type onePanelSitePlugin struct{}
type onePanelPanelPlugin struct{}
type safelinePlugin struct{}

func (baotaPlugin) ID() string                  { return "baota" }
func (baotaPlugin) Aliases() []string           { return nil }
func (baotaPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (baotaPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	cert := pluginutil.Certificate(req.Certificate)
	cfg := pluginutil.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return deployBaotaSite(cert, cfg)
	case "panel":
		return deployBaotaPanel(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

func (onePanelPlugin) ID() string                  { return "1panel" }
func (onePanelPlugin) Aliases() []string           { return nil }
func (onePanelPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (onePanelPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	cert := pluginutil.Certificate(req.Certificate)
	cfg := pluginutil.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return deployOnePanelSite(cert, cfg)
	case "panel":
		return deployOnePanelPanel(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

func (baotaSitePlugin) ID() string                  { return "baota_site" }
func (baotaSitePlugin) Aliases() []string           { return nil }
func (baotaSitePlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (baotaSitePlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployBaotaSite(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func (baotaPanelPlugin) ID() string                  { return "baota_panel" }
func (baotaPanelPlugin) Aliases() []string           { return nil }
func (baotaPanelPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (baotaPanelPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployBaotaPanel(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func (onePanelSitePlugin) ID() string                  { return "1panel_site" }
func (onePanelSitePlugin) Aliases() []string           { return nil }
func (onePanelSitePlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (onePanelSitePlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployOnePanelSite(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func (onePanelPanelPlugin) ID() string                  { return "1panel_panel" }
func (onePanelPanelPlugin) Aliases() []string           { return nil }
func (onePanelPanelPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (onePanelPanelPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployOnePanelPanel(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func (safelinePlugin) ID() string                  { return "safeline" }
func (safelinePlugin) Aliases() []string           { return nil }
func (safelinePlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (safelinePlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deploySafeline(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func init() {
	deployplugin.Register(baotaPlugin{})
	deployplugin.Register(onePanelPlugin{})
	deployplugin.Register(baotaSitePlugin{})
	deployplugin.Register(baotaPanelPlugin{})
	deployplugin.Register(onePanelSitePlugin{})
	deployplugin.Register(onePanelPanelPlugin{})
	deployplugin.Register(safelinePlugin{})
}

func deployHTTPClient(skipVerify bool) *http.Client {
	return outbound.NewClient(30*time.Second, skipVerify)
}

func panelURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func skipVerify(cfg map[string]string) bool {
	return cfg["skip_verify"] == "true"
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

// baotaRequestToken 生成宝塔面板 API 的 request_token。
func baotaRequestToken(secret, timestamp string) string {
	return md5Hex(timestamp + md5Hex(secret))
}

func baotaPost(client *http.Client, base, secret, apiPath, action string, data url.Values) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	data.Set("request_time", timestamp)
	data.Set("request_token", baotaRequestToken(secret, timestamp))

	endpoint := base + apiPath
	if action != "" {
		endpoint += "?action=" + url.QueryEscape(action)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.baota_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var result struct {
		Status bool   `json:"status"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(body, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.baota_status", "status", resp.StatusCode, "body", pluginutil.Truncate(string(body), 300))
	}
	if !result.Status {
		if result.Msg != "" {
			return i18n.E("service.deploy.baota_error", "message", result.Msg)
		}
		return i18n.E("service.deploy.baota_status", "status", resp.StatusCode, "body", pluginutil.Truncate(string(body), 300))
	}
	return nil
}

func deployBaotaSite(cert *model.Certificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateURL(base); err != nil {
		return err
	}
	if secret == "" {
		return i18n.E("service.deploy.baota_secret_required")
	}
	sites := pluginutil.SplitList(cfg["sites"])
	if len(sites) == 0 {
		sites = cert.Domains
	}
	if len(sites) == 0 {
		return i18n.E("service.deploy.baota_sites_empty")
	}

	client := deployHTTPClient(skipVerify(cfg))
	var errs []string
	for _, site := range sites {
		data := url.Values{
			"type":     {"0"},
			"siteName": {site},
			"key":      {cert.KeyPEM},
			"csr":      {cert.FullchainPEM},
		}
		if err := baotaPost(client, base, secret, "/site", "SetSSL", data); err != nil {
			errs = append(errs, site+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func deployBaotaPanel(cert *model.Certificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateURL(base); err != nil {
		return err
	}
	if secret == "" {
		return i18n.E("service.deploy.baota_secret_required")
	}
	data := url.Values{
		"privateKey": {cert.KeyPEM},
		"certPem":    {cert.FullchainPEM},
	}
	if err := baotaPost(deployHTTPClient(skipVerify(cfg)), base, secret, "/config", "SavePanelSSL", data); err != nil {
		return err
	}
	return nil
}

func onePanelVersion(value string) string {
	if strings.TrimSpace(value) == "v1" {
		return "v1"
	}
	return "v2"
}

func onePanelRequest(client *http.Client, base, version, apiKey, method, apiPath string, payload any) (json.RawMessage, error) {
	endpoint := base + "/api/" + version + apiPath
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("1Panel-Token", md5Hex("1panel"+apiKey+timestamp))
	req.Header.Set("1Panel-Timestamp", timestamp)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.onepanel_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var result struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(bodyData, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 200 {
		if result.Message != "" {
			return nil, i18n.E("service.deploy.onepanel_error", "code", result.Code, "message", result.Message)
		}
		return nil, i18n.E("service.deploy.onepanel_status", "status", resp.StatusCode, "body", pluginutil.Truncate(string(bodyData), 300))
	}
	return result.Data, nil
}

func onePanelResolveWebsite(client *http.Client, base, version, apiKey, target string) (int64, error) {
	if id, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64); err == nil && id > 0 {
		return id, nil
	}
	payload := map[string]any{
		"page":     1,
		"pageSize": 100,
		"name":     target,
		"order":    "null",
		"orderBy":  "created_at",
	}
	data, err := onePanelRequest(client, base, version, apiKey, http.MethodPost, "/websites/search", payload)
	if err != nil {
		return 0, err
	}
	var result struct {
		Items []struct {
			ID            int64  `json:"id"`
			PrimaryDomain string `json:"primaryDomain"`
			Alias         string `json:"alias"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, i18n.E("service.deploy.onepanel_status", "status", http.StatusOK, "body", pluginutil.Truncate(string(data), 300))
	}
	for _, item := range result.Items {
		if strings.EqualFold(item.PrimaryDomain, target) || strings.EqualFold(item.Alias, target) {
			return item.ID, nil
		}
	}
	if len(result.Items) == 1 {
		return result.Items[0].ID, nil
	}
	return 0, i18n.E("service.deploy.onepanel_website_not_found", "website", target)
}

func deployOnePanelSite(cert *model.Certificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateURL(base); err != nil {
		return err
	}
	if apiKey == "" {
		return i18n.E("service.deploy.onepanel_key_required")
	}
	target := strings.TrimSpace(cfg["website"])
	if target == "" {
		target = cert.PrimaryDomain
	}
	if target == "" {
		return i18n.E("service.deploy.onepanel_website_required")
	}
	version := onePanelVersion(cfg["api_version"])
	client := deployHTTPClient(skipVerify(cfg))
	websiteID, err := onePanelResolveWebsite(client, base, version, apiKey, target)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"websiteId":   websiteID,
		"type":        "manual",
		"importType":  "paste",
		"certificate": cert.FullchainPEM,
		"privateKey":  cert.KeyPEM,
		"enable":      true,
		"httpConfig":  "HTTPToHTTPS",
	}
	_, err = onePanelRequest(client, base, version, apiKey, http.MethodPost, "/websites/"+strconv.FormatInt(websiteID, 10)+"/https", payload)
	return err
}

func deployOnePanelPanel(cert *model.Certificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateURL(base); err != nil {
		return err
	}
	if apiKey == "" {
		return i18n.E("service.deploy.onepanel_key_required")
	}
	version := onePanelVersion(cfg["api_version"])
	sslMode := strings.TrimSpace(cfg["ssl_mode"])
	if sslMode == "" {
		sslMode = "Enable"
	}
	apiPath := "/core/settings/ssl/update"
	if version == "v1" {
		sslMode = "enable"
		apiPath = "/settings/ssl/update"
	}
	payload := map[string]any{
		"cert":    cert.FullchainPEM,
		"key":     cert.KeyPEM,
		"domain":  cert.PrimaryDomain,
		"ssl":     sslMode,
		"sslID":   0,
		"sslType": "import-paste",
	}
	_, err := onePanelRequest(deployHTTPClient(skipVerify(cfg)), base, version, apiKey, http.MethodPost, apiPath, payload)
	return err
}

type safelineResponse struct {
	Err  json.RawMessage `json:"err"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (r safelineResponse) ok() bool {
	errValue := strings.TrimSpace(string(r.Err))
	return errValue == "" || errValue == "null" || errValue == "false" || errValue == "0" || errValue == `""`
}

func safelineRequest(client *http.Client, base, token, method, apiPath string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+apiPath, body)
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("X-SLCE-API-TOKEN", token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.safeline_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var result safelineResponse
	_ = json.Unmarshal(bodyData, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.ok() {
		if result.Msg != "" {
			return nil, i18n.E("service.deploy.safeline_error", "message", result.Msg)
		}
		return nil, i18n.E("service.deploy.safeline_status", "status", resp.StatusCode, "body", pluginutil.Truncate(string(bodyData), 300))
	}
	return result.Data, nil
}

type safelineCert struct {
	ID      int64    `json:"id"`
	Domains []string `json:"domains"`
	Domain  string   `json:"domain"`
}

func safelineDomains(cert safelineCert) []string {
	if len(cert.Domains) > 0 {
		return cert.Domains
	}
	if cert.Domain != "" {
		return []string{cert.Domain}
	}
	return nil
}

func safelineDomainMatch(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if strings.HasPrefix(left, "*.") && strings.HasSuffix(right, left[1:]) {
		return true
	}
	if strings.HasPrefix(right, "*.") && strings.HasSuffix(left, right[1:]) {
		return true
	}
	return false
}

func safelineMatches(cert safelineCert, domains []string) bool {
	for _, left := range safelineDomains(cert) {
		for _, right := range domains {
			if safelineDomainMatch(left, right) {
				return true
			}
		}
	}
	return false
}

func deploySafeline(cert *model.Certificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	token := strings.TrimSpace(cfg["api_token"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateURL(base); err != nil {
		return err
	}
	if token == "" {
		return i18n.E("service.deploy.safeline_token_required")
	}
	client := deployHTTPClient(skipVerify(cfg))

	domains := cert.Domains
	if len(domains) == 0 && cert.PrimaryDomain != "" {
		domains = []string{cert.PrimaryDomain}
	}

	var targets []int64
	rawIDs := pluginutil.SplitList(cfg["cert_ids"])
	if len(rawIDs) > 0 {
		for _, raw := range rawIDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return i18n.E("service.deploy.safeline_cert_ids_invalid", "value", raw)
			}
			targets = append(targets, id)
		}
	} else {
		data, err := safelineRequest(client, base, token, http.MethodGet, "/api/open/cert", nil)
		if err != nil {
			return err
		}
		var result struct {
			Nodes []safelineCert `json:"nodes"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return i18n.E("service.deploy.safeline_status", "status", http.StatusOK, "body", pluginutil.Truncate(string(data), 300))
		}
		for _, node := range result.Nodes {
			if safelineMatches(node, domains) {
				targets = append(targets, node.ID)
			}
		}
	}

	if len(targets) == 0 {
		_, err := safelineRequest(client, base, token, http.MethodPost, "/api/open/cert", map[string]any{
			"manual": map[string]string{"crt": cert.FullchainPEM, "key": cert.KeyPEM},
			"type":   2,
		})
		return err
	}

	var errs []string
	for _, id := range targets {
		_, err := safelineRequest(client, base, token, http.MethodPost, "/api/open/cert", map[string]any{
			"id":     id,
			"manual": map[string]string{"crt": cert.FullchainPEM, "key": cert.KeyPEM},
			"type":   2,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%d: %s", id, err.Error()))
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}
