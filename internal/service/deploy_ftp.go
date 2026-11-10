package service

import (
	"bytes"
	"crypto/tls"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// deployFTP 通过 FTP / FTPS 上传证书和私钥。
func deployFTP(cert *model.Certificate, cfg map[string]string) error {
	host := strings.TrimSpace(cfg["host"])
	user := strings.TrimSpace(cfg["user"])
	if host == "" || user == "" {
		return i18n.E("service.deploy.ftp_host_user_required")
	}
	if cfg["password"] == "" {
		return i18n.E("service.deploy.ftp_password_required")
	}

	tlsMode := strings.ToLower(strings.TrimSpace(cfg["tls"]))
	if tlsMode == "" {
		tlsMode = "none"
	}
	port := strings.TrimSpace(cfg["port"])
	if port == "" {
		if tlsMode == "implicit" {
			port = "990"
		} else {
			port = "21"
		}
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
			InsecureSkipVerify: cfg["skip_verify"] == "true", // 自建 FTP 服务常使用自签证书
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

	upload := func(remote string, data []byte) error {
		remote = strings.TrimSpace(remote)
		if remote == "" {
			return i18n.E("service.deploy.remote_path_required")
		}
		ftpMkdirAll(client, path.Dir(remote))
		tmp := remote + ".lactssl.tmp"
		if err := client.Stor(tmp, bytes.NewReader(data)); err != nil {
			return i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		// FTP 的 RNTO 在部分服务器上不允许覆盖已有文件，先删除旧文件。
		_ = client.Delete(remote)
		if err := client.Rename(tmp, remote); err != nil {
			_ = client.Delete(tmp)
			return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
		}
		return nil
	}

	if err := upload(cfg["cert_path"], []byte(cert.FullchainPEM)); err != nil {
		return err
	}
	if err := upload(cfg["key_path"], []byte(cert.KeyPEM)); err != nil {
		return err
	}
	return nil
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
