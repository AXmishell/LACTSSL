// Package huawei 通过华为云 CDN API 更新域名 HTTPS 证书。
//
// 对应 certd 的「华为云-部署证书至CDN」插件。
package huawei

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/huawei"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "huawei_cdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployHuaweiCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

// Verify 只读列出 CDN 域名，验证 AK/SK 与 CDN 服务权限。
func (plugin) Verify(_ context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["secret_access_key"]
	if accessKey == "" || secretKey == "" {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": i18n.T(loc, "service.deploy.huawei_key_required", nil)})}}, nil
	}
	endpoint := strings.TrimRight(huaweiCDNBaseURL, "/") + "/v1.0/cdn/domains?page_size=1"
	reqHTTP, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}, nil
	}
	if err := (huawei.Signer{AccessKey: accessKey, SecretKey: secretKey}).Sign(reqHTTP, nil); err != nil {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}, nil
	}
	resp, err := outbound.NewClient(30*time.Second, false).Do(reqHTTP)
	if err != nil {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": util.Truncate(string(raw), 300)})}}, nil
	}
	return []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
		{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)},
	}, nil
}

func init() {
	deploy.MarkNativeVerifiable("huawei_cdn")
	deploy.RegisterNative(plugin{})
}

var huaweiCDNBaseURL = "https://cdn.myhuaweicloud.com"

func deployHuaweiCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["secret_access_key"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.huawei_key_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	client := outbound.NewClient(30*time.Second, false)
	var errs []string
	for _, domain := range domains {
		if err := updateHuaweiDomain(client, accessKey, secretKey, domain, name, cfg["enterprise_project_id"], cert); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func updateHuaweiDomain(client *http.Client, accessKey, secretKey, domain, name, projectID string, cert *deploy.NativeCertificate) error {
	payload := map[string]any{
		"configs": map[string]any{
			"https": map[string]any{
				"https_status":       "on",
				"certificate_type":   "server",
				"certificate_source": 0,
				"certificate_name":   name,
				"certificate_value":  cert.FullchainPEM,
				"private_key":        cert.KeyPEM,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	endpoint := strings.TrimRight(huaweiCDNBaseURL, "/") + "/v1.0/cdn/domains/" + url.PathEscape(domain) + "/configs"
	if strings.TrimSpace(projectID) != "" {
		endpoint += "?enterprise_project_id=" + url.QueryEscape(strings.TrimSpace(projectID))
	}
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if err := (huawei.Signer{AccessKey: accessKey, SecretKey: secretKey}).Sign(req, body); err != nil {
		return i18n.E("service.deploy.huawei_sign_failed", "error", err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.huawei_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.huawei_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return nil
}
