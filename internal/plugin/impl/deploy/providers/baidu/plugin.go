// Package baidu 通过百度智能云 CDN API 上传并绑定域名证书。
//
// 对应 ALLinSSL 的 baidu-cdn 部署目标。
package baidu

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
	sdkbaidu "github.com/AXmishell/LACTSSL-Plugins/sdk/baidu"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "baidu_cdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	return deployBaiduCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.Register(plugin{}) }

var baiduCDNBaseURL = "https://cdn.baidubce.com"

type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

func deployBaiduCDN(cert *deploy.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key"])
	secretKey := cfg["secret_key"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.baidu_key_required")
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
		if err := putCertificate(client, accessKey, secretKey, domain, name, cert); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func putCertificate(client *http.Client, accessKey, secretKey, domain, name string, cert *deploy.Certificate) error {
	payload := map[string]any{
		"certificate": map[string]string{
			"certName":        name,
			"certServerData":  cert.FullchainPEM,
			"certPrivateData": cert.KeyPEM,
			"certLinkData":    "",
		},
		"httpsEnable": "ON",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	endpoint := strings.TrimRight(baiduCDNBaseURL, "/") + "/v2/" + url.PathEscape(domain) + "/certificates"
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if err := (sdkbaidu.Signer{AccessKey: accessKey, SecretKey: secretKey}).Sign(req, body); err != nil {
		return i18n.E("service.deploy.baidu_sign_failed", "error", err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.baidu_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr errorResponse
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Code != "" {
			return i18n.E("service.deploy.baidu_error", "code", apiErr.Code, "message", apiErr.Message)
		}
		return i18n.E("service.deploy.baidu_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return nil
}
