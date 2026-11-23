// Package channels 提供官方通知 helper 内置的渠道实现。
package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"lactssl/internal/plugin/impl/notify"
)

func cfgString(cfg map[string]any, key, fallback string) string {
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

func cfgInt(cfg map[string]any, key string, fallback int) int {
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

func cfgBool(cfg map[string]any, key string) bool {
	switch value := cfg[key].(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
}

func cfgList(cfg map[string]any, key string) []string {
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
	_, err := notifyHTTPResponse(method, endpoint, payload, contentType, headers)
	return err
}

func notifyHTTPResponse(method, endpoint string, payload []byte, contentType string, headers map[string]string) ([]byte, error) {
	if err := outbound.ValidateServiceURL(endpoint); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := outbound.NewClient(15*time.Second, false).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = resp.Status
		}
		return nil, fmt.Errorf("%s", text)
	}
	return body, nil
}

func notifyJSON(endpoint string, payload any, headers map[string]string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body, err := notifyHTTPResponse(http.MethodPost, endpoint, data, "application/json", headers)
	if err != nil {
		return err
	}
	return checkNotifyResponse(body, true)
}

func notifyForm(endpoint string, values url.Values, headers map[string]string) error {
	body, err := notifyHTTPResponse(http.MethodPost, endpoint, []byte(values.Encode()), "application/x-www-form-urlencoded", headers)
	if err != nil {
		return err
	}
	return checkNotifyResponse(body, false)
}

// checkNotifyResponse 识别 HTTP 200 里的业务错误。很多国内通知 API
// 用 200 + errcode/code/retcode 表达失败；不检查会静默丢通知。
func checkNotifyResponse(body []byte, requireJSON bool) error {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	if lower == "ok" || lower == "success" || lower == "true" {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		if requireJSON {
			return i18n.E("service.notify.channel_failed", "error", "invalid JSON response: "+truncateText(text, 200))
		}
		// 表单接口成功时可能返回其他纯文本。
		return nil
	}
	if len(object) == 0 {
		return nil
	}
	if value, ok := object["ok"].(bool); ok && !value {
		return businessError(object, "ok=false")
	}
	if value, ok := object["success"].(bool); ok && !value {
		return businessError(object, "success=false")
	}
	if value, exists := object["errcode"]; exists {
		if number, ok := numberValue(value); ok && number != 0 {
			return businessError(object, "errcode")
		}
	}
	if value, exists := object["retcode"]; exists {
		if number, ok := numberValue(value); ok && number != 0 {
			return businessError(object, "retcode")
		}
	}
	if value, exists := object["code"]; exists {
		if number, ok := numberValue(value); ok {
			// 0/1/200/1000 覆盖 PushDeer/ServerChan、常见自建 API、
			// Bark/PushPlus、WxPusher。
			if number != 0 && number != 1 && number != 200 && number != 1000 {
				return businessError(object, "code")
			}
		}
	}
	if value, ok := object["status"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "error", "failed", "fail":
			return businessError(object, "status")
		}
	}
	if value, exists := object["error"]; exists && value != nil {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return i18n.E("service.notify.channel_failed", "error", truncateText(text, 200))
		}
	}
	return nil
}

func businessError(object map[string]any, field string) error {
	for _, key := range []string{"errmsg", "message", "msg", "description", "error"} {
		if text, ok := object[key].(string); ok && strings.TrimSpace(text) != "" {
			return i18n.E("service.notify.channel_failed", "error", truncateText(text, 200))
		}
	}
	if text, ok := object[field].(string); ok && strings.TrimSpace(text) != "" {
		return i18n.E("service.notify.channel_failed", "error", text)
	}
	return i18n.E("service.notify.channel_failed", "error", field)
}

func numberValue(value any) (float64, bool) {
	switch item := value.(type) {
	case float64:
		return item, true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`(`, `\(`, `)`, `\)`, `#`, `\#`, `>`, `\>`, `~`, `\~`,
	"`", "\\`", `@`, `\@`,
)

// escapeMarkdown 避免证书错误/域名中的 markdown 链接或 @mention 被渲染。
func escapeMarkdown(value string) string {
	return markdownEscaper.Replace(value)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit - 3
	for cut > 0 && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return value[:cut] + "..."
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

func escapeJSONString(value string) string {
	data, _ := json.Marshal(value)
	text := string(data)
	if len(text) < 2 {
		return ""
	}
	// 不能使用 strings.Trim(text, `"`)：当内容本身以引号结尾时，marshal
	// 结果是 "a\""，Trim 会连转义引号一起删掉，生成非法 JSON。
	return text[1 : len(text)-1]
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

// channel 用一份定义加发送函数实现 Notifier，避免为每个渠道维护样板类型。
type channel struct {
	def  notify.Definition
	send func(cfg map[string]any, msg notify.Message) error
}

func (c channel) Definition() notify.Definition { return c.def }
func (c channel) Notify(cfg map[string]any, msg notify.Message) error {
	return c.send(cfg, msg)
}

func newChannel(id string, fields []notify.Field, send func(cfg map[string]any, msg notify.Message) error) channel {
	return channel{
		def: notify.Definition{
			ID:     id,
			Name:   id,
			Fields: fields,
		},
		send: send,
	}
}

func text(key, labelKey string, required bool) notify.Field {
	return notify.Field{Key: key, LabelKey: labelKey, Type: "text", Required: required}
}

func password(key, labelKey string, required bool) notify.Field {
	return notify.Field{Key: key, LabelKey: labelKey, Type: "password", Secret: true, Required: required}
}

func number(key, labelKey string, def int) notify.Field {
	return notify.Field{Key: key, LabelKey: labelKey, Type: "number", Default: strconv.Itoa(def)}
}

func selectField(key, labelKey, def string, options ...notify.Option) notify.Field {
	return notify.Field{Key: key, LabelKey: labelKey, Type: "select", Default: def, Options: options}
}

func opt(value, labelKey string) notify.Option {
	return notify.Option{Value: value, LabelKey: labelKey}
}

func textarea(key, labelKey, placeholder string) notify.Field {
	return notify.Field{Key: key, LabelKey: labelKey, Type: "textarea", Placeholder: placeholder}
}

func secretTextarea(key, labelKey, placeholder string) notify.Field {
	field := textarea(key, labelKey, placeholder)
	field.Secret = true
	return field
}
