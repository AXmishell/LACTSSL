package service

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"lactssl/internal/i18n"
)

// ValidateOutboundURL 校验用户配置的出站 HTTP(S) 地址。
// 只允许 http/https，拒绝 userinfo 与非法端口，避免把请求发送到
// file:// 等非预期协议，或让 URL 中的凭据信息被误当作主机。
func ValidateOutboundURL(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return i18n.E("service.url_invalid", "url", raw)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return i18n.E("service.url_invalid", "url", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return i18n.E("service.url_invalid", "url", value)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return i18n.E("service.url_invalid", "url", value)
		}
	}
	// 字面量地址在配置阶段就可以拦截；域名解析结果在拨号阶段再次校验。
	if ip := net.ParseIP(strings.Trim(parsed.Hostname(), "[]")); ip != nil && isBlockedOutboundIP(ip) {
		return i18n.E("service.url_blocked", "url", value)
	}
	return nil
}
