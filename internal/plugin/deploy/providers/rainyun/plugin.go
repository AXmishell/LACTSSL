// Package rainyun 通过雨云 API 更新 SSL 证书中心的证书。
//
// 对应 ALLinSSL 的 rainyun-sslcenter 部署目标。
package rainyun

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
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "rainyun" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployRainyun(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

var rainyunBaseURL = "https://api.v2.rainyun.com"

type apiResult struct {
	Code    *int   `json:"code"`
	Message string `json:"message"`
}

func deployRainyun(cert *deploy.NativeCertificate, cfg map[string]string) error {
	apiKey := strings.TrimSpace(cfg["api_key"])
	certID := strings.TrimSpace(cfg["cert_id"])
	if apiKey == "" {
		return i18n.E("service.deploy.rainyun_key_required")
	}
	if certID == "" {
		return i18n.E("service.deploy.rainyun_cert_id_required")
	}
	payload, err := json.Marshal(map[string]string{
		"cert": cert.FullchainPEM,
		"key":  cert.KeyPEM,
	})
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	endpoint := strings.TrimRight(rainyunBaseURL, "/") + "/product/sslcenter/" + url.PathEscape(certID)
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := outbound.NewClient(30*time.Second, false)
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.rainyun_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result apiResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return i18n.E("service.deploy.rainyun_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code == nil || (*result.Code != 0 && *result.Code != 200) {
		if result.Message != "" {
			code := 0
			if result.Code != nil {
				code = *result.Code
			}
			return i18n.E("service.deploy.rainyun_error", "code", code, "message", result.Message)
		}
		return i18n.E("service.deploy.rainyun_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return nil
}
