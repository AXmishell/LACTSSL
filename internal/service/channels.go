package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

// NotifyChannelConfig 是单个通知渠道的配置。
type NotifyChannelConfig map[string]any

// notifyChannelSecretFields 列出每个渠道需要加密保存的字段。
var notifyChannelSecretFields = map[string][]string{
	"ntfy":          {"token"},
	"gocqhttp":      {"access_token"},
	"pushdeer":      {"pushkey"},
	"bark":          {"key"},
	"telegram":      {"bot_token"},
	"dingtalk":      {"webhook", "secret"},
	"wecom_bot":     {"webhook"},
	"wecom_app":     {"corp_secret"},
	"wecom_smart":   {"token"},
	"igot":          {"key"},
	"pushplus":      {"token"},
	"weplus":        {"token"},
	"wxpusher":      {"app_token"},
	"wxpusher_spt":  {"spt"},
	"openilink":     {"token"},
	"wpush":         {"token"},
	"synology_chat": {"url"},
	"feishu":        {"webhook", "secret"},
	"pushme":        {"push_key"},
	"chronocat":     {"token"},
	"custom":        {"headers"},
}

// LoadNotifyChannels 读取所有扩展通知渠道配置。
func (m *Manager) LoadNotifyChannels() map[string]NotifyChannelConfig {
	result := make(map[string]NotifyChannelConfig)
	raw, _ := m.Store.GetSecret("notify.channels")
	if raw == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

// SaveNotifyChannels 保存所有扩展通知渠道配置。
func (m *Manager) SaveNotifyChannels(channels map[string]NotifyChannelConfig) error {
	data, err := json.Marshal(channels)
	if err != nil {
		return err
	}
	return m.Store.SetSecret("notify.channels", string(data))
}

// NotifyChannelsForUI 返回脱敏后的渠道配置，避免把密钥回传到前端。
func (m *Manager) NotifyChannelsForUI() map[string]NotifyChannelConfig {
	channels := m.LoadNotifyChannels()
	for name, cfg := range channels {
		for _, field := range notifyChannelSecretFields[name] {
			if _, exists := cfg[field]; exists {
				cfg[field] = ""
			}
		}
	}
	return channels
}

// IsNotifyChannel 判断渠道名称是否受支持。
func IsNotifyChannel(name string) bool {
	switch name {
	case "mail", "gotify", "serverchan":
		return true
	}
	_, ok := notifyChannelSecretFields[name]
	return ok
}

func isNotifySecretField(channel, field string) bool {
	for _, item := range notifyChannelSecretFields[channel] {
		if item == field {
			return true
		}
	}
	return false
}

// MergeNotifyChannelConfig 合并前端提交的配置，空密钥表示保留旧值。
func MergeNotifyChannelConfig(channel string, existing, incoming NotifyChannelConfig) NotifyChannelConfig {
	if existing == nil {
		existing = NotifyChannelConfig{}
	}
	for key, value := range incoming {
		if isNotifySecretField(channel, key) {
			text, ok := value.(string)
			if ok && strings.TrimSpace(text) == "" {
				continue
			}
		}
		existing[key] = value
	}
	return existing
}

func cfgString(cfg NotifyChannelConfig, key, fallback string) string {
	value, ok := cfg[key]
	if !ok || value == nil {
		return fallback
	}
	switch item := value.(type) {
	case string:
		if strings.TrimSpace(item) == "" {
			return fallback
		}
		return strings.TrimSpace(item)
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(item)
	default:
		return fmt.Sprint(item)
	}
}

func cfgInt(cfg NotifyChannelConfig, key string, fallback int) int {
	raw := cfgString(cfg, key, "")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func cfgBool(cfg NotifyChannelConfig, key string) bool {
	switch value := cfg[key].(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
}

func cfgList(cfg NotifyChannelConfig, key string) []string {
	switch value := cfg[key].(type) {
	case []any:
		list := make([]string, 0, len(value))
		for _, item := range value {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				list = append(list, text)
			}
		}
		return list
	case []string:
		return value
	case string:
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
		list := make([]string, 0, len(parts))
		for _, part := range parts {
			if text := strings.TrimSpace(part); text != "" {
				list = append(list, text)
			}
		}
		return list
	default:
		return nil
	}
}

func notifyHTTP(method, endpoint string, payload []byte, contentType string, headers map[string]string) error {
	req, err := http.NewRequest(method, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = resp.Status
		}
		return fmt.Errorf("%s", text)
	}
	return nil
}

func notifyJSON(endpoint string, payload any, headers map[string]string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return notifyHTTP(http.MethodPost, endpoint, data, "application/json", headers)
}

func notifyForm(endpoint string, values url.Values, headers map[string]string) error {
	return notifyHTTP(http.MethodPost, endpoint, []byte(values.Encode()), "application/x-www-form-urlencoded", headers)
}

func bearer(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

func missingChannelConfig(name string) error {
	return i18n.E("service.notify.channel_not_configured", "channel", name)
}

func channelMessage(title, body string) string {
	if strings.TrimSpace(title) == "" {
		return body
	}
	return title + "\n\n" + body
}

// SendNotifyChannel 按渠道类型发送一条通知。
func SendNotifyChannel(name string, cfg NotifyChannelConfig, title, body string) error {
	text := channelMessage(title, body)
	switch name {
	case "ntfy":
		base := cfgString(cfg, "url", "https://ntfy.sh")
		topic := cfgString(cfg, "topic", "")
		if topic == "" {
			return missingChannelConfig(name)
		}
		headers := map[string]string{
			"Title":    title,
			"Priority": strconv.Itoa(cfgInt(cfg, "priority", 3)),
		}
		for key, value := range bearer(cfgString(cfg, "token", "")) {
			headers[key] = value
		}
		return notifyHTTP(http.MethodPost, strings.TrimRight(base, "/")+"/"+url.PathEscape(topic), []byte(body), "text/plain", headers)

	case "gocqhttp":
		base := cfgString(cfg, "url", "http://127.0.0.1:5700")
		targetID := cfgString(cfg, "target_id", "")
		if targetID == "" {
			return missingChannelConfig(name)
		}
		targetType := cfgString(cfg, "target_type", "private")
		endpoint := strings.TrimRight(base, "/") + "/send_private_msg"
		key := "user_id"
		if targetType == "group" {
			endpoint = strings.TrimRight(base, "/") + "/send_group_msg"
			key = "group_id"
		}
		return notifyJSON(endpoint, map[string]any{key: targetID, "message": text}, bearer(cfgString(cfg, "access_token", "")))

	case "pushdeer":
		base := cfgString(cfg, "url", "https://api2.pushdeer.com")
		pushKey := cfgString(cfg, "pushkey", "")
		if pushKey == "" {
			return missingChannelConfig(name)
		}
		return notifyForm(strings.TrimRight(base, "/")+"/message/push", url.Values{
			"pushkey": {pushKey},
			"text":    {title},
			"desp":    {body},
		}, nil)

	case "bark":
		base := cfgString(cfg, "url", "https://api.day.app")
		key := cfgString(cfg, "key", "")
		if key == "" {
			return missingChannelConfig(name)
		}
		payload := map[string]any{"title": title, "body": body}
		if sound := cfgString(cfg, "sound", ""); sound != "" {
			payload["sound"] = sound
		}
		if group := cfgString(cfg, "group", ""); group != "" {
			payload["group"] = group
		}
		return notifyJSON(strings.TrimRight(base, "/")+"/"+url.PathEscape(key), payload, nil)

	case "telegram":
		token := cfgString(cfg, "bot_token", "")
		chatID := cfgString(cfg, "chat_id", "")
		if token == "" || chatID == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON("https://api.telegram.org/bot"+token+"/sendMessage", map[string]any{
			"chat_id":                  chatID,
			"text":                     text,
			"disable_web_page_preview": true,
		}, nil)

	case "dingtalk":
		webhook := cfgString(cfg, "webhook", "")
		if webhook == "" {
			return missingChannelConfig(name)
		}
		if secret := cfgString(cfg, "secret", ""); secret != "" {
			timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(timestamp + "\n" + secret))
			sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			separator := "?"
			if strings.Contains(webhook, "?") {
				separator = "&"
			}
			webhook += separator + "timestamp=" + timestamp + "&sign=" + url.QueryEscape(sign)
		}
		return notifyJSON(webhook, map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": "### " + title + "\n\n" + body},
		}, nil)

	case "wecom_bot":
		webhook := cfgString(cfg, "webhook", "")
		if webhook == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON(webhook, map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": title + "\n" + body},
		}, nil)

	case "wecom_app":
		corpID := cfgString(cfg, "corp_id", "")
		corpSecret := cfgString(cfg, "corp_secret", "")
		agentID := cfgInt(cfg, "agent_id", 0)
		if corpID == "" || corpSecret == "" || agentID == 0 {
			return missingChannelConfig(name)
		}
		tokenURL := "https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=" + url.QueryEscape(corpID) + "&corpsecret=" + url.QueryEscape(corpSecret)
		resp, err := http.Get(tokenURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var tokenResult struct {
			AccessToken string `json:"access_token"`
			ErrMsg      string `json:"errmsg"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResult); err != nil {
			return err
		}
		if tokenResult.AccessToken == "" {
			return fmt.Errorf("%s", tokenResult.ErrMsg)
		}
		toUser := cfgString(cfg, "to_user", "@all")
		return notifyJSON("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token="+url.QueryEscape(tokenResult.AccessToken), map[string]any{
			"touser":  toUser,
			"msgtype": "text",
			"agentid": agentID,
			"text":    map[string]any{"content": text},
		}, nil)

	case "wecom_smart":
		endpoint := cfgString(cfg, "url", "")
		if endpoint == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON(endpoint, map[string]any{"title": title, "content": body}, bearer(cfgString(cfg, "token", "")))

	case "igot":
		key := cfgString(cfg, "key", "")
		if key == "" {
			return missingChannelConfig(name)
		}
		return notifyForm("https://push.hellyw.com/"+url.PathEscape(key), url.Values{
			"title":   {title},
			"content": {body},
		}, nil)

	case "pushplus":
		token := cfgString(cfg, "token", "")
		if token == "" {
			return missingChannelConfig(name)
		}
		payload := map[string]any{"token": token, "title": title, "content": body, "template": "markdown"}
		if topic := cfgString(cfg, "topic", ""); topic != "" {
			payload["topic"] = topic
		}
		return notifyJSON("https://www.pushplus.plus/send", payload, nil)

	case "weplus":
		endpoint := cfgString(cfg, "url", "")
		if endpoint == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON(endpoint, map[string]any{"title": title, "content": body}, bearer(cfgString(cfg, "token", "")))

	case "wxpusher":
		appToken := cfgString(cfg, "app_token", "")
		if appToken == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON("https://wxpusher.zjiecode.com/api/send/message", map[string]any{
			"appToken":    appToken,
			"content":     text,
			"summary":     title,
			"contentType": 1,
			"uids":        cfgList(cfg, "uids"),
			"topicIds":    cfgList(cfg, "topic_ids"),
		}, nil)

	case "wxpusher_spt":
		spt := cfgString(cfg, "spt", "")
		if spt == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON("https://wxpusher.zjiecode.com/api/send/message/simple-push", map[string]any{
			"spt":     spt,
			"content": text,
			"summary": title,
		}, nil)

	case "openilink", "wpush":
		endpoint := cfgString(cfg, "url", "")
		if endpoint == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON(endpoint, map[string]any{"title": title, "content": body}, bearer(cfgString(cfg, "token", "")))

	case "synology_chat":
		endpoint := cfgString(cfg, "url", "")
		if endpoint == "" {
			return missingChannelConfig(name)
		}
		payload, _ := json.Marshal(map[string]string{"text": text})
		return notifyForm(endpoint, url.Values{"payload": {string(payload)}}, nil)

	case "feishu":
		webhook := cfgString(cfg, "webhook", "")
		if webhook == "" {
			return missingChannelConfig(name)
		}
		payload := map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": text},
		}
		if secret := cfgString(cfg, "secret", ""); secret != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			stringToSign := timestamp + "\n" + secret
			mac := hmac.New(sha256.New, []byte(stringToSign))
			mac.Write([]byte{})
			payload["timestamp"] = timestamp
			payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		return notifyJSON(webhook, payload, nil)

	case "pushme":
		base := cfgString(cfg, "url", "https://push.i-i.me")
		key := cfgString(cfg, "push_key", "")
		if key == "" {
			return missingChannelConfig(name)
		}
		return notifyForm(strings.TrimRight(base, "/")+"/"+url.PathEscape(key), url.Values{
			"title":   {title},
			"content": {body},
		}, nil)

	case "chronocat":
		endpoint := cfgString(cfg, "url", "http://127.0.0.1:5500")
		target := cfgString(cfg, "target", "")
		if target == "" {
			return missingChannelConfig(name)
		}
		return notifyJSON(strings.TrimRight(endpoint, "/")+"/api/message/send", map[string]any{
			"target":      target,
			"target_type": cfgString(cfg, "target_type", "private"),
			"message":     text,
		}, bearer(cfgString(cfg, "token", "")))

	case "custom":
		endpoint := cfgString(cfg, "url", "")
		if endpoint == "" {
			return missingChannelConfig(name)
		}
		method := strings.ToUpper(cfgString(cfg, "method", "POST"))
		headers := map[string]string{}
		if raw := cfgString(cfg, "headers", ""); raw != "" {
			_ = json.Unmarshal([]byte(raw), &headers)
		}
		bodyTemplate := cfgString(cfg, "body", `{"title":"{title}","content":"{content}"}`)
		replacer := strings.NewReplacer(
			"{title}", escapeJSONString(title),
			"{content}", escapeJSONString(body),
		)
		rendered := replacer.Replace(bodyTemplate)
		contentType := headers["Content-Type"]
		if contentType == "" {
			contentType = "application/json"
		}
		return notifyHTTP(method, endpoint, []byte(rendered), contentType, headers)

	default:
		return i18n.E("service.notify.channel_unsupported", "channel", name)
	}
}

func escapeJSONString(value string) string {
	data, _ := json.Marshal(value)
	text := string(data)
	return strings.Trim(text, `"`)
}
