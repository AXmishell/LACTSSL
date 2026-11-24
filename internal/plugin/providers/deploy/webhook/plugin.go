// Package webhook 通过 HTTP POST 把证书内容推送到用户配置的接口。
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "webhook" }
func (plugin) Aliases() []string { return nil }

func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployWebhook(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

func deployWebhook(cert *deploy.NativeCertificate, cfg map[string]string) error {
	target := strings.TrimSpace(cfg["url"])
	if target == "" {
		return i18n.E("service.deploy.webhook_url_required")
	}
	if err := outbound.ValidateWebhookURL(target); err != nil {
		return err
	}
	payload := map[string]any{
		"event":      "certificate.deploy",
		"domain":     cert.Domain,
		"domains":    cert.Domains,
		"cert":       cert.CertPEM,
		"key":        cert.KeyPEM,
		"fullchain":  cert.FullchainPEM,
		"issuer":     cert.Issuer,
		"serial":     cert.Serial,
		"not_before": cert.NotBefore,
		"not_after":  cert.NotAfter,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LACTSSL/1.0")
	if auth := strings.TrimSpace(cfg["auth_header"]); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := outbound.NewClient(30*time.Second, cfg["skip_verify"] == "true")
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.callback_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.callback_status", "status", resp.StatusCode, "body", util.Truncate(string(data), 400))
	}
	return nil
}
