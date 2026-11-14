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

	"lactssl/internal/deployplugin"
	"lactssl/internal/deployplugin/pluginutil"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/outbound"
)

type plugin struct{}

func (plugin) ID() string                  { return "webhook" }
func (plugin) Aliases() []string           { return nil }
func (plugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }

func (plugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployWebhook(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func init() { deployplugin.Register(plugin{}) }

func deployWebhook(cert *model.Certificate, cfg map[string]string) error {
	target := strings.TrimSpace(cfg["url"])
	if target == "" {
		return i18n.E("service.deploy.webhook_url_required")
	}
	if err := outbound.ValidateWebhookURL(target); err != nil {
		return err
	}
	payload := map[string]any{
		"event":      "certificate.deploy",
		"domain":     cert.PrimaryDomain,
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
		return i18n.E("service.deploy.callback_status", "status", resp.StatusCode, "body", pluginutil.Truncate(string(data), 400))
	}
	return nil
}
