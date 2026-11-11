package service

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

// ServerChanConfig 是 Server酱推送配置。
type ServerChanConfig struct {
	SendKey string
	Version string // auto / turbo / sc3
}

// LoadServerChanConfig 从设置中读取 Server酱配置。
func (m *Manager) LoadServerChanConfig() ServerChanConfig {
	sendKey, _ := m.Store.GetSecret("serverchan.sendkey")
	version, _ := m.Store.GetSetting("serverchan.version")
	if version == "" {
		version = "auto"
	}
	return ServerChanConfig{SendKey: strings.TrimSpace(sendKey), Version: version}
}

var serverChanSC3KeyPattern = regexp.MustCompile(`^sctp(\d+)t`)

// serverChanEndpoint 根据版本与 SendKey 生成接口地址。
func serverChanEndpoint(sendKey, version string) (string, error) {
	switch version {
	case "turbo":
		return "https://sctapi.ftqq.com/" + sendKey + ".send", nil
	case "sc3":
		matches := serverChanSC3KeyPattern.FindStringSubmatch(strings.ToLower(sendKey))
		if len(matches) < 2 {
			return "", i18n.E("service.notify.serverchan_invalid_key")
		}
		return fmt.Sprintf("https://%s.push.ft07.com/send/%s.send", matches[1], sendKey), nil
	default:
		// 自动识别：SCT 开头的 SendKey 走 Turbo，sctp 开头走 Server酱³。
		if matches := serverChanSC3KeyPattern.FindStringSubmatch(strings.ToLower(sendKey)); len(matches) >= 2 {
			return fmt.Sprintf("https://%s.push.ft07.com/send/%s.send", matches[1], sendKey), nil
		}
		return "https://sctapi.ftqq.com/" + sendKey + ".send", nil
	}
}

// SendServerChan 通过 Server酱接口推送一条消息。
func SendServerChan(cfg ServerChanConfig, title, message string) error {
	if cfg.SendKey == "" {
		return i18n.E("service.notify.serverchan_not_configured")
	}
	endpoint, err := serverChanEndpoint(cfg.SendKey, cfg.Version)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("title", title)
	form.Set("desp", message)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return i18n.E("service.notify.serverchan_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = resp.Status
		}
		return i18n.E("service.notify.serverchan_failed", "error", text)
	}
	var result struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err == nil && result.Code != nil && *result.Code != 0 {
		text := strings.TrimSpace(result.Message)
		if text == "" {
			text = strconv.Itoa(*result.Code)
		}
		return i18n.E("service.notify.serverchan_failed", "error", text)
	}
	return nil
}

// GotifyConfig 是 Gotify 推送配置。
type GotifyConfig struct {
	URL      string
	Token    string
	Priority int
}

// LoadGotifyConfig 从设置中读取 Gotify 配置。
func (m *Manager) LoadGotifyConfig() GotifyConfig {
	baseURL, _ := m.Store.GetSetting("gotify.url")
	token, _ := m.Store.GetSecret("gotify.token")
	priorityStr, _ := m.Store.GetSetting("gotify.priority")
	priority, _ := strconv.Atoi(priorityStr)
	if priority < 0 || priority > 10 {
		priority = 5
	}
	return GotifyConfig{
		URL:      strings.TrimSpace(baseURL),
		Token:    strings.TrimSpace(token),
		Priority: priority,
	}
}

// SendGotify 通过 Gotify HTTP API 推送一条消息。
func SendGotify(cfg GotifyConfig, title, message string) error {
	if cfg.URL == "" || cfg.Token == "" {
		return i18n.E("service.notify.gotify_not_configured")
	}
	base := strings.TrimRight(cfg.URL, "/")
	endpoint := base + "/message?token=" + url.QueryEscape(cfg.Token)
	payload, err := json.Marshal(map[string]any{
		"title":    title,
		"message":  message,
		"priority": cfg.Priority,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return i18n.E("service.notify.gotify_request_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("service.notify.gotify_request_failed", "error", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = resp.Status
		}
		return i18n.E("service.notify.gotify_failed", "error", text)
	}
	return nil
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// htmlToText 把邮件 HTML 粗略转换为适合 Gotify 展示的纯文本。
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
			conn.Close()
			return i18n.E("service.notify.tls_failed", "error", err.Error())
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return i18n.E("service.notify.connect_failed", "error", err.Error())
	}
	defer client.Close()

	if !cfg.SSL {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return i18n.E("service.notify.starttls_failed", "error", err.Error())
			}
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
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
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
		if name := strings.TrimSpace(channel); name != "" {
			selected[name] = true
		}
	}
	all := len(selected) == 0
	if all || selected["mail"] {
		if enabled, _ := m.Store.GetSetting("notify.enabled"); enabled == "true" {
			m.sendMailNotification(subject, body)
		}
	}
	if all || selected["gotify"] {
		if enabled, _ := m.Store.GetSetting("gotify.enabled"); enabled == "true" {
			m.sendGotifyNotification(subject, body)
		}
	}
	if all || selected["serverchan"] {
		if enabled, _ := m.Store.GetSetting("serverchan.enabled"); enabled == "true" {
			m.sendServerChanNotification(subject, body)
		}
	}
	for name, cfg := range m.LoadNotifyChannels() {
		if !all && !selected[name] {
			continue
		}
		if !cfgBool(cfg, "enabled") {
			continue
		}
		if err := SendNotifyChannel(name, cfg, subject, htmlToText(body)); err != nil {
			m.Store.AddLogKey("notify", 0, "error", "log.notify.channel_failed", map[string]any{"channel": name, "error": err.Error()})
			continue
		}
		m.Store.AddLogKey("notify", 0, "info", "log.notify.channel_sent", map[string]any{"channel": name, "subject": subject})
	}
}

func (m *Manager) sendMailNotification(subject, body string) {
	to, _ := m.Store.GetSetting("notify.to")
	if to == "" {
		if user, err := m.Store.GetUser(); err == nil {
			to = user.Email
		}
	}
	if to == "" {
		return
	}
	cfg := m.LoadSMTPConfig()
	if cfg.Host == "" {
		return
	}
	if err := SendMail(cfg, to, subject, body); err != nil {
		m.Store.AddLogKey("notify", 0, "error", "log.notify.failed", map[string]any{"error": err.Error()})
		return
	}
	m.Store.AddLogKey("notify", 0, "info", "log.notify.sent", map[string]any{"subject": subject})
}

func (m *Manager) sendGotifyNotification(subject, body string) {
	cfg := m.LoadGotifyConfig()
	if cfg.URL == "" || cfg.Token == "" {
		return
	}
	if err := SendGotify(cfg, subject, htmlToText(body)); err != nil {
		m.Store.AddLogKey("notify", 0, "error", "log.notify.gotify_failed", map[string]any{"error": err.Error()})
		return
	}
	m.Store.AddLogKey("notify", 0, "info", "log.notify.gotify_sent", map[string]any{"subject": subject})
}

func (m *Manager) sendServerChanNotification(subject, body string) {
	cfg := m.LoadServerChanConfig()
	if cfg.SendKey == "" {
		return
	}
	if err := SendServerChan(cfg, subject, htmlToText(body)); err != nil {
		m.Store.AddLogKey("notify", 0, "error", "log.notify.serverchan_failed", map[string]any{"error": err.Error()})
		return
	}
	m.Store.AddLogKey("notify", 0, "info", "log.notify.serverchan_sent", map[string]any{"subject": subject})
}
