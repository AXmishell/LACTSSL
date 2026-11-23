// Package aliyunoss 把证书绑定到阿里云 OSS 自定义域名。
//
// 对应 certd 的「阿里云-部署证书至OSS」插件。注意这里部署的是 OSS
// 自定义域名证书，而不是把证书文件上传到存储桶。
package aliyunoss

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/aliyunoss"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "aliyun_oss" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	return deployAliyunOSS(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.Register(plugin{}) }

var aliyunOSSScheme = "https"

var aliyunOSSHost = func(bucket, region string) string {
	return bucket + "." + region + ".aliyuncs.com"
}

var ossNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}[a-z0-9]$`)

type bucketCnameConfiguration struct {
	XMLName xml.Name `xml:"BucketCnameConfiguration"`
	Cname   struct {
		Domain                   string `xml:"Domain"`
		CertificateConfiguration struct {
			PrivateKey  string `xml:"PrivateKey,omitempty"`
			Certificate string `xml:"Certificate,omitempty"`
			Force       bool   `xml:"Force"`
		} `xml:"CertificateConfiguration"`
	} `xml:"Cname"`
}

type ossError struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// DeployOSS 供合并后的 aliyun 规范目标复用 OSS 自定义域名部署实现。
func DeployOSS(cert *deploy.Certificate, cfg map[string]string) error {
	return deployAliyunOSS(cert, cfg)
}

func deployAliyunOSS(cert *deploy.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["access_key_secret"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.aliyun_key_required")
	}
	region := strings.TrimSpace(cfg["region"])
	bucket := strings.TrimSpace(cfg["bucket"])
	if region == "" || bucket == "" {
		return i18n.E("service.deploy.aliyun_oss_bucket_region_required")
	}
	if !ossNamePattern.MatchString(region) || !ossNamePattern.MatchString(bucket) {
		return i18n.E("service.deploy.aliyun_oss_name_invalid")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}

	client := outbound.NewClient(30*time.Second, false)
	var errs []string
	for _, domain := range domains {
		if err := setCnameCertificate(client, accessKey, secretKey, region, bucket, domain, cert); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func setCnameCertificate(client *http.Client, accessKey, secretKey, region, bucket, domain string, cert *deploy.Certificate) error {
	var config bucketCnameConfiguration
	config.Cname.Domain = domain
	config.Cname.CertificateConfiguration.PrivateKey = cert.KeyPEM
	config.Cname.CertificateConfiguration.Certificate = cert.FullchainPEM
	config.Cname.CertificateConfiguration.Force = true
	body, err := xml.Marshal(config)
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	body = append([]byte(xml.Header), body...)
	sum := md5.Sum(body)
	contentMD5 := base64.StdEncoding.EncodeToString(sum[:])

	endpoint := aliyunOSSScheme + "://" + aliyunOSSHost(bucket, region) + "/?cname&comp=add"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	resource := aliyunoss.CanonicalResource(bucket, "/", map[string]string{"cname": "", "comp": "add"})
	req.Header.Set("Authorization", aliyunoss.SetHeaders(req, aliyunoss.Credentials{
		AccessKeyID: accessKey, AccessKeySecret: secretKey,
	}, resource, contentMD5, "application/xml", time.Now()))
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.aliyun_oss_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr ossError
		if xml.Unmarshal(raw, &apiErr) == nil && apiErr.Code != "" {
			return i18n.E("service.deploy.aliyun_oss_error", "code", apiErr.Code, "message", apiErr.Message)
		}
		return i18n.E("service.deploy.aliyun_oss_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return nil
}
