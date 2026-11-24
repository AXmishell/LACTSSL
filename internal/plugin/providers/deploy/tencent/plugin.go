// Package tencent 通过腾讯云 SSL 证书服务把证书部署到 CDN 或 COS。
//
// 对应 certd 的「腾讯云-部署到CDN-v2」与「腾讯云-部署证书到COS」插件。
package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/tencentcloud"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type tencentPlugin struct{}
type cdnPlugin struct{}
type cosPlugin struct{}
type wafPlugin struct{}
type teoPlugin struct{}

func (tencentPlugin) ID() string        { return "tencent" }
func (tencentPlugin) Aliases() []string { return nil }
func (tencentPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	cfg := util.Merge(req.Auth, req.Config)
	target := strings.TrimSpace(cfg["target"])
	switch target {
	case "", "cdn", "cos", "teo", "waf":
		if target == "" {
			target = "cdn"
		}
		return deployTencent(target, util.Certificate(req.Certificate), cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", target)
	}
}

func (cdnPlugin) ID() string        { return "tencent_cdn" }
func (cdnPlugin) Aliases() []string { return nil }
func (cdnPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployTencent("cdn", util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (cosPlugin) ID() string        { return "tencent_cos" }
func (cosPlugin) Aliases() []string { return nil }
func (cosPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployTencent("cos", util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (wafPlugin) ID() string        { return "tencent_waf" }
func (wafPlugin) Aliases() []string { return nil }
func (wafPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployTencent("waf", util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (teoPlugin) ID() string        { return "tencent_teo" }
func (teoPlugin) Aliases() []string { return nil }
func (teoPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployTencent("teo", util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() {
	deploy.MarkNativeVerifiable("tencent", "tencent_cdn", "tencent_cos", "tencent_waf", "tencent_teo")
	deploy.RegisterNative(tencentPlugin{})
	deploy.RegisterNative(cdnPlugin{})
	deploy.RegisterNative(cosPlugin{})
	deploy.RegisterNative(wafPlugin{})
	deploy.RegisterNative(teoPlugin{})
}

// 测试时可替换为本地服务地址。
var tencentSSLBaseURL = ""

const (
	sslService = "ssl"
	sslVersion = "2019-12-05"
)

// newTencentClient 构造 SSL 服务客户端。SSL 是全局服务，region 只作为
// X-TC-Region 传递，不能拼进端点。
func newTencentClient(cfg map[string]string) (*tencentcloud.Client, string, error) {
	secretID := strings.TrimSpace(cfg["secret_id"])
	secretKey := cfg["secret_key"]
	if secretID == "" || secretKey == "" {
		return nil, "", i18n.E("service.deploy.tencent_key_required")
	}
	region := strings.TrimSpace(cfg["region"])
	client := &tencentcloud.Client{
		Credentials: tencentcloud.Credentials{SecretID: secretID, SecretKey: secretKey},
		HTTP:        util.Client(cfg),
	}
	switch {
	case tencentSSLBaseURL != "":
		client.Endpoint = tencentSSLBaseURL
	case util.Bool(cfg["intl"]):
		client.Endpoint = "https://ssl.intl.tencentcloudapi.com"
	default:
		client.Endpoint = "https://ssl.tencentcloudapi.com"
	}
	return client, region, nil
}

func deployTencent(target string, cert *deploy.NativeCertificate, cfg map[string]string) error {
	client, region, err := newTencentClient(cfg)
	if err != nil {
		return err
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	if target == "cos" {
		if region == "" {
			return i18n.E("service.deploy.tencent_cos_region_required")
		}
		bucket := strings.TrimSpace(cfg["bucket"])
		if bucket == "" {
			return i18n.E("service.deploy.tencent_cos_bucket_required")
		}
	}

	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	var uploaded struct {
		CertificateID string `json:"CertificateId"`
	}
	if _, err := client.Do(context.Background(), sslService, region, "UploadCertificate", sslVersion, map[string]any{
		"CertificatePublicKey":  cert.FullchainPEM,
		"CertificatePrivateKey": cert.KeyPEM,
		"Alias":                 name,
	}, &uploaded); err != nil {
		return err
	}
	if uploaded.CertificateID == "" {
		return i18n.E("service.deploy.tencent_cert_id_missing")
	}

	instances := make([]string, 0, len(domains))
	for _, domain := range domains {
		switch target {
		case "cos":
			// COS: Region|Bucket|Domain
			instances = append(instances, fmt.Sprintf("%s|%s|%s", region, strings.TrimSpace(cfg["bucket"]), domain))
		case "cdn":
			// CDN: Domain|计费开关；部署证书时开启 HTTPS。
			instances = append(instances, domain+"|on")
		default:
			// WAF 等资源直接使用域名。
			instances = append(instances, domain)
		}
	}
	if _, err := client.Do(context.Background(), sslService, region, "DeployCertificateInstance", sslVersion, map[string]any{
		"CertificateId":  uploaded.CertificateID,
		"ResourceType":   target,
		"Status":         1,
		"InstanceIdList": instances,
	}, nil); err != nil {
		return err
	}
	return nil
}

// Verify 通过 DescribeCertificates 只读检查腾讯云凭据。
func (tencentPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyTencent(ctx, req)
}

func (cdnPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyTencent(ctx, req)
}

func (cosPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyTencent(ctx, req)
}

func (wafPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyTencent(ctx, req)
}

func (teoPlugin) Verify(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	return verifyTencent(ctx, req)
}

func verifyTencent(ctx context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	client, region, err := newTencentClient(cfg)
	if err != nil {
		return tencentVerifyFailed(loc, err), nil
	}
	var result struct {
		TotalCount int `json:"TotalCount"`
	}
	if _, err := client.Do(ctx, sslService, region, "DescribeCertificates", sslVersion, map[string]any{"Limit": 1}, &result); err != nil {
		return tencentVerifyFailed(loc, err), nil
	}
	return []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
		{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)},
	}, nil
}

func tencentVerifyFailed(loc i18n.Locale, err error) []protocol.VerifyCheck {
	if err == nil {
		return nil
	}
	return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}
}
