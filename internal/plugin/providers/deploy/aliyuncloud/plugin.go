// Package aliyuncloud 实现阿里云 DCDN、ESA 与 WAF 的证书部署。
//
// 对应 ALLinSSL 的 aliyun-dcdn / aliyun-esa / aliyun-waf 部署目标。
package aliyuncloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/alibaba"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/httpapi"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/validate"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type dcdnPlugin struct{}
type esaPlugin struct{}
type wafPlugin struct{}

func (dcdnPlugin) ID() string        { return "aliyun_dcdn" }
func (dcdnPlugin) Aliases() []string { return nil }
func (dcdnPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployDCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (esaPlugin) ID() string        { return "aliyun_esa" }
func (esaPlugin) Aliases() []string { return nil }
func (esaPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployESA(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func (wafPlugin) ID() string        { return "aliyun_waf" }
func (wafPlugin) Aliases() []string { return nil }
func (wafPlugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployWAF(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() {
	deploy.RegisterNative(dcdnPlugin{})
	deploy.RegisterNative(esaPlugin{})
	deploy.RegisterNative(wafPlugin{})
}

var (
	aliyunDCDNEndpoint = "https://dcdn.aliyuncs.com"
	aliyunESAEndpoint  = "https://esa.ap-southeast-1.aliyuncs.com"
	aliyunCASEndpoint  = "https://cas.aliyuncs.com"
	aliyunWAFEndpoint  = "https://wafopenapi.cn-hangzhou.aliyuncs.com"
)

func credentials(cfg map[string]string) (string, string, error) {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["access_key_secret"]
	if accessKey == "" || secretKey == "" {
		return "", "", i18n.E("service.deploy.aliyun_key_required")
	}
	return accessKey, secretKey, nil
}

// DeployDCDN 供合并后的 aliyun 规范目标复用 DCDN 部署实现。
func DeployDCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	return deployDCDN(cert, cfg)
}

// DeployESA 供合并后的 aliyun 规范目标复用 ESA 部署实现。
func DeployESA(cert *deploy.NativeCertificate, cfg map[string]string) error {
	return deployESA(cert, cfg)
}

// DeployWAF 供合并后的 aliyun 规范目标复用 WAF 部署实现。
func DeployWAF(cert *deploy.NativeCertificate, cfg map[string]string) error {
	return deployWAF(cert, cfg)
}

func deployDCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey, secretKey, err := credentials(cfg)
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
	client := httpapi.New(aliyunDCDNEndpoint)
	signer := alibaba.Signer{AccessKeyID: accessKey, AccessKeySecret: secretKey}
	var errs []string
	for _, domain := range domains {
		params := map[string]string{
			"DomainName":  domain,
			"SSLPub":      cert.FullchainPEM,
			"SSLPri":      cert.KeyPEM,
			"SSLProtocol": "on",
			"CertType":    "upload",
		}
		if err := callAliyun(context.Background(), signer, client, true, aliyunDCDNEndpoint, "SetDcdnDomainSSLCertificate", "2018-01-15", params, nil); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func deployESA(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey, secretKey, err := credentials(cfg)
	if err != nil {
		return err
	}
	siteID := strings.TrimSpace(cfg["site_id"])
	if siteID == "" {
		return i18n.E("service.deploy.aliyun_esa_site_required")
	}
	if _, err := strconv.ParseInt(siteID, 10, 64); err != nil {
		return i18n.E("service.deploy.aliyun_esa_site_invalid", "value", siteID)
	}
	client := httpapi.New(aliyunESAEndpoint)
	signer := alibaba.Signer{AccessKeyID: accessKey, AccessKeySecret: secretKey}
	params := map[string]string{
		"SiteId":      siteID,
		"Type":        "upload",
		"Certificate": cert.FullchainPEM,
		"PrivateKey":  cert.KeyPEM,
	}
	return callAliyun(context.Background(), signer, client, true, aliyunESAEndpoint, "SetCertificate", "2024-09-10", params, nil)
}

func deployWAF(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey, secretKey, err := credentials(cfg)
	if err != nil {
		return err
	}
	region, err := validate.RegionOr(cfg["region"], "cn-hangzhou")
	if err != nil {
		return i18n.E("service.deploy.region_invalid", "region", strings.TrimSpace(cfg["region"]))
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}
	casClient := httpapi.New(aliyunCASEndpoint)
	wafEndpoint := strings.Replace(aliyunWAFEndpoint, "cn-hangzhou", region, 1)
	wafClient := httpapi.New(wafEndpoint)
	signer := alibaba.Signer{AccessKeyID: accessKey, AccessKeySecret: secretKey}

	var uploaded struct {
		CertID int64 `json:"CertId"`
	}
	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	if err := callAliyun(context.Background(), signer, casClient, true, aliyunCASEndpoint, "UploadUserCertificate", "2020-04-07", map[string]string{
		"Cert": cert.FullchainPEM,
		"Key":  cert.KeyPEM,
		"Name": name,
	}, &uploaded); err != nil {
		return err
	}
	if uploaded.CertID <= 0 {
		return i18n.E("service.deploy.aliyun_waf_cert_id_missing")
	}

	var instance struct {
		InstanceID string `json:"InstanceId"`
	}
	if err := callAliyun(context.Background(), signer, wafClient, true, wafEndpoint, "DescribeInstance", "2021-10-01", map[string]string{
		"RegionId": region,
	}, &instance); err != nil {
		return err
	}
	if instance.InstanceID == "" {
		return i18n.E("service.deploy.aliyun_waf_instance_missing", "region", region)
	}

	certID := fmt.Sprintf("%d-%s", uploaded.CertID, region)
	var errs []string
	for _, domain := range domains {
		if err := updateWAFDomain(signer, wafClient, wafEndpoint, region, instance.InstanceID, domain, certID, cfg); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func updateWAFDomain(signer alibaba.Signer, client *httpapi.Client, endpoint, region, instanceID, domain, certID string, cfg map[string]string) error {
	var detail struct {
		Listen   map[string]any `json:"Listen"`
		Redirect map[string]any `json:"Redirect"`
	}
	if err := callAliyun(context.Background(), signer, client, true, endpoint, "DescribeDomainDetail", "2021-10-01", map[string]string{
		"RegionId":   region,
		"InstanceId": instanceID,
		"Domain":     domain,
	}, &detail); err != nil {
		return err
	}
	if detail.Listen == nil {
		return i18n.E("service.deploy.aliyun_waf_listen_missing", "domain", domain)
	}
	detail.Listen["CertId"] = certID
	if ports, ok := detail.Listen["HttpsPorts"].([]any); !ok || len(ports) == 0 {
		detail.Listen["HttpsPorts"] = []any{443}
	}
	if detail.Redirect == nil {
		detail.Redirect = map[string]any{}
	}
	if backends, ok := detail.Redirect["AllBackends"]; ok {
		detail.Redirect["Backends"] = backends
	}
	listenJSON, _ := json.Marshal(detail.Listen)
	redirectJSON, _ := json.Marshal(detail.Redirect)
	tlsVersion := strings.TrimSpace(cfg["tls_version"])
	if tlsVersion == "" {
		tlsVersion = "tlsv1.2"
	}
	enableTLSv3 := "true"
	if strings.TrimSpace(cfg["enable_tlsv3"]) != "" {
		enableTLSv3 = strconv.FormatBool(util.Bool(cfg["enable_tlsv3"]))
	}
	return callAliyun(context.Background(), signer, client, true, endpoint, "ModifyDomain", "2021-10-01", map[string]string{
		"RegionId":    region,
		"InstanceId":  instanceID,
		"Domain":      domain,
		"Listen":      string(listenJSON),
		"Redirect":    string(redirectJSON),
		"TLSVersion":  tlsVersion,
		"EnableTLSv3": enableTLSv3,
	}, nil)
}

// callAliyun 统一处理阿里云 RPC 响应：先检查 Code/Message，再解码业务字段。
func callAliyun(ctx context.Context, signer alibaba.Signer, client *httpapi.Client, post bool, endpoint, action, version string, params map[string]string, out any) error {
	var body json.RawMessage
	var err error
	if post {
		err = signer.DoPost(ctx, client, endpoint, action, version, params, &body)
	} else {
		err = signer.Do(ctx, client, endpoint, action, version, params, &body)
	}
	if err != nil {
		return i18n.E("service.deploy.aliyuncloud_request_failed", "action", action, "error", err.Error())
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return i18n.E("service.deploy.aliyuncloud_response_invalid", "action", action, "error", "empty response")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return i18n.E("service.deploy.aliyuncloud_response_invalid", "action", action, "error", err.Error())
	}
	if code := strings.TrimSpace(string(raw["Code"])); code != "" && code != `""` {
		return i18n.E("service.deploy.aliyuncloud_error", "action", action, "code", code, "message", strings.TrimSpace(string(raw["Message"])))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return i18n.E("service.deploy.aliyuncloud_response_invalid", "action", action, "error", err.Error())
	}
	return nil
}
