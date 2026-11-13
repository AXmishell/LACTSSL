package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

// blockedOutboundNets 是明确不应作为出站 HTTP/TLS 目标的网段。
// 保留回环与私网地址，便于 Gotify、自建面板、内网 webhook 等合理场景；
// 但云元数据、链路本地、保留/文档/NAT64 网段一律拒绝，降低 SSRF 风险。
var blockedOutboundNets = buildBlockedOutboundNets()

func buildBlockedOutboundNets() []*net.IPNet {
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

// isBlockedOutboundIP 判断地址是否禁止作为出站目标。
func isBlockedOutboundIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, network := range blockedOutboundNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// dialSafe 在解析后逐个校验目标地址并直接拨号，避免 DNS 重新解析到云元数据地址。
func dialSafe(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if isBlockedOutboundIP(ip) {
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
		if isBlockedOutboundIP(item.IP) {
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

// newOutboundClient 返回带安全拨号器的 HTTP 客户端。
// skipVerify 仅用于用户明确允许的自签证书场景。
func newOutboundClient(timeout time.Duration, skipVerify bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialSafe,
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

// ValidateWebhookURL 校验部署 webhook 地址。
// Webhook 会携带证书私钥，因此公网地址必须使用 HTTPS；
// 回环/私网/.local/.internal 目标允许 HTTP，便于本地接收端。
func ValidateWebhookURL(raw string) error {
	if err := ValidateOutboundURL(raw); err != nil {
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

var outboundURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// SanitizeErrorText 脱敏错误文本中的 URL 路径与查询参数，避免把 token 写入日志。
func SanitizeErrorText(text string) string {
	if text == "" {
		return ""
	}
	return outboundURLPattern.ReplaceAllStringFunc(text, redactURL)
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
