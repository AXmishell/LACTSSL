// Package ucloud 通过 UCloud CDN API 上传证书并更新域名 HTTPS 配置。
//
// 对应 certd 的「UCloud-部署到CDN」插件。
package ucloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	sdkucloud "github.com/AXmishell/LACTSSL-Plugins/sdk/ucloud"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "ucloud_cdn" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployUCloudCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

var ucloudBaseURL = "https://api.ucloud.cn"

type apiResult struct {
	RetCode int    `json:"RetCode"`
	Message string `json:"Message"`
}

type domainConfig struct {
	Domain             string `json:"Domain"`
	DomainID           string `json:"DomainId"`
	HttpsStatusAbroad  string `json:"HttpsStatusAbroad"`
	HttpsStatusCn      string `json:"HttpsStatusCn"`
	EnableHTTP2        string `json:"EnableHttp2"`
	RedirectHTTP2HTTPS string `json:"RedirectHttp2Https"`
	TLSVersion         string `json:"TlsVersion"`
}

type certificate struct {
	CertificateID util.FlexInt64 `json:"CertificateID"`
	Domains       string         `json:"Domains"`
}

func deployUCloudCDN(cert *deploy.NativeCertificate, cfg map[string]string) error {
	projectID := strings.TrimSpace(cfg["project_id"])
	publicKey := strings.TrimSpace(cfg["public_key"])
	privateKey := cfg["private_key"]
	if publicKey == "" || privateKey == "" {
		return i18n.E("service.deploy.ucloud_key_required")
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
	certID, err := uploadCertificate(client, projectID, publicKey, privateKey, name, cert)
	if err != nil {
		return err
	}

	region := strings.TrimSpace(cfg["region"])
	if region == "" {
		region = "cn-bj2"
	}
	var errs []string
	for _, domain := range domains {
		if err := deployDomain(client, projectID, publicKey, privateKey, region, domain, name, certID); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func uploadCertificate(client *http.Client, projectID, publicKey, privateKey, name string, cert *deploy.NativeCertificate) (int64, error) {
	crtBase64 := base64.StdEncoding.EncodeToString([]byte(cert.FullchainPEM))
	keyBase64 := base64.StdEncoding.EncodeToString([]byte(cert.KeyPEM))
	sum := md5.Sum([]byte(crtBase64 + keyBase64))
	params := map[string]string{
		"Action":          "UploadNormalCertificate",
		"SslPublicKey":    crtBase64,
		"SslPrivateKey":   keyBase64,
		"CertificateName": name,
		"SslMD5":          hex.EncodeToString(sum[:]),
	}
	if projectID != "" {
		params["ProjectId"] = projectID
	}
	data, err := call(client, params, publicKey, privateKey)
	if err != nil {
		if strings.Contains(err.Error(), "重复上传证书") {
			existing, findErr := findCertificate(client, projectID, publicKey, privateKey, util.FirstDomain(cert), strings.Join(util.Domains(cert), ","))
			if findErr == nil && existing > 0 {
				return existing, nil
			}
		}
		return 0, err
	}
	var result struct {
		CertificateID util.FlexInt64 `json:"CertificateID"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, i18n.E("service.deploy.ucloud_response_invalid", "error", err.Error())
	}
	id := result.CertificateID.Int64()
	if id <= 0 {
		return 0, i18n.E("service.deploy.ucloud_cert_id_missing")
	}
	return id, nil
}

func findCertificate(client *http.Client, projectID, publicKey, privateKey, domain, allDomains string) (int64, error) {
	params := map[string]string{
		"Action": "GetCertificateList",
		"Mode":   "trust",
		"Domain": domain,
		"Sort":   "2",
	}
	if projectID != "" {
		params["ProjectId"] = projectID
	}
	data, err := call(client, params, publicKey, privateKey)
	if err != nil {
		return 0, err
	}
	var result struct {
		CertificateList []certificate `json:"CertificateList"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	for _, item := range result.CertificateList {
		if strings.TrimSpace(item.Domains) != "" && sameDomainSet(item.Domains, allDomains) {
			id := item.CertificateID.Int64()
			return id, nil
		}
	}
	return 0, nil
}

func sameDomainSet(left, right string) bool {
	normalize := func(value string) map[string]bool {
		out := map[string]bool{}
		for _, item := range strings.Split(value, ",") {
			item = strings.ToLower(strings.TrimSpace(item))
			if item != "" {
				out[item] = true
			}
		}
		return out
	}
	a, b := normalize(left), normalize(right)
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func deployDomain(client *http.Client, projectID, publicKey, privateKey, region, domain, name string, certID int64) error {
	params := map[string]string{
		"Action":   "GetUcdnDomainConfig",
		"Domain.0": domain,
	}
	if projectID != "" {
		params["ProjectId"] = projectID
	}
	data, err := callWithRegion(client, params, publicKey, privateKey, region)
	if err != nil {
		return err
	}
	var result struct {
		DomainList []domainConfig `json:"DomainList"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return i18n.E("service.deploy.ucloud_response_invalid", "error", err.Error())
	}
	var item *domainConfig
	for index := range result.DomainList {
		if strings.EqualFold(result.DomainList[index].Domain, domain) {
			item = &result.DomainList[index]
			break
		}
	}
	if item == nil {
		return i18n.E("service.deploy.ucloud_domain_not_found", "domain", domain)
	}
	httpsCN := item.HttpsStatusCn
	if item.HttpsStatusAbroad == "disable" && httpsCN == "disable" {
		httpsCN = "enable"
	}
	update := map[string]string{
		"Action":             "UpdateUcdnDomainHttpsConfigV2",
		"DomainId":           item.DomainID,
		"CertName":           name,
		"CertId":             strconv.FormatInt(certID, 10),
		"CertType":           "ussl",
		"EnableHttp2":        nonEmpty(item.EnableHTTP2, "0"),
		"RedirectHttp2Https": nonEmpty(item.RedirectHTTP2HTTPS, "1"),
		"TlsVersion":         nonEmpty(item.TLSVersion, "tlsv1.2,tlsv1.3"),
	}
	if item.HttpsStatusAbroad == "enable" {
		update["HttpsStatusAbroad"] = "enable"
	}
	if httpsCN == "enable" {
		update["HttpsStatusCn"] = "enable"
	}
	if projectID != "" {
		update["ProjectId"] = projectID
	}
	_, err = callWithRegion(client, update, publicKey, privateKey, region)
	return err
}

func call(client *http.Client, params map[string]string, publicKey, privateKey string) (json.RawMessage, error) {
	return callWithRegion(client, params, publicKey, privateKey, "")
}

func callWithRegion(client *http.Client, params map[string]string, publicKey, privateKey, region string) (json.RawMessage, error) {
	if region != "" {
		params["Region"] = region
	}
	body := sdkucloud.Encode(params, publicKey, privateKey)
	req, err := http.NewRequest(http.MethodPost, ucloudBaseURL, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.ucloud_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result apiResult
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.RetCode != 0 {
		if result.Message != "" {
			return nil, i18n.E("service.deploy.ucloud_error", "code", result.RetCode, "message", result.Message)
		}
		return nil, i18n.E("service.deploy.ucloud_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	// 除 RetCode/Message 外的字段就是接口数据，直接返回完整响应供调用方解码。
	return raw, nil
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
