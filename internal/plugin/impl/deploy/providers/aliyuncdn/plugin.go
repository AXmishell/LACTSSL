// Package aliyuncdn 调用阿里云 CDN API 更新域名证书。
package aliyuncdn

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "aliyun_cdn" }
func (plugin) Aliases() []string { return nil }

func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	return deployAliyunCDN(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.Register(plugin{}) }

// aliyunPercentEncode 按阿里云 RPC 签名要求进行 RFC3986 编码。
func aliyunPercentEncode(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' {
			buf.WriteByte(b)
		} else {
			fmt.Fprintf(&buf, "%%%02X", b)
		}
	}
	return buf.String()
}

// DeployCDN 供合并后的 aliyun 规范目标复用 CDN 部署实现。
func DeployCDN(cert *deploy.Certificate, cfg map[string]string) error {
	return deployAliyunCDN(cert, cfg)
}

func deployAliyunCDN(cert *deploy.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := strings.TrimSpace(cfg["access_key_secret"])
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.aliyun_key_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = cert.Domains
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}

	var errs []string
	for _, domain := range domains {
		if err := setAliyunCDNCert(accessKey, secretKey, domain, cfg["cert_name"], cert); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func setAliyunCDNCert(accessKey, secretKey, domain, certName string, cert *deploy.Certificate) error {
	if certName == "" {
		certName = cert.Domain + "-" + time.Now().Format("20060102150405")
	}
	params := map[string]string{
		"Action":           "SetCdnDomainSSLCertificate",
		"Version":          "2018-05-10",
		"Format":           "JSON",
		"AccessKeyId":      accessKey,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"DomainName":       domain,
		"SSLProtocol":      "on",
		"SSLPub":           cert.FullchainPEM,
		"SSLPri":           cert.KeyPEM,
		"CertType":         "upload",
		"CertName":         certName,
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for i, k := range keys {
		if i > 0 {
			canonical.WriteString("&")
		}
		canonical.WriteString(aliyunPercentEncode(k))
		canonical.WriteString("=")
		canonical.WriteString(aliyunPercentEncode(params[k]))
	}
	stringToSign := "POST&%2F&" + aliyunPercentEncode(canonical.String())
	mac := hmac.New(sha1.New, []byte(secretKey+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	form := canonical.String() + "&Signature=" + aliyunPercentEncode(signature)
	client := outbound.NewClient(30*time.Second, false)
	req, err := http.NewRequest(http.MethodPost, "https://cdn.aliyuncs.com/", strings.NewReader(form))
	if err != nil {
		return i18n.E("service.deploy.aliyun_request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.aliyun_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return i18n.E("service.deploy.aliyun_status", "status", resp.StatusCode, "body", util.Truncate(string(data), 300))
	}
	var result struct {
		Code      string `json:"Code"`
		Message   string `json:"Message"`
		RequestID string `json:"RequestId"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(bytes.TrimSpace(data)) == 0 {
		return i18n.E("service.deploy.aliyun_response_invalid", "error", "empty or invalid JSON response")
	}
	if result.Code != "" {
		return i18n.E("service.deploy.aliyun_error", "code", result.Code, "message", result.Message)
	}
	return nil
}
