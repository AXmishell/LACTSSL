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

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

// 以下部署目标都由部署插件提供：宝塔、1Panel 与雷池 WAF。

type baotaPlugin struct{}
type baotaDockerPlugin struct{}
type onePanelPlugin struct{}
type baotaSitePlugin struct{}
type baotaPanelPlugin struct{}
type onePanelSitePlugin struct{}
type onePanelPanelPlugin struct{}
type safelinePlugin struct{}
type safelinePanelPlugin struct{}
type safelinePortalPlugin struct{}

func (baotaPlugin) ID() string        { return "baota" }
func (baotaPlugin) Aliases() []string { return nil }
func (baotaPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return deployBaotaSite(cert, cfg)
	case "panel":
		return deployBaotaPanel(cert, cfg)
	case "docker":
		return deployBaotaDockerSite(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}
func (baotaPlugin) ListOptions(ctx context.Context, req deploy.NativeOptionsRequest) ([]protocol.DeployOption, error) {
	cfg := util.Merge(req.Auth, req.Config)
	target := strings.TrimSpace(cfg["target"])
	switch target {
	case "", "site":
		return baotaListOptions(ctx, cfg, "site", req.Query)
	case "docker":
		return baotaListOptions(ctx, cfg, "docker", req.Query)
	default:
		return nil, i18n.E("service.deploy.baota_options_unsupported")
	}
}

// Plan 返回宝塔网站列表，供宿主按证书域名匹配后再部署。
func (baotaPlugin) Plan(ctx context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return baotaPlan(ctx, cfg, "site")
	case "docker":
		return baotaPlan(ctx, cfg, "docker")
	default:
		return deploy.NativePlanResponse{}, nil
	}
}

func (baotaDockerPlugin) ID() string        { return "baota_docker" }
func (baotaDockerPlugin) Aliases() []string { return nil }
func (baotaDockerPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployBaotaDockerSite(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}
func (baotaDockerPlugin) ListOptions(ctx context.Context, req deploy.NativeOptionsRequest) ([]protocol.DeployOption, error) {
	return baotaListOptions(ctx, util.Merge(req.Auth, req.Config), "docker", req.Query)
}

func (baotaDockerPlugin) Plan(ctx context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	return baotaPlan(ctx, util.Merge(req.Auth, req.Config), "docker")
}

func (onePanelPlugin) ID() string        { return "1panel" }
func (onePanelPlugin) Aliases() []string { return nil }
func (onePanelPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return deployOnePanelSite(cert, cfg)
	case "panel":
		return deployOnePanelPanel(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

func (onePanelPlugin) Plan(_ context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "site":
		return onePanelPlan(cfg)
	default:
		return deploy.NativePlanResponse{}, nil
	}
}

func (baotaSitePlugin) ID() string        { return "baota_site" }
func (baotaSitePlugin) Aliases() []string { return nil }
func (baotaSitePlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployBaotaSite(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}
func (baotaSitePlugin) ListOptions(ctx context.Context, req deploy.NativeOptionsRequest) ([]protocol.DeployOption, error) {
	return baotaListOptions(ctx, util.Merge(req.Auth, req.Config), "site", req.Query)
}

func (baotaSitePlugin) Plan(ctx context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	return baotaPlan(ctx, util.Merge(req.Auth, req.Config), "site")
}

func (baotaPanelPlugin) ID() string        { return "baota_panel" }
func (baotaPanelPlugin) Aliases() []string { return nil }
func (baotaPanelPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployBaotaPanel(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (onePanelSitePlugin) ID() string        { return "1panel_site" }
func (onePanelSitePlugin) Aliases() []string { return nil }
func (onePanelSitePlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployOnePanelSite(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (onePanelSitePlugin) Plan(_ context.Context, req deploy.NativePlanRequest) (deploy.NativePlanResponse, error) {
	return onePanelPlan(util.Merge(req.Auth, req.Config))
}

func (onePanelPanelPlugin) ID() string        { return "1panel_panel" }
func (onePanelPanelPlugin) Aliases() []string { return nil }
func (onePanelPanelPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployOnePanelPanel(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (safelinePlugin) ID() string        { return "safeline" }
func (safelinePlugin) Aliases() []string { return nil }
func (safelinePlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "waf":
		return deploySafeline(cert, cfg)
	case "panel":
		return deploySafelinePanel(cert, cfg)
	case "portal":
		return deploySafelinePortal(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

func (safelinePanelPlugin) ID() string        { return "safeline_panel" }
func (safelinePanelPlugin) Aliases() []string { return nil }
func (safelinePanelPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deploySafelinePanel(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (safelinePortalPlugin) ID() string        { return "safeline_portal" }
func (safelinePortalPlugin) Aliases() []string { return nil }
func (safelinePortalPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deploySafelinePortal(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

// RegisterBaota 注册宝塔规范目标及历史兼容 ID。
func RegisterBaota() {
	deploy.MarkNativeVerifiable("baota", "baota_site", "baota_panel", "baota_docker")
	deploy.MarkNativePlannable("baota", "baota_site", "baota_docker")
	deploy.RegisterNative(baotaPlugin{})
	deploy.RegisterNative(baotaDockerPlugin{})
	deploy.RegisterNative(baotaSitePlugin{})
	deploy.RegisterNative(baotaPanelPlugin{})
}

// RegisterOnePanel 注册 1Panel 规范目标及历史兼容 ID。
func RegisterOnePanel() {
	deploy.MarkNativeVerifiable("1panel", "1panel_site", "1panel_panel")
	deploy.MarkNativePlannable("1panel", "1panel_site")
	deploy.RegisterNative(onePanelPlugin{})
	deploy.RegisterNative(onePanelSitePlugin{})
	deploy.RegisterNative(onePanelPanelPlugin{})
}

// RegisterSafeline 注册雷池 WAF 目标。
func RegisterSafeline() {
	deploy.MarkNativeVerifiable("safeline", "safeline_panel", "safeline_portal")
	deploy.RegisterNative(safelinePlugin{})
	deploy.RegisterNative(safelinePanelPlugin{})
	deploy.RegisterNative(safelinePortalPlugin{})
}

func deployHTTPClient(skipVerify bool) *http.Client {
	return outbound.NewClient(30*time.Second, skipVerify)
}

// readPanelBody 限制控制面响应大小，同时避免把截断后的 JSON 当成功解析。
func readPanelBody(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, i18n.E("service.deploy.request_failed", "error", err.Error())
	}
	if int64(len(body)) > limit {
		return nil, i18n.E("service.deploy.panel_response_too_large", "limit", limit)
	}
	return body, nil
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

// baotaPostRaw 发送带签名的面板请求并返回原始响应。
// 部分列表接口（如 data?action=getData）不返回 status 字段，必须由调用方解析。
func baotaPostRaw(client *http.Client, base, secret, apiPath, action string, data url.Values) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	data.Set("request_time", timestamp)
	data.Set("request_token", baotaRequestToken(secret, timestamp))

	endpoint := base + apiPath
	if action != "" {
		endpoint += "?action=" + url.QueryEscape(action)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.baota_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, i18n.E("service.deploy.baota_status", "status", resp.StatusCode, "body", util.Truncate(string(body), 300))
	}
	return body, nil
}

func baotaPost(client *http.Client, base, secret, apiPath, action string, data url.Values) error {
	body, err := baotaPostRaw(client, base, secret, apiPath, action, data)
	if err != nil {
		return err
	}
	var result struct {
		Status bool   `json:"status"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(body, &result)
	if !result.Status {
		if result.Msg != "" {
			return i18n.E("service.deploy.baota_error", "message", result.Msg)
		}
		return i18n.E("service.deploy.baota_status", "status", 0, "body", util.Truncate(string(body), 300))
	}
	return nil
}

func deployBaotaSite(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return err
	}
	if secret == "" {
		return i18n.E("service.deploy.baota_secret_required")
	}
	sites := util.SplitList(cfg["sites"])
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

func deployBaotaDockerSite(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return err
	}
	if secret == "" {
		return i18n.E("service.deploy.baota_secret_required")
	}
	sites := util.SplitList(cfg["sites"])
	if len(sites) == 0 {
		return i18n.E("service.deploy.baota_sites_empty")
	}
	client := deployHTTPClient(skipVerify(cfg))
	var errs []string
	for _, site := range sites {
		data := url.Values{
			"key":      {cert.KeyPEM},
			"csr":      {cert.FullchainPEM},
			"siteName": {site},
		}
		if err := baotaPost(client, base, secret, "/mod/docker/com/set_ssl", "", data); err != nil {
			errs = append(errs, site+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

// baotaPlan 读取宝塔网站或 Docker 网站列表，供宿主按证书域名匹配。
func baotaPlan(ctx context.Context, cfg map[string]string, target string) (deploy.NativePlanResponse, error) {
	options, err := baotaListOptions(ctx, cfg, target, "")
	if err != nil {
		return deploy.NativePlanResponse{}, err
	}
	objects := make([]deploy.NativePlanObject, 0, len(options))
	for _, option := range options {
		objects = append(objects, deploy.NativePlanObject{
			Value:   option.Value,
			Name:    option.Label,
			Domains: []string{option.Value},
		})
	}
	return deploy.NativePlanResponse{Field: "sites", Multiple: true, Objects: objects}, nil
}

// baotaListOptions 从宝塔面板读取可部署对象名称。
// 普通网站使用数据查询接口，Docker 网站使用 Docker 模块的 get_site_list。
func baotaListOptions(_ context.Context, cfg map[string]string, target, query string) ([]protocol.DeployOption, error) {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return nil, i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return nil, err
	}
	if secret == "" {
		return nil, i18n.E("service.deploy.baota_secret_required")
	}

	client := deployHTTPClient(skipVerify(cfg))
	var (
		body []byte
		err  error
	)
	switch target {
	case "docker":
		data := url.Values{
			"p":    {"1"},
			"row":  {"1000"},
			"type": {"all"},
		}
		if keyword := strings.TrimSpace(query); keyword != "" {
			data.Set("query", keyword)
		}
		body, err = baotaPostRaw(client, base, secret, "/mod/docker/com/get_site_list", "", data)
	default:
		data := url.Values{
			"p":     {"1"},
			"limit": {"1000"},
			"type":  {"-1"},
			"order": {"id desc"},
		}
		if keyword := strings.TrimSpace(query); keyword != "" {
			data.Set("search", keyword)
		}
		body, err = baotaPostRaw(client, base, secret, "/data?action=getData&table=sites", "", data)
	}
	if err != nil {
		return nil, err
	}
	return parseBaotaSiteOptions(body)
}

// parseBaotaSiteOptions 兼容不同面板版本的响应结构：
// 网站列表通常返回 data 数组，Docker 网站列表可能再包一层 data。
func parseBaotaSiteOptions(body []byte) ([]protocol.DeployOption, error) {
	var envelope struct {
		Status *bool           `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, i18n.E("service.deploy.baota_sites_invalid")
	}
	if envelope.Status != nil && !*envelope.Status {
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = util.Truncate(string(body), 300)
		}
		return nil, i18n.E("service.deploy.baota_error", "message", message)
	}
	data := bytes.TrimSpace(envelope.Data)
	if len(data) == 0 {
		return nil, i18n.E("service.deploy.baota_sites_invalid")
	}
	if bytes.Equal(data, []byte("null")) {
		return []protocol.DeployOption{}, nil
	}
	items, err := baotaSiteItems(data, 0)
	if err != nil {
		return nil, err
	}
	options := make([]protocol.DeployOption, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		name := baotaSiteName(item)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		options = append(options, protocol.DeployOption{Label: name, Value: name})
	}
	if len(items) > 0 && len(options) == 0 {
		return nil, i18n.E("service.deploy.baota_sites_invalid")
	}
	return options, nil
}

func baotaSiteItems(raw json.RawMessage, depth int) ([]map[string]any, error) {
	if depth > 2 {
		return nil, i18n.E("service.deploy.baota_sites_invalid")
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		items = make([]map[string]any, 0, len(names))
		for _, name := range names {
			items = append(items, map[string]any{"name": name})
		}
		return items, nil
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(bytes.TrimSpace(wrapper.Data)) > 0 {
		return baotaSiteItems(wrapper.Data, depth+1)
	}
	return nil, i18n.E("service.deploy.baota_sites_invalid")
}

func baotaSiteName(item map[string]any) string {
	for _, key := range []string{"name", "site_name", "siteName", "site"} {
		value, ok := item[key].(string)
		if !ok {
			continue
		}
		if name := strings.TrimSpace(value); name != "" {
			return name
		}
	}
	return ""
}

func deployBaotaPanel(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	secret := strings.TrimSpace(cfg["api_secret"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
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
	bodyData, err := readPanelBody(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
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
		return nil, i18n.E("service.deploy.onepanel_status", "status", resp.StatusCode, "body", util.Truncate(string(bodyData), 300))
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
		return 0, i18n.E("service.deploy.onepanel_status", "status", http.StatusOK, "body", util.Truncate(string(data), 300))
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

// onePanelPlan 读取 1Panel 网站列表。返回的 Value 是主域名，
// 宿主可直接写入部署节点的 website 字段。
func onePanelPlan(cfg map[string]string) (deploy.NativePlanResponse, error) {
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return deploy.NativePlanResponse{}, i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return deploy.NativePlanResponse{}, err
	}
	if apiKey == "" {
		return deploy.NativePlanResponse{}, i18n.E("service.deploy.onepanel_key_required")
	}
	version := onePanelVersion(cfg["api_version"])
	payload := map[string]any{
		"page":     1,
		"pageSize": 1000,
		"order":    "null",
		"orderBy":  "created_at",
	}
	data, err := onePanelRequest(deployHTTPClient(skipVerify(cfg)), base, version, apiKey, http.MethodPost, "/websites/search", payload)
	if err != nil {
		return deploy.NativePlanResponse{}, err
	}
	var result struct {
		Items []struct {
			ID            int64  `json:"id"`
			PrimaryDomain string `json:"primaryDomain"`
			Alias         string `json:"alias"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return deploy.NativePlanResponse{}, i18n.E("service.deploy.onepanel_status", "status", http.StatusOK, "body", util.Truncate(string(data), 300))
	}
	objects := make([]deploy.NativePlanObject, 0, len(result.Items))
	for _, item := range result.Items {
		domains := make([]string, 0, 8)
		if value := strings.TrimSpace(item.PrimaryDomain); value != "" {
			domains = append(domains, value)
		}
		domains = append(domains, util.SplitList(item.Alias)...)
		value := strings.TrimSpace(item.PrimaryDomain)
		if value == "" && len(domains) > 0 {
			value = domains[0]
		}
		if value == "" && item.ID > 0 {
			value = strconv.FormatInt(item.ID, 10)
		}
		if value == "" {
			continue
		}
		var identifiers []string
		if item.ID > 0 {
			identifiers = []string{strconv.FormatInt(item.ID, 10)}
		}
		objects = append(objects, deploy.NativePlanObject{
			Value:       value,
			Name:        strings.TrimSpace(item.PrimaryDomain),
			Domains:     domains,
			Identifiers: identifiers,
		})
	}
	return deploy.NativePlanResponse{Field: "website", Multiple: false, Objects: objects}, nil
}

func deployOnePanelSite(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return err
	}
	if apiKey == "" {
		return i18n.E("service.deploy.onepanel_key_required")
	}
	target := strings.TrimSpace(cfg["website"])
	if target == "" {
		target = cert.Domain
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

func deployOnePanelPanel(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
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
		"domain":  cert.Domain,
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
	if len(bytes.TrimSpace(r.Err)) == 0 {
		return false
	}
	errValue := strings.TrimSpace(string(r.Err))
	return errValue == "null" || errValue == "false" || errValue == "0" || errValue == `""`
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
	bodyData, err := readPanelBody(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
	var result safelineResponse
	if err := json.Unmarshal(bodyData, &result); err != nil {
		return nil, i18n.E("service.deploy.safeline_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.ok() {
		if result.Msg != "" {
			return nil, i18n.E("service.deploy.safeline_error", "message", result.Msg)
		}
		return nil, i18n.E("service.deploy.safeline_status", "status", resp.StatusCode, "body", util.Truncate(string(bodyData), 300))
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
	return util.DomainMatch(left, right)
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

func deploySafeline(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base := panelURL(cfg["panel_url"])
	token := strings.TrimSpace(cfg["api_token"])
	if base == "" {
		return i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return err
	}
	if token == "" {
		return i18n.E("service.deploy.safeline_token_required")
	}
	client := deployHTTPClient(skipVerify(cfg))

	domains := cert.Domains
	if len(domains) == 0 && cert.Domain != "" {
		domains = []string{cert.Domain}
	}

	var targets []int64
	rawIDs := util.SplitList(cfg["cert_ids"])
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
			return i18n.E("service.deploy.safeline_status", "status", http.StatusOK, "body", util.Truncate(string(data), 300))
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

// deploySafelinePanel 上传证书并替换雷池控制台自身的证书。
func deploySafelinePanel(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, token, client, err := safelineClient(cfg)
	if err != nil {
		return err
	}
	certID, err := uploadSafelineCert(client, base, token, 0, cert)
	if err != nil {
		return err
	}
	_, err = safelineRequest(client, base, token, http.MethodPut, "/api/open/system", map[string]any{
		"cert_id": certID,
	})
	return err
}

// deploySafelinePortal 更新雷池认证中心已绑定的证书。
// 认证中心尚未启用证书时只上传新证书，与 ALLinSSL 的行为一致。
func deploySafelinePortal(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, token, client, err := safelineClient(cfg)
	if err != nil {
		return err
	}
	data, err := safelineRequest(client, base, token, http.MethodGet, "/api/open/portal", nil)
	if err != nil {
		return err
	}
	var portal map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &portal); err != nil {
			return i18n.E("service.deploy.safeline_status", "status", http.StatusOK, "body", util.Truncate(string(data), 300))
		}
	}
	var certID float64
	if value, ok := portal["cert_id"].(float64); ok {
		certID = value
	}
	_, err = uploadSafelineCert(client, base, token, certID, cert)
	return err
}

func safelineClient(cfg map[string]string) (string, string, *http.Client, error) {
	base := panelURL(cfg["panel_url"])
	token := strings.TrimSpace(cfg["api_token"])
	if base == "" {
		return "", "", nil, i18n.E("service.deploy.panel_url_required")
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return "", "", nil, err
	}
	if token == "" {
		return "", "", nil, i18n.E("service.deploy.safeline_token_required")
	}
	return base, token, deployHTTPClient(skipVerify(cfg)), nil
}

func uploadSafelineCert(client *http.Client, base, token string, certID float64, cert *deploy.NativeCertificate) (float64, error) {
	payload := map[string]any{
		"manual": map[string]string{"crt": cert.FullchainPEM, "key": cert.KeyPEM},
		"type":   2,
	}
	if certID > 0 {
		payload["id"] = certID
	}
	data, err := safelineRequest(client, base, token, http.MethodPost, "/api/open/cert", payload)
	if err != nil {
		return 0, err
	}
	var id float64
	if err := json.Unmarshal(data, &id); err != nil || id <= 0 {
		return 0, i18n.E("service.deploy.safeline_cert_id_missing")
	}
	return id, nil
}

// Verify 只读检查面板认证与目标对象；不会上传证书或修改面板设置。
func (baotaPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyBaotaPanelChecks(ctx, req, "")
}

func (baotaDockerPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyBaotaPanelChecks(ctx, req, "docker")
}

func (baotaSitePlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyBaotaPanelChecks(ctx, req, "site")
}

func (baotaPanelPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyBaotaPanelChecks(ctx, req, "panel")
}

func (onePanelPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyOnePanelChecks(ctx, req)
}

func (onePanelSitePlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyOnePanelChecks(ctx, req)
}

func (onePanelPanelPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyOnePanelChecks(ctx, req)
}

func (safelinePlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifySafelineChecks(ctx, req)
}

func (safelinePanelPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifySafelineChecks(ctx, req)
}

func (safelinePortalPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifySafelineChecks(ctx, req)
}

func verifyBaotaPanelChecks(ctx context.Context, req deploy.NativeRequest, forcedTarget string) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	target := forcedTarget
	if target == "" {
		target = strings.TrimSpace(cfg["target"])
		if target == "" {
			target = "site"
		}
	}
	listTarget := "site"
	if target == "docker" {
		listTarget = "docker"
	}
	options, err := baotaListOptions(ctx, cfg, listTarget, "")
	if err != nil {
		return failedVerifyCheck(loc, err), nil
	}
	checks := []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
	}
	if target == "panel" {
		return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)}), nil
	}
	wanted := util.SplitList(cfg["sites"])
	if len(wanted) == 0 {
		return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)}), nil
	}
	existing := make(map[string]bool, len(options))
	for _, option := range options {
		existing[option.Value] = true
	}
	var missing []string
	for _, name := range wanted {
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
	return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.target_ok", nil)}), nil
}

func verifyOnePanelChecks(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	base := panelURL(cfg["panel_url"])
	apiKey := strings.TrimSpace(cfg["api_key"])
	if base == "" {
		return failedVerifyCheck(loc, i18n.E("service.deploy.panel_url_required")), nil
	}
	if err := outbound.ValidateServiceURL(base); err != nil {
		return failedVerifyCheck(loc, err), nil
	}
	if apiKey == "" {
		return failedVerifyCheck(loc, i18n.E("service.deploy.onepanel_key_required")), nil
	}
	client := deployHTTPClient(skipVerify(cfg))
	version := onePanelVersion(cfg["api_version"])
	payload := map[string]any{
		"page":     1,
		"pageSize": 10,
		"name":     "",
		"order":    "null",
		"orderBy":  "created_at",
	}
	if _, err := onePanelRequest(client, base, version, apiKey, http.MethodPost, "/websites/search", payload); err != nil {
		return failedVerifyCheck(loc, err), nil
	}
	checks := []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
	}
	target := strings.TrimSpace(cfg["target"])
	website := strings.TrimSpace(cfg["website"])
	if target == "panel" || website == "" {
		return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)}), nil
	}
	if _, err := onePanelResolveWebsite(client, base, version, apiKey, website); err != nil {
		checks = append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusFailed, Message: err.Error()})
		return checks, nil
	}
	return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.target_ok", nil)}), nil
}

func verifySafelineChecks(_ context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	base, token, client, err := safelineClient(cfg)
	if err != nil {
		return failedVerifyCheck(loc, err), nil
	}
	data, err := safelineRequest(client, base, token, http.MethodGet, "/api/open/cert", nil)
	if err != nil {
		return failedVerifyCheck(loc, err), nil
	}
	var result struct {
		Nodes []safelineCert `json:"nodes"`
	}
	_ = json.Unmarshal(data, &result)
	checks := []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
	}
	target := strings.TrimSpace(cfg["target"])
	if target == "portal" {
		if _, err := safelineRequest(client, base, token, http.MethodGet, "/api/open/portal", nil); err != nil {
			checks = append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusFailed, Message: err.Error()})
			return checks, nil
		}
	}
	rawIDs := util.SplitList(cfg["cert_ids"])
	if len(rawIDs) == 0 {
		return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)}), nil
	}
	existing := make(map[int64]bool, len(result.Nodes))
	for _, node := range result.Nodes {
		existing[node.ID] = true
	}
	var missing []string
	for _, raw := range rawIDs {
		id, convErr := strconv.ParseInt(raw, 10, 64)
		if convErr != nil || !existing[id] {
			missing = append(missing, raw)
		}
	}
	if len(missing) > 0 {
		checks = append(checks, protocol.VerifyCheck{
			Key: "scope", Status: protocol.VerifyStatusFailed,
			Message: i18n.T(loc, "service.verify.target_missing", map[string]any{"target": strings.Join(missing, ", ")}),
		})
		return checks, nil
	}
	return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.target_ok", nil)}), nil
}

func failedVerifyCheck(loc i18n.Locale, err error) []protocol.VerifyCheck {
	if err == nil {
		return nil
	}
	return []protocol.VerifyCheck{{
		Key: "verify", Status: protocol.VerifyStatusFailed,
		Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}),
	}}
}
