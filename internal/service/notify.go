package service

import (
	"crypto/tls"
	"html"
	"mime"
	"net"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/plugin/notify"
)

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// htmlToText 把邮件 HTML 粗略转换为适合即时通知展示的纯文本。
func htmlToText(value string) string {
	replacer := strings.NewReplacer(
		"</p>", "\n",
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</h2>", "\n",
	)
	value = replacer.Replace(value)
	value = htmlTagPattern.ReplaceAllString(value, "")
	value = html.UnescapeString(value)
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = strings.TrimSpace(lines[index])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// SMTPConfig 是邮件发送配置。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	SSL      bool
}

// LoadSMTPConfig 从设置中读取邮件配置。
func (m *Manager) LoadSMTPConfig() SMTPConfig {
	host, _ := m.Store.GetSetting("smtp.host")
	portStr, _ := m.Store.GetSetting("smtp.port")
	user, _ := m.Store.GetSetting("smtp.username")
	pass, _ := m.Store.GetSecret("smtp.password")
	from, _ := m.Store.GetSetting("smtp.from")
	sslStr, _ := m.Store.GetSetting("smtp.ssl")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 465
	}
	if from == "" {
		from = user
	}
	return SMTPConfig{
		Host:     strings.TrimSpace(host),
		Port:     port,
		Username: strings.TrimSpace(user),
		Password: pass,
		From:     strings.TrimSpace(from),
		SSL:      sslStr == "true",
	}
}

// sanitizeMailHeader 移除邮件头中的换行，避免通过任务名、备注等字段注入额外头。
func sanitizeMailHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

// SendMail 使用给定配置发送 HTML 邮件。
func SendMail(cfg SMTPConfig, to, subject, html string) error {
	if cfg.Host == "" || cfg.From == "" {
		return i18n.E("service.notify.smtp_not_configured")
	}
	if to == "" {
		return i18n.E("service.notify.recipient_empty")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	fromAddr := extractAddress(sanitizeMailHeader(cfg.From))
	header := strings.Join([]string{
		"From: " + sanitizeMailHeader(cfg.From),
		"To: " + sanitizeMailHeader(to),
		"Subject: " + mime.BEncoding.Encode("UTF-8", sanitizeMailHeader(subject)),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"Date: " + time.Now().Format(time.RFC1123Z),
	}, "\r\n")
	message := []byte(header + "\r\n\r\n" + html + "\r\n")

	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return i18n.E("service.notify.connect_failed", "error", err.Error())
	}
	// 设置整体读写超时，避免 SMTP 服务异常时阻塞调度器。
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	if cfg.SSL {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return i18n.E("service.notify.tls_failed", "error", err.Error())
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return i18n.E("service.notify.connect_failed", "error", err.Error())
	}
	defer client.Close()

	if !cfg.SSL {
		// 未启用隐式 TLS 时必须完成 STARTTLS，避免邮件正文和重置令牌明文传输。
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return i18n.E("service.notify.starttls_required")
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return i18n.E("service.notify.starttls_failed", "error", err.Error())
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return i18n.E("service.notify.auth_failed", "error", err.Error())
		}
	}
	if err := client.Mail(fromAddr); err != nil {
		return err
	}
	if err := client.Rcpt(sanitizeMailHeader(to)); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	// writer.Close 会提交正文并等待服务器返回接收结果，成功后邮件已经
	// 被服务器接受。QUIT 只是礼貌关闭连接，失败（例如服务端读完正文后
	// 直接断开）不能让上层把已投递的邮件当成失败并重试，否则会重复发送。
	if err := writer.Close(); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}

func extractAddress(value string) string {
	if start := strings.LastIndex(value, "<"); start >= 0 {
		if end := strings.Index(value[start:], ">"); end > 0 {
			return strings.TrimSpace(value[start+1 : start+end])
		}
	}
	return strings.TrimSpace(value)
}

// sendNotification 向所有已启用的渠道发送通知，失败时只记录日志。
func (m *Manager) sendNotification(subject, body string) {
	m.sendNotificationTo(subject, body, nil)
}

// sendNotificationTo 向指定渠道发送通知；channels 为空时发送到所有已启用渠道。
func (m *Manager) sendNotificationTo(subject, body string, channels []string) {
	selected := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if name := strings.ToLower(strings.TrimSpace(channel)); name != "" {
			selected[name] = true
		}
	}
	all := len(selected) == 0
	if all || selected["mail"] {
		if enabled, _ := m.Store.GetSetting("notify.enabled"); enabled == "true" {
			m.sendMailNotification(subject, body)
		}
	}

	stored := m.LoadNotifyChannels()
	for _, channel := range notifyplugin.ExternalEnabledChannels() {
		id := strings.ToLower(channel.ID)
		if !all && !selected[id] {
			continue
		}
		cfg := stored[id]
		if !NotifyChannelConfigEnabled(cfg) {
			continue
		}
		if err := SendNotifyChannel(id, cfg, subject, htmlToText(body)); err != nil {
			m.Store.AddLogKey("notify", 0, "error", "log.notify.channel_failed", map[string]any{"channel": id, "error": SafeError(err)})
			continue
		}
		m.Store.AddLogKey("notify", 0, "info", "log.notify.channel_sent", map[string]any{"channel": id, "subject": subject})
	}
}

func (m *Manager) sendMailNotification(subject, body string) {
	cfg := m.LoadSMTPConfig()
	if cfg.Host == "" {
		return
	}
	raw, _ := m.Store.GetSetting("notify.to")
	configured, err := ParseNotifyRecipients(raw)
	if err != nil {
		m.Store.AddLogKey("notify", 0, "error", "log.notify.failed", map[string]any{"error": SafeError(err)})
		return
	}
	accountEmail := ""
	if user, err := m.Store.GetUser(); err == nil && user != nil {
		accountEmail = user.Email
	}
	recipients := ResolveMailRecipients(accountEmail, configured)
	if len(recipients) == 0 {
		return
	}
	sent := 0
	for _, to := range recipients {
		if err := m.sendMail(cfg, to, subject, body); err != nil {
			m.Store.AddLogKey("notify", 0, "error", "log.notify.failed", map[string]any{
				"error": SafeError(err),
				"to":    to,
			})
			continue
		}
		sent++
	}
	if sent > 0 {
		m.Store.AddLogKey("notify", 0, "info", "log.notify.sent", map[string]any{"subject": subject})
	}
}
