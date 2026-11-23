// Package volcengine 通过火山引擎 CDN API 上传证书并更新加速域名。
//
// 对应 certd 的「火山引擎-部署证书至CDN」插件。
package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/volcengine"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type volcenginePlugin struct{}
type plugin struct{}
type dcdnPlugin struct{}

func (volcenginePlugin) ID() string        { return "volcengine" }
func (volcenginePlugin) Aliases() []string { return nil }
func (volcenginePlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "cdn":
		return deployVolcengineCDN(cert, cfg)
	case "dcdn":
		return deployVolcengineDCDN(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

func (plugin) ID() string        { return "volcengine_cdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployVolcengineCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (dcdnPlugin) ID() string        { return "volcengine_dcdn" }
func (dcdnPlugin) Aliases() []string { return nil }
func (dcdnPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployVolcengineDCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() {
	deploy.RegisterNative(volcenginePlugin{})
	deploy.RegisterNative(plugin{})
	deploy.RegisterNative(dcdnPlugin{})
}

var (
	volcengineBaseURL     = "https://cdn.volcengineapi.com"
	volcengineDCDNBaseURL = "https://dcdn.volcengineapi.com"
)

const (
	cdnService  = "cdn"
	cdnVersion  = "2021-03-01"
	cdnRegion   = "cn-north-1"
	dcdnService = "dcdn"
	dcdnVersion = "2021-04-01"
)

type envelope struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	} `json:"ResponseMetadata"`
	Result json.RawMessage `json:"Result"`
}

var duplicatedIDPattern = regexp.MustCompile(`(?i)(?:ID为|ID is)\s*([A-Za-z0-9_-]+)`)

func deployVolcengineCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["secret_access_key"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.volcengine_key_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	region := strings.TrimSpace(cfg["region"])
	if region == "" {
		region = cdnRegion
	}
	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}

	client := outbound.NewClient(30*time.Second, false)
	certID, err := uploadCertificate(client, accessKey, secretKey, region, name, cert)
	if err != nil {
		return err
	}

	var errs []string
	for _, domain := range domains {
		if err := updateDomain(client, accessKey, secretKey, region, domain, certID); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func uploadCertificate(client *http.Client, accessKey, secretKey, region, name string, cert *deploy.NativeCertificate) (string, error) {
	data, err := call(client, accessKey, secretKey, region, volcengineBaseURL, cdnService, cdnVersion, "AddCertificate", map[string]any{
		"Source":      "volc_cert_center",
		"CertType":    "server_cert",
		"Certificate": cert.FullchainPEM,
		"PrivateKey":  cert.KeyPEM,
		"EncryType":   "inter_cert",
		"Repeatable":  false,
		"Desc":        name,
	})
	if err != nil {
		if message := apiErrorMessage(err); message != "" {
			if match := duplicatedIDPattern.FindStringSubmatch(message); len(match) == 2 {
				return match[1], nil
			}
		}
		return "", err
	}
	var result struct {
		CertID string `json:"CertId"`
	}
	_ = json.Unmarshal(data, &result)
	if result.CertID == "" {
		return "", i18n.E("service.deploy.volcengine_cert_id_missing")
	}
	return result.CertID, nil
}

func updateDomain(client *http.Client, accessKey, secretKey, region, domain, certID string) error {
	_, err := call(client, accessKey, secretKey, region, volcengineBaseURL, cdnService, cdnVersion, "UpdateCdnConfig", map[string]any{
		"Domain": domain,
		"HTTPS": map[string]any{
			"CertInfo": map[string]any{"CertId": certID},
			"Switch":   true,
		},
	})
	return err
}

func call(client *http.Client, accessKey, secretKey, region, baseURL, service, version, action string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	query := url.Values{}
	query.Set("Action", action)
	query.Set("Version", version)
	endpoint := strings.TrimRight(baseURL, "/") + "/?" + query.Encode()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if err := (volcengine.Signer{
		AccessKey: accessKey, SecretKey: secretKey,
		Region: region, Service: service,
	}).Sign(req, body); err != nil {
		return nil, i18n.E("service.deploy.volcengine_sign_failed", "error", err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.volcengine_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result envelope
	_ = json.Unmarshal(raw, &result)
	if result.ResponseMetadata.Error != nil {
		return nil, i18n.E("service.deploy.volcengine_error",
			"code", result.ResponseMetadata.Error.Code,
			"message", result.ResponseMetadata.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, i18n.E("service.deploy.volcengine_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return result.Result, nil
}

func deployVolcengineDCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["secret_access_key"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.volcengine_key_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	region := strings.TrimSpace(cfg["region"])
	if region == "" {
		region = cdnRegion
	}
	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	client := outbound.NewClient(30*time.Second, false)
	certID, err := uploadCertificate(client, accessKey, secretKey, region, name, cert)
	if err != nil {
		return err
	}
	var errs []string
	for _, domain := range domains {
		if _, err := call(client, accessKey, secretKey, region, volcengineDCDNBaseURL, dcdnService, dcdnVersion, "CreateCertBind", map[string]any{
			"CertId":      certID,
			"DomainNames": []string{domain},
		}); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

// apiErrorMessage 从结构化错误里提取服务端 message，用于识别重复证书。
func apiErrorMessage(err error) string {
	return err.Error()
}
