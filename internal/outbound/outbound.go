// Package outbound 提供带 SSRF 防护的出站 HTTP 客户端与 URL 校验。
//
// 主程序的通知、监控与部署插件共用同一套规则：公网目标允许 HTTP/HTTPS，
// 但拒绝云元数据、链路本地与保留网段；拨号阶段会重新校验解析结果，
// 避免 DNS 重绑定绕过配置阶段的检查。
package outbound

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

// blockedNets 是明确不应作为出站 HTTP/TLS 目标的网段。
// 保留回环与私网地址，便于 Gotify、自建面板、内网 webhook 等合理场景；
// 但云元数据、链路本地、保留/文档/NAT64 网段一律拒绝，降低 SSRF 风险。
var blockedNets = buildBlockedNets()

func buildBlockedNets() []*net.IPNet {
	values := []string{
		"0.0.0.0/8",
		"169.254.0.0/16",     // 链路本地 / 云元数据
		"100.100.100.200/32", // 阿里云元数据
		"192.0.0.0/24",       // IETF 协议分配
		"192.88.99.0/24",     // 6to4 中继
		"198.18.0.0/15",      // 基准测试
		"240.0.0.0/4",        // 保留
		"255.255.255.255/32",
		"64:ff9b::/96",      // NAT64
		"64:ff9b:1::/48",    // 本地 NAT64
		"100::/64",          // 丢弃前缀
		"2001::/32",         // Teredo
		"2001:10::/28",      // ORCHID
		"2001:db8::/32",     // 文档
		"2002::/16",         // 6to4
		"fd00:ec2::254/128", // AWS IMDS IPv6
	}
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		if _, network, err := net.ParseCIDR(value); err == nil {
			out = append(out, network)
		}
	}
	return out
}

// isBlockedIP 判断地址是否禁止作为出站目标。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, network := range blockedNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// DialSafe 在解析后逐个校验目标地址并直接拨号，避免 DNS 重新解析到云元数据地址。
func DialSafe(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if isBlockedIP(ip) {
			return nil, i18n.E("service.url_blocked", "url", address)
		}
		return dialer.DialContext(ctx, network, address)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, item := range addresses {
		if isBlockedIP(item.IP) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(item.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, i18n.E("service.url_blocked", "url", address)
}

// NewClient 返回带安全拨号器的 HTTP 客户端。
// skipVerify 仅用于用户明确允许的自签证书场景。
func NewClient(timeout time.Duration, skipVerify bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           DialSafe,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: skipVerify,
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// ValidateURL 校验用户配置的出站 HTTP(S) 地址。
// 只允许 http/https，拒绝 userinfo 与非法端口，避免把请求发送到
// file:// 等非预期协议，或让 URL 中的凭据信息被误当作主机。
func ValidateURL(raw string) error {
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
	if ip := net.ParseIP(strings.Trim(parsed.Hostname(), "[]")); ip != nil && isBlockedIP(ip) {
		return i18n.E("service.url_blocked", "url", value)
	}
	return nil
}

// ValidateWebhookURL 校验部署 webhook 地址。
// Webhook 会携带证书私钥，因此公网地址必须使用 HTTPS；
// 回环/私网/.local/.internal 目标允许 HTTP，便于本地接收端。
func ValidateWebhookURL(raw string) error {
	if err := ValidateURL(raw); err != nil {
		return err
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return i18n.E("service.url_invalid", "url", raw)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "localhost" || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		return nil
	}
	return i18n.E("service.deploy.webhook_https_required")
}

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// SanitizeErrorText 脱敏错误文本中的 URL 路径与查询参数，避免把 token 写入日志。
func SanitizeErrorText(text string) string {
	if text == "" {
		return ""
	}
	return urlPattern.ReplaceAllStringFunc(text, redactURL)
}

// SafeError 返回适合写入日志或接口响应的错误文本。
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		label := strings.TrimSpace(urlErr.Op)
		if label == "" {
			label = "request"
		}
		inner := ""
		if urlErr.Err != nil {
			inner = SanitizeErrorText(urlErr.Err.Error())
		}
		if inner == "" {
			return fmt.Sprintf("%s %s", label, redactURL(urlErr.URL))
		}
		return fmt.Sprintf("%s %s: %s", label, redactURL(urlErr.URL), inner)
	}
	return SanitizeErrorText(err.Error())
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "http://***"
	}
	parsed.User = nil
	if parsed.Path != "" && parsed.Path != "/" {
		parsed.Path = "/***"
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "***"
	}
	parsed.Fragment = ""
	return parsed.String()
}
