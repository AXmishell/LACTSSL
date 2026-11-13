package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// Deploy 将证书部署到指定目标。deployConfig 由任务流程部署节点提供，
// 与凭据中的认证信息合并后交给具体部署实现。保留可变参数以兼容旧的调用方式。
func (m *Manager) Deploy(cert *model.Certificate, account *model.DeployAccount, deployConfigs ...map[string]string) error {
	if strings.TrimSpace(cert.KeyPEM) == "" {
		return i18n.E("service.deploy.private_key_missing")
	}
	var deployConfig map[string]string
	if len(deployConfigs) > 0 {
		deployConfig = deployConfigs[0]
	}
	def, found := DeployTypeInfo(account.Type)
	if !found {
		return i18n.E("service.deploy.type_unsupported", "type", account.Type)
	}
	cfg := MergeDeployConfig(def, account.Config, deployConfig)
	switch account.Type {
	case "local":
		return deployLocal(cert, cfg)
	case "ssh":
		return m.deploySSH(cert, cfg, account)
	case "ftp":
		return deployFTP(cert, cfg)
	case "webhook":
		return deployWebhook(cert, cfg)
	case "aliyun_cdn":
		return deployAliyunCDN(cert, cfg)
	case "qiniu_cdn":
		return deployQiniuCDN(cert, cfg)
	case "qiniu_kodo":
		return deployQiniuKodo(cert, cfg)
	case "baota_site":
		return deployBaotaSite(cert, cfg)
	case "baota_panel":
		return deployBaotaPanel(cert, cfg)
	case "1panel_site":
		return deployOnePanelSite(cert, cfg)
	case "1panel_panel":
		return deployOnePanelPanel(cert, cfg)
	case "safeline":
		return deploySafeline(cert, cfg)
	default:
		return i18n.E("service.deploy.type_unsupported", "type", account.Type)
	}
}

// writeFileAtomic 先写临时文件再重命名，避免服务读到半个文件。
func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return i18n.E("service.deploy.mkdir_failed", "dir", dir, "error", err.Error())
	}
	tmp, err := os.CreateTemp(dir, ".lactssl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	return nil
}

func runCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", command)
	// 子进程不继承完整环境，避免并发的 DNS 申请把 lego 注入的 API 密钥
	// （如 CLOUDFLARE_DNS_API_TOKEN）泄露给部署命令。
	cmd.Env = commandEnv()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return i18n.E("service.deploy.command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
		}
		return nil
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		return i18n.E("service.deploy.command_timeout")
	}
}

// commandEnv 返回部署/重载命令允许继承的环境变量。
// 只保留运行命令必需的基础变量与常见代理/工具配置，避免把应用密钥传给子进程。
func commandEnv() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
		"LANG": true, "TZ": true, "TMPDIR": true, "TMP": true, "TEMP": true,
		"TERM": true, "PWD": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true,
		"KUBECONFIG": true, "DOCKER_HOST": true, "XDG_RUNTIME_DIR": true,
	}
	var env []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			env = append(env, item)
		}
	}
	return env
}

func deployLocal(cert *model.Certificate, cfg map[string]string) error {
	certPath := strings.TrimSpace(cfg["cert_path"])
	keyPath := strings.TrimSpace(cfg["key_path"])
	if certPath == "" || keyPath == "" {
		return i18n.E("service.deploy.paths_required")
	}
	if err := writeFileAtomic(certPath, []byte(cert.FullchainPEM), 0o644); err != nil {
		return i18n.E("service.deploy.write_cert_failed", "error", err.Error())
	}
	if err := writeFileAtomic(keyPath, []byte(cert.KeyPEM), 0o600); err != nil {
		return i18n.E("service.deploy.write_key_failed", "error", err.Error())
	}
	if err := runCommand(cfg["reload_command"]); err != nil {
		return i18n.E("service.deploy.reload_failed", "error", err.Error())
	}
	return nil
}

func (m *Manager) deploySSH(cert *model.Certificate, cfg map[string]string, account *model.DeployAccount) error {
	host := strings.TrimSpace(cfg["host"])
	user := strings.TrimSpace(cfg["user"])
	if host == "" || user == "" {
		return i18n.E("service.deploy.ssh_host_user_required")
	}
	port := strings.TrimSpace(cfg["port"])
	if port == "" {
		port = "22"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return i18n.E("service.deploy.ssh_port_invalid")
	}

	authMethods := []ssh.AuthMethod{}
	if cfg["auth_type"] == "key" && strings.TrimSpace(cfg["private_key"]) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg["private_key"]))
		if err != nil {
			return i18n.E("service.deploy.ssh_key_parse_failed", "error", err.Error())
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		if cfg["password"] == "" {
			return i18n.E("service.deploy.ssh_password_required")
		}
		authMethods = append(authMethods, ssh.Password(cfg["password"]))
	}

	pinned := strings.TrimSpace(cfg["host_key_fingerprint"])
	sshConfig := &ssh.ClientConfig{
		User:    user,
		Auth:    authMethods,
		Timeout: 15 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if pinned != "" {
				if actual != pinned {
					return i18n.E("service.deploy.ssh_host_key_mismatch", "expected", pinned, "actual", actual)
				}
				return nil
			}
			// 首次连接采用 TOFU：记录指纹，后续连接必须一致。
			if account != nil && account.ID > 0 && m != nil && m.Store != nil {
				if account.Config == nil {
					account.Config = map[string]string{}
				}
				account.Config["host_key_fingerprint"] = actual
				if err := m.Store.UpdateDeployAccount(account); err != nil {
					return i18n.E("service.deploy.ssh_host_key_save_failed", "error", err.Error())
				}
			}
			return nil
		},
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, port), sshConfig)
	if err != nil {
		return i18n.E("service.deploy.ssh_connect_failed", "error", err.Error())
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return i18n.E("service.deploy.sftp_session_failed", "error", err.Error())
	}
	defer sftpClient.Close()

	upload := func(remote string, data []byte, mode os.FileMode) error {
		remote = strings.TrimSpace(remote)
		if remote == "" {
			return i18n.E("service.deploy.remote_path_required")
		}
		if err := sftpClient.MkdirAll(path.Dir(remote)); err != nil {
			return i18n.E("service.deploy.remote_mkdir_failed", "error", err.Error())
		}
		tmp := remote + ".lactssl.tmp"
		f, err := sftpClient.Create(tmp)
		if err != nil {
			return i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := sftpClient.Chmod(tmp, mode); err != nil {
			// 部分 SFTP 服务不支持 chmod，忽略即可。
			_ = err
		}
		_ = sftpClient.Remove(remote)
		if err := sftpClient.Rename(tmp, remote); err != nil {
			return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
		}
		return nil
	}

	if err := upload(cfg["cert_path"], []byte(cert.FullchainPEM), 0o644); err != nil {
		return err
	}
	if err := upload(cfg["key_path"], []byte(cert.KeyPEM), 0o600); err != nil {
		return err
	}

	if command := strings.TrimSpace(cfg["reload_command"]); command != "" {
		session, err := client.NewSession()
		if err != nil {
			return i18n.E("service.deploy.ssh_session_failed", "error", err.Error())
		}
		defer session.Close()
		var output bytes.Buffer
		session.Stdout = &output
		session.Stderr = &output
		if err := session.Run(command); err != nil {
			return i18n.E("service.deploy.remote_command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
		}
	}
	return nil
}

func deployWebhook(cert *model.Certificate, cfg map[string]string) error {
	target := strings.TrimSpace(cfg["url"])
	if target == "" {
		return i18n.E("service.deploy.webhook_url_required")
	}
	if err := ValidateWebhookURL(target); err != nil {
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
	client := newOutboundClient(30*time.Second, cfg["skip_verify"] == "true")
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.deploy.callback_failed", "error", SafeError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.E("service.deploy.callback_status", "status", resp.StatusCode, "body", truncate(string(data), 400))
	}
	return nil
}

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

func deployAliyunCDN(cert *model.Certificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := strings.TrimSpace(cfg["access_key_secret"])
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.aliyun_key_required")
	}
	domains := splitConfigList(cfg["domains"])
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

func setAliyunCDNCert(accessKey, secretKey, domain, certName string, cert *model.Certificate) error {
	if certName == "" {
		certName = cert.PrimaryDomain + "-" + time.Now().Format("20060102150405")
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
	client := &http.Client{Timeout: 30 * time.Second}
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
		return i18n.E("service.deploy.aliyun_status", "status", resp.StatusCode, "body", truncate(string(data), 300))
	}
	var result struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(data, &result); err == nil && result.Code != "" {
		return i18n.E("service.deploy.aliyun_error", "code", result.Code, "message", result.Message)
	}
	return nil
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
	return i18n.E("service.deploy.qiniu_status", "status", status, "body", truncate(string(body), 300))
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
	domains := splitConfigList(cfg["domains"])
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
	domains := splitConfigList(cfg["domains"])
	if len(domains) == 0 {
		domains = splitConfigList(cfg["domain"])
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

func splitConfigList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
