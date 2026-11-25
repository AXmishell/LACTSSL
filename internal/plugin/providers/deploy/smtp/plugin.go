// Package smtp 通过 SMTP 把证书打包为 ZIP 附件发送到指定邮箱。
//
// 对应 certd 的「邮件发送证书」部署插件。
package smtp

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "smtp" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deploySMTP(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

// Verify 只读连接 SMTP 并完成认证，不发送任何邮件。
func (plugin) Verify(_ context.Context, req deploy.NativeRequest) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	host := strings.TrimSpace(cfg["host"])
	if host == "" {
		return util.VerifyFailed(loc, i18n.E("service.deploy.smtp_host_from_required")), nil
	}
	port := util.Int(cfg["port"], 465)
	if port < 1 || port > 65535 {
		return util.VerifyFailed(loc, i18n.E("service.deploy.smtp_port_invalid")), nil
	}
	tlsMode := strings.ToLower(strings.TrimSpace(cfg["tls"]))
	if tlsMode == "" {
		tlsMode = "tls"
	}
	switch tlsMode {
	case "none", "starttls", "tls":
	default:
		return util.VerifyFailed(loc, i18n.E("service.deploy.smtp_tls_invalid", "mode", tlsMode)), nil
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if tlsMode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: util.Bool(cfg["skip_verify"])})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return util.VerifyFailed(loc, i18n.E("service.notify.connect_failed", "error", err.Error())), nil
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return util.VerifyFailed(loc, i18n.E("service.notify.connect_failed", "error", err.Error())), nil
	}
	defer client.Close()
	if tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return util.VerifyFailed(loc, i18n.E("service.notify.starttls_required")), nil
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: util.Bool(cfg["skip_verify"])}); err != nil {
			return util.VerifyFailed(loc, i18n.E("service.notify.starttls_failed", "error", err.Error())), nil
		}
	}
	if username := strings.TrimSpace(cfg["user"]); username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, cfg["password"], host)); err != nil {
			return util.VerifyFailed(loc, i18n.E("service.notify.auth_failed", "error", err.Error())), nil
		}
	}
	_ = client.Quit()
	return []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
		{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)},
	}, nil
}

func init() {
	deploy.MarkNativeVerifiable("smtp")
	deploy.RegisterNative(plugin{})
}

func deploySMTP(cert *deploy.NativeCertificate, cfg map[string]string) error {
	host := strings.TrimSpace(cfg["host"])
	from := strings.TrimSpace(cfg["from"])
	if host == "" || from == "" {
		return i18n.E("service.deploy.smtp_host_from_required")
	}
	recipients := util.SplitList(cfg["to"])
	if len(recipients) == 0 {
		return i18n.E("service.notify.recipient_empty")
	}
	port := util.Int(cfg["port"], 465)
	if port < 1 || port > 65535 {
		return i18n.E("service.deploy.smtp_port_invalid")
	}
	tlsMode := strings.ToLower(strings.TrimSpace(cfg["tls"]))
	if tlsMode == "" {
		tlsMode = "tls"
	}
	switch tlsMode {
	case "none", "starttls", "tls":
	default:
		return i18n.E("service.deploy.smtp_tls_invalid", "mode", tlsMode)
	}

	subject := strings.TrimSpace(cfg["subject"])
	if subject == "" {
		subject = "证书：" + util.FirstDomain(cert)
	}
	archive, filename, err := buildArchive(cert)
	if err != nil {
		return i18n.E("service.deploy.smtp_archive_failed", "error", err.Error())
	}
	message, err := buildMessage(from, recipients, subject, cert, filename, archive)
	if err != nil {
		return i18n.E("service.deploy.smtp_message_failed", "error", err.Error())
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if tlsMode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: util.Bool(cfg["skip_verify"]),
		})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return i18n.E("service.notify.connect_failed", "error", err.Error())
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return i18n.E("service.notify.connect_failed", "error", err.Error())
	}
	defer client.Close()

	if tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return i18n.E("service.notify.starttls_required")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: util.Bool(cfg["skip_verify"]),
		}); err != nil {
			return i18n.E("service.notify.starttls_failed", "error", err.Error())
		}
	}
	username := strings.TrimSpace(cfg["user"])
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, cfg["password"], host)); err != nil {
			return i18n.E("service.notify.auth_failed", "error", err.Error())
		}
	}
	if err := client.Mail(extractAddress(from)); err != nil {
		return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(extractAddress(recipient)); err != nil {
			return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
		}
	}
	writer, err := client.Data()
	if err != nil {
		return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
	}
	if _, err := writer.Write(message); err != nil {
		return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
	}
	if err := writer.Close(); err != nil {
		return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
	}
	if err := client.Quit(); err != nil {
		return i18n.E("service.deploy.smtp_send_failed", "error", err.Error())
	}
	return nil
}

func buildArchive(cert *deploy.NativeCertificate) ([]byte, string, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	files := []struct {
		name string
		data string
	}{
		{"cert.pem", cert.CertPEM},
		{"fullchain.pem", cert.FullchainPEM},
		{"privkey.pem", cert.KeyPEM},
		{"chain.pem", util.ChainPEM(cert.FullchainPEM)},
	}
	for _, file := range files {
		if strings.TrimSpace(file.data) == "" {
			continue
		}
		entry, err := archive.Create(file.name)
		if err != nil {
			return nil, "", err
		}
		if _, err := entry.Write([]byte(file.data)); err != nil {
			return nil, "", err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, "", err
	}
	domain := util.FirstDomain(cert)
	if domain == "" {
		domain = "certificate"
	}
	// 附件名会进入 MIME 头；去掉引号/控制字符，避免头注入。
	domain = strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(domain)
	return buf.Bytes(), "cert-" + domain + ".zip", nil
}

func buildMessage(from string, recipients []string, subject string, cert *deploy.NativeCertificate, filename string, archive []byte) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textHeader.Set("Content-Transfer-Encoding", "base64")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("证书已部署完成。\n\n主域名：%s\n全部域名：%s\n有效期至：%s\n",
		util.FirstDomain(cert), strings.Join(util.Domains(cert), ", "), cert.NotAfter)
	if _, err := textPart.Write([]byte(base64.StdEncoding.EncodeToString([]byte(text)))); err != nil {
		return nil, err
	}

	attachmentHeader := textproto.MIMEHeader{}
	attachmentHeader.Set("Content-Type", "application/zip")
	attachmentHeader.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	attachmentHeader.Set("Content-Transfer-Encoding", "base64")
	attachmentPart, err := writer.CreatePart(attachmentHeader)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(archive)))
	base64.StdEncoding.Encode(encoded, archive)
	// 每 76 字符换行，兼容严格的邮件网关。
	for len(encoded) > 76 {
		if _, err := attachmentPart.Write(encoded[:76]); err != nil {
			return nil, err
		}
		if _, err := attachmentPart.Write([]byte("\r\n")); err != nil {
			return nil, err
		}
		encoded = encoded[76:]
	}
	if len(encoded) > 0 {
		if _, err := attachmentPart.Write(encoded); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", sanitizeHeader(from))
	fmt.Fprintf(&message, "To: %s\r\n", sanitizeHeader(strings.Join(recipients, ", ")))
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", sanitizeHeader(subject)))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func extractAddress(value string) string {
	value = sanitizeHeader(value)
	if start := strings.LastIndex(value, "<"); start >= 0 {
		if end := strings.Index(value[start:], ">"); end > 0 {
			return strings.TrimSpace(value[start+1 : start+end])
		}
	}
	return strings.TrimSpace(value)
}
