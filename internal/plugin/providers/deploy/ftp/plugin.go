package ftp

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/jlaffaye/ftp"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

// plugin 通过 FTP / FTPS 上传证书和私钥。
type plugin struct{}

func (plugin) ID() string        { return "ftp" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployFTP(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

// Verify 只读连接并登录 FTP/FTPS，必要时列出目标目录。
func (plugin) Verify(_ context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	host := strings.TrimSpace(cfg["host"])
	user := strings.TrimSpace(cfg["user"])
	if host == "" || user == "" {
		return ftpVerifyFailed(loc, i18n.E("service.deploy.ftp_host_user_required")), nil
	}
	if cfg["password"] == "" {
		return ftpVerifyFailed(loc, i18n.E("service.deploy.ftp_password_required")), nil
	}
	tlsMode := strings.ToLower(strings.TrimSpace(cfg["tls"]))
	if tlsMode == "" {
		tlsMode = "explicit"
	}
	port := strings.TrimSpace(cfg["port"])
	if tlsMode == "implicit" && (port == "" || port == "21") {
		port = "990"
	} else if port == "" {
		port = "21"
	}
	options := []ftp.DialOption{ftp.DialWithTimeout(20 * time.Second)}
	switch tlsMode {
	case "none":
	case "explicit", "implicit":
		tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: util.Bool(cfg["skip_verify"])}
		if tlsMode == "implicit" {
			options = append(options, ftp.DialWithTLS(tlsConfig))
		} else {
			options = append(options, ftp.DialWithExplicitTLS(tlsConfig))
		}
	default:
		return ftpVerifyFailed(loc, i18n.E("service.deploy.ftp_tls_invalid", "mode", tlsMode)), nil
	}
	client, err := ftp.Dial(net.JoinHostPort(host, port), options...)
	if err != nil {
		return ftpVerifyFailed(loc, i18n.E("service.deploy.ftp_connect_failed", "error", err.Error())), nil
	}
	defer func() { _ = client.Quit() }()
	if err := client.Login(user, cfg["password"]); err != nil {
		return ftpVerifyFailed(loc, i18n.E("service.deploy.ftp_login_failed", "error", err.Error())), nil
	}
	checks := []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
	}
	dir := path.Dir(strings.TrimSpace(cfg["cert_path"]))
	if strings.TrimSpace(cfg["cert_path"]) == "" || dir == "." || dir == "/" {
		return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)}), nil
	}
	if _, err := client.List(dir); err != nil {
		checks = append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.target_missing", map[string]any{"target": dir})})
		return checks, nil
	}
	return append(checks, protocol.VerifyCheck{Key: "scope", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.target_ok", nil)}), nil
}

func ftpVerifyFailed(loc i18n.Locale, err error) []protocol.VerifyCheck {
	if err == nil {
		return nil
	}
	return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}
}

func init() {
	deploy.MarkNativeVerifiable("ftp")
	deploy.RegisterNative(plugin{})
}

// deployFTP 通过 FTP / FTPS 上传证书和私钥。
func deployFTP(cert *deploy.NativeCertificate, cfg map[string]string) error {
	host := strings.TrimSpace(cfg["host"])
	user := strings.TrimSpace(cfg["user"])
	if host == "" || user == "" {
		return i18n.E("service.deploy.ftp_host_user_required")
	}
	if cfg["password"] == "" {
		return i18n.E("service.deploy.ftp_password_required")
	}
	if strings.TrimSpace(cert.FullchainPEM) == "" || strings.TrimSpace(cert.KeyPEM) == "" {
		return i18n.E("service.deploy.certificate_empty")
	}
	certPath, err := cleanRemotePath(cfg["cert_path"])
	if err != nil {
		return err
	}
	keyPath, err := cleanRemotePath(cfg["key_path"])
	if err != nil {
		return err
	}

	tlsMode := strings.ToLower(strings.TrimSpace(cfg["tls"]))
	if tlsMode == "" {
		// 默认显式 TLS；明文 FTP 会把证书私钥和登录密码暴露在网络上。
		tlsMode = "explicit"
	}
	port := strings.TrimSpace(cfg["port"])
	if tlsMode == "implicit" && (port == "" || port == "21") {
		// catalog 的默认端口是 21；隐式 TLS 的惯用端口是 990。
		port = "990"
	} else if port == "" {
		port = "21"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return i18n.E("service.deploy.ftp_port_invalid")
	}

	options := []ftp.DialOption{ftp.DialWithTimeout(20 * time.Second)}
	switch tlsMode {
	case "none":
	case "explicit", "implicit":
		tlsConfig := &tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: util.Bool(cfg["skip_verify"]), // 自建 FTP 服务常使用自签证书
		}
		if tlsMode == "implicit" {
			options = append(options, ftp.DialWithTLS(tlsConfig))
		} else {
			options = append(options, ftp.DialWithExplicitTLS(tlsConfig))
		}
	default:
		return i18n.E("service.deploy.ftp_tls_invalid", "mode", tlsMode)
	}

	client, err := ftp.Dial(net.JoinHostPort(host, port), options...)
	if err != nil {
		return i18n.E("service.deploy.ftp_connect_failed", "error", err.Error())
	}
	defer func() { _ = client.Quit() }()

	if err := client.Login(user, cfg["password"]); err != nil {
		return i18n.E("service.deploy.ftp_login_failed", "error", err.Error())
	}

	files := []ftpUpload{
		{remote: certPath, data: []byte(cert.FullchainPEM)},
		{remote: keyPath, data: []byte(cert.KeyPEM)},
	}
	return uploadFTPFiles(client, files)
}

type ftpUpload struct {
	remote string
	data   []byte
}

// uploadFTPFiles 先把所有文件写入临时文件，全部成功后再统一替换。
// 替换前保留旧文件备份；任一步失败时尽量回滚，避免证书与私钥不一致
// 或旧证书在失败路径中被删除。
func uploadFTPFiles(client *ftp.ServerConn, files []ftpUpload) error {
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	type prepared struct {
		ftpUpload
		tmp      string
		backup   string
		backedUp bool
	}
	items := make([]prepared, 0, len(files))
	cleanupTemps := func() {
		for _, item := range items {
			_ = client.Delete(item.tmp)
		}
	}

	for _, file := range files {
		ftpMkdirAll(client, path.Dir(file.remote))
		item := prepared{
			ftpUpload: file,
			tmp:       file.remote + ".lactssl.tmp." + suffix,
			backup:    file.remote + ".lactssl.bak." + suffix,
		}
		if err := client.Stor(item.tmp, bytes.NewReader(file.data)); err != nil {
			_ = client.Delete(item.tmp)
			cleanupTemps()
			return i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		items = append(items, item)
	}

	replaced := make([]prepared, 0, len(items))
	restore := func(item prepared) {
		if item.backedUp {
			_ = client.Delete(item.remote)
			if err := client.Rename(item.backup, item.remote); err != nil {
				// 恢复失败时至少不要删掉备份，便于人工恢复。
				_ = client.Delete(item.remote)
			}
			return
		}
		// 没有旧文件时才允许删除本次写入的新文件。
		_ = client.Delete(item.remote)
	}
	rollback := func(current *prepared) {
		if current != nil {
			restore(*current)
		}
		for i := len(replaced) - 1; i >= 0; i-- {
			restore(replaced[i])
		}
		cleanupTemps()
	}

	for index := range items {
		item := &items[index]
		// 目标已存在时先备份；不存在时 Rename 会失败，忽略即可。
		if err := client.Rename(item.remote, item.backup); err == nil {
			item.backedUp = true
		} else if _, statErr := client.FileSize(item.remote); statErr == nil {
			// 目标存在但无法备份：拒绝覆盖，保留旧文件。
			rollback(nil)
			return i18n.E("service.deploy.remote_rename_failed", "error", "cannot back up existing file")
		}
		if err := client.Rename(item.tmp, item.remote); err != nil {
			rollback(item)
			return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
		}
		replaced = append(replaced, *item)
	}
	for _, item := range items {
		_ = client.Delete(item.backup)
	}
	return nil
}

// cleanRemotePath 清理远程路径并拒绝目录穿越。绝对路径仍允许，因为
// 许多用户的证书路径就是 /etc/nginx/ssl/...；但任何 .. 组件都会导致
// 在 FTP 账号权限范围内写到预期之外的位置。
func cleanRemotePath(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", i18n.E("service.deploy.remote_path_required")
	}
	if strings.ContainsAny(remote, "\x00\r\n") {
		return "", i18n.E("service.deploy.ftp_path_invalid")
	}
	for _, part := range strings.Split(strings.ReplaceAll(remote, "\\", "/"), "/") {
		if part == ".." {
			return "", i18n.E("service.deploy.ftp_path_invalid")
		}
	}
	cleaned := path.Clean(remote)
	if cleaned == "." || cleaned == "/" {
		return "", i18n.E("service.deploy.remote_path_required")
	}
	return cleaned, nil
}

// ftpMkdirAll 逐级创建远程目录。目录已存在时服务器会返回错误，可忽略，
// 真正的权限或路径问题会在后续写入文件时暴露。
func ftpMkdirAll(client *ftp.ServerConn, dir string) {
	dir = path.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." || dir == "/" {
		return
	}
	current := ""
	if strings.HasPrefix(dir, "/") {
		current = "/"
	}
	for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
		if part == "" || part == "." {
			continue
		}
		switch current {
		case "":
			current = part
		case "/":
			current += part
		default:
			current += "/" + part
		}
		_ = client.MakeDir(current)
	}
}
