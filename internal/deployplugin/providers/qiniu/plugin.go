// Package qiniu 通过七牛云 API 上传证书并绑定 CDN 或 Kodo 域名。
package qiniu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lactssl/internal/deployplugin"
	"lactssl/internal/deployplugin/pluginutil"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

type qiniuPlugin struct{}
type cdnPlugin struct{}
type kodoPlugin struct{}

func (qiniuPlugin) ID() string                  { return "qiniu" }
func (qiniuPlugin) Aliases() []string           { return nil }
func (qiniuPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (qiniuPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	cert := pluginutil.Certificate(req.Certificate)
	cfg := pluginutil.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["domain_type"]) {
	case "", "cdn":
		return deployQiniuCDN(cert, cfg)
	case "kodo":
		return deployQiniuKodo(cert, cfg)
	default:
		return i18n.E("service.deploy.qiniu_domain_type_invalid", "type", cfg["domain_type"])
	}
}

func (cdnPlugin) ID() string                  { return "qiniu_cdn" }
func (cdnPlugin) Aliases() []string           { return nil }
func (cdnPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (cdnPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployQiniuCDN(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func (kodoPlugin) ID() string                  { return "qiniu_kodo" }
func (kodoPlugin) Aliases() []string           { return nil }
func (kodoPlugin) Source() deployplugin.Source { return deployplugin.SourceBuiltin }
func (kodoPlugin) Deploy(_ context.Context, req deployplugin.Request) error {
	return deployQiniuKodo(pluginutil.Certificate(req.Certificate), pluginutil.Merge(req.Auth, req.Config))
}

func init() {
	deployplugin.Register(qiniuPlugin{})
	deployplugin.Register(cdnPlugin{})
	deployplugin.Register(kodoPlugin{})
}

// 七牛云的证书上传与域名配置分属两套鉴权：证书接口使用 QBox 路径签名，
// 域名接口使用 Qiniu 请求内容签名。变量便于测试时替换为本地服务地址。
var (
	qiniuCertBaseURL   = "https://fusion.qiniuapi.com"
	qiniuDomainBaseURL = "https://api.qiniu.com"
	// qiniuKodoBaseURL 留空时复用域名 API 地址，便于测试时统一替换。
	qiniuKodoBaseURL = ""
)

func qiniuKodoBase() string {
	if strings.TrimSpace(qiniuKodoBaseURL) != "" {
		return qiniuKodoBaseURL
	}
	return qiniuDomainBaseURL
}

type qiniuAPIResult struct {
	Code   int    `json:"code"`
	Error  string `json:"error"`
	CertID string `json:"certID"`
}

type qiniuAPIResponse struct {
	Status int
	Body   []byte
	Result qiniuAPIResult
}

func (r *qiniuAPIResponse) ok() bool {
	return r.Status >= 200 && r.Status < 300 && (r.Result.Code == 0 || r.Result.Code == 200)
}

func (r *qiniuAPIResponse) err() error {
	return qiniuAPIError(r.Status, r.Body, r.Result)
}

func qiniuAPIError(status int, body []byte, result qiniuAPIResult) error {
	if result.Error != "" {
		return i18n.E("service.deploy.qiniu_error", "code", result.Code, "message", result.Error)
	}
	return i18n.E("service.deploy.qiniu_status", "status", status, "body", pluginutil.Truncate(string(body), 300))
}

// qiniuAuthToken 生成七牛云管理凭证，签名结果使用 URL 安全 Base64。
func qiniuAuthToken(accessKey, secretKey, signingData string) string {
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(signingData))
	return accessKey + ":" + base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

// qiniuQBoxToken 按 QBox 规范签名「路径 + query」。
func qiniuQBoxToken(accessKey, secretKey, pathAndQuery string) string {
	return qiniuAuthToken(accessKey, secretKey, pathAndQuery+"\n")
}

// qiniuRequestToken 按 Qiniu 规范对请求方法、路径、Host、Content-Type、日期和请求体签名。
func qiniuRequestToken(accessKey, secretKey, method, pathAndQuery, host, contentType, date, body string) string {
	var data strings.Builder
	data.WriteString(method)
	data.WriteString(" ")
	data.WriteString(pathAndQuery)
	data.WriteString("\nHost: ")
	data.WriteString(host)
	if contentType != "" {
		data.WriteString("\nContent-Type: ")
		data.WriteString(contentType)
	}
	if date != "" {
		data.WriteString("\nX-Qiniu-Date: ")
		data.WriteString(date)
	}
	data.WriteString("\n\n")
	data.WriteString(body)
	return qiniuAuthToken(accessKey, secretKey, data.String())
}

func deployQiniuCDN(cert *model.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key"])
	secretKey := strings.TrimSpace(cfg["secret_key"])
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.qiniu_key_required")
	}
	domains := pluginutil.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = cert.Domains
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}

	certName := strings.TrimSpace(cfg["cert_name"])
	if certName == "" {
		certName = cert.PrimaryDomain + "-" + time.Now().Format("20060102150405")
	}
	commonName := cert.PrimaryDomain
	if commonName == "" {
		commonName = domains[0]
	}

	certID, err := qiniuUploadCert(accessKey, secretKey, certName, commonName, cert)
	if err != nil {
		return err
	}

	var errs []string
	for _, domain := range domains {
		if err := qiniuBindDomain(accessKey, secretKey, domain, certID); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

// deployQiniuKodo 将证书上传到七牛云证书服务，并绑定到 Kodo 自定义源站域名。
// 与 CDN 域名不同，Kodo 源站域名使用 /cert/bind 接口，不走 httpsconf/sslize。
func deployQiniuKodo(cert *model.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key"])
	secretKey := strings.TrimSpace(cfg["secret_key"])
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.qiniu_key_required")
	}
	// 兼容 domain 单域名写法，并允许留空时使用证书包含的全部域名。
	domains := pluginutil.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = pluginutil.SplitList(cfg["domain"])
	}
	if len(domains) == 0 {
		domains = cert.Domains
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.qiniu_kodo_domains_empty")
	}

	certName := strings.TrimSpace(cfg["cert_name"])
	if certName == "" {
		certName = cert.PrimaryDomain + "-" + time.Now().Format("20060102150405")
	}
	commonName := cert.PrimaryDomain
	if commonName == "" {
		commonName = domains[0]
	}

	certID, err := qiniuUploadCert(accessKey, secretKey, certName, commonName, cert)
	if err != nil {
		return err
	}

	var errs []string
	for _, domain := range domains {
		if err := qiniuBindKodoCert(accessKey, secretKey, domain, certID); err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func qiniuBindKodoCert(accessKey, secretKey, domain, certID string) error {
	payload, err := json.Marshal(map[string]string{"certid": certID, "domain": domain})
	if err != nil {
		return i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	apiPath := "/cert/bind"
	endpoint := strings.TrimRight(qiniuKodoBase(), "/") + apiPath
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QBox "+qiniuQBoxToken(accessKey, secretKey, apiPath))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.qiniu_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result := qiniuAPIResult{}
	_ = json.Unmarshal(data, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Error != "" || (result.Code != 0 && result.Code != 200) {
		if strings.Contains(strings.ToLower(result.Error), "domaintype") {
			return i18n.E("service.deploy.qiniu_kodo_cdn_domain", "domain", domain)
		}
		return qiniuAPIError(resp.StatusCode, data, result)
	}
	return nil
}

func qiniuUploadCert(accessKey, secretKey, name, commonName string, cert *model.Certificate) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"name":        name,
		"common_name": commonName,
		"ca":          cert.FullchainPEM,
		"pri":         cert.KeyPEM,
	})
	if err != nil {
		return "", i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	apiPath := "/sslcert"
	endpoint := strings.TrimRight(qiniuCertBaseURL, "/") + apiPath
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QBox "+qiniuQBoxToken(accessKey, secretKey, apiPath))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", i18n.E("service.deploy.qiniu_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var result qiniuAPIResult
	_ = json.Unmarshal(data, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (result.Code != 0 && result.Code != 200) {
		return "", qiniuAPIError(resp.StatusCode, data, result)
	}
	if result.CertID == "" {
		return "", i18n.E("service.deploy.qiniu_cert_id_missing")
	}
	return result.CertID, nil
}

func qiniuBindDomain(accessKey, secretKey, domain, certID string) error {
	basePath := "/domain/" + url.PathEscape(domain)
	// 只传证书 ID，保留域名原有的强制 HTTPS 等配置。
	payload, err := json.Marshal(map[string]any{"certId": certID})
	if err != nil {
		return i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}

	// httpsconf 用于更新已开启 HTTPS 的域名；首次部署时接口会失败，改用 sslize 开启。
	first, err := qiniuDomainRequest(http.MethodPut, basePath+"/httpsconf", accessKey, secretKey, payload)
	if err != nil {
		return err
	}
	if first.ok() {
		return nil
	}

	payload, err = json.Marshal(map[string]any{"certId": certID, "forceHttps": false, "http2Enable": true})
	if err != nil {
		return i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	second, err := qiniuDomainRequest(http.MethodPut, basePath+"/sslize", accessKey, secretKey, payload)
	if err != nil {
		return err
	}
	if second.ok() {
		return nil
	}
	if strings.Contains(strings.ToLower(string(second.Body)+" "+second.Result.Error), "kodo") {
		return i18n.E("service.deploy.qiniu_cdn_kodo_domain", "domain", domain)
	}
	return second.err()
}

func qiniuDomainRequest(method, apiPath, accessKey, secretKey string, payload []byte) (*qiniuAPIResponse, error) {
	endpoint := strings.TrimRight(qiniuDomainBaseURL, "/") + apiPath
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	date := time.Now().UTC().Format("20060102T150405Z")
	const contentType = "application/json"
	token := qiniuRequestToken(accessKey, secretKey, method, apiPath, parsed.Host, contentType, date, string(payload))

	req, err := http.NewRequest(method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, i18n.E("service.deploy.qiniu_request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Qiniu-Date", date)
	req.Header.Set("Authorization", "Qiniu "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.qiniu_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result := qiniuAPIResult{}
	_ = json.Unmarshal(data, &result)
	return &qiniuAPIResponse{Status: resp.StatusCode, Body: data, Result: result}, nil
}
