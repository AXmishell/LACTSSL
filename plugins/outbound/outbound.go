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

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
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
	// 仅对 IPv4-compatible（::/96）做内嵌 IPv4 检查；IPv4-mapped
	// （::ffff:a.b.c.d）已被上面 To4 转成 IPv4，NAT64/6to4/Teredo
	// 则由 blockedNets 整体覆盖。不能对所有 IPv6 取最后 32 位，否则
	// fd00::1、2001:4860:4860::8888 等合法地址会被误封。
	if isIPv4Compatible(ip) {
		embedded := net.IPv4(ip[12], ip[13], ip[14], ip[15])
		if isBlockedIPv4(embedded) {
			return true
		}
	}
	for _, network := range blockedNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isIPv4Compatible 判断是否为已废弃的 IPv4-compatible 形式（::/96）。
func isIPv4Compatible(ip net.IP) bool {
	if len(ip) != net.IPv6len {
		return false
	}
	for i := 0; i < 12; i++ {
		if ip[i] != 0 {
			return false
		}
	}
	return !ip.IsUnspecified() && !ip.IsLoopback()
}

func isBlockedIPv4(ip net.IP) bool {
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

// lookupIPAddr 便于测试替换。
var lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// lookupIPAddrTimeout 用于配置校验阶段的 DNS 查询，避免无 deadline 阻塞。
func lookupIPAddrTimeout(host string) ([]net.IPAddr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return lookupIPAddr(ctx, host)
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
	addresses, err := lookupIPAddr(ctx, host)
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
//
// 默认不跟随重定向：证书私钥/API 凭据通常放在 POST body 或自定义头中，
// 307/308 会把它们重放到 Location 指向的主机。确需重定向的调用方应使用
// 独立的客户端并显式实现同源、HTTPS-only 策略。
func NewClient(timeout time.Duration, skipVerify bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 safeProxyFromEnvironment,
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
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// safeProxyFromEnvironment 在把目标交给代理之前先校验解析结果。
//
// 原生 http.ProxyFromEnvironment 只会让 DialSafe 看到代理地址，目标由代理
// 解析，元数据/链路本地地址会绕过 blockedNets。这里先解析目标：
//   - 字面量或解析结果命中 blockedNets：拒绝；
//   - 目标为回环/私网：返回 nil，强制直连并由 DialSafe 再校验；
//   - 解析失败：仍交给环境代理（兼容仅代理可解析的内网 DNS 场景），
//     但不会命中已知的元数据字面量。
func safeProxyFromEnvironment(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil || proxy == nil {
		return proxy, err
	}
	if err := validateProxyTarget(req.URL); err != nil {
		return nil, err
	}
	if targetIsLocal(req.URL.Hostname()) {
		return nil, nil
	}
	return proxy, nil
}

func validateProxyTarget(target *url.URL) error {
	host := strings.Trim(target.Hostname(), "[]")
	if host == "" {
		return nil
	}
	if isMetadataHost(host) {
		return i18n.E("service.url_blocked", "url", RedactURL(target.String()))
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return i18n.E("service.url_blocked", "url", RedactURL(target.String()))
		}
		return nil
	}
	addresses, err := lookupIPAddrTimeout(host)
	if err != nil {
		return nil
	}
	for _, item := range addresses {
		if isBlockedIP(item.IP) {
			return i18n.E("service.url_blocked", "url", RedactURL(target.String()))
		}
	}
	return nil
}

func isMetadataHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "metadata", "metadata.google.internal", "instance-data", "instance-data.ec2.internal":
		return true
	default:
		return false
	}
}

func targetIsLocal(host string) bool {
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	addresses, err := lookupIPAddrTimeout(host)
	if err != nil {
		return false
	}
	for _, item := range addresses {
		if !item.IP.IsLoopback() && !item.IP.IsPrivate() {
			return false
		}
	}
	return len(addresses) > 0
}

// ValidateURL 校验用户配置的出站 HTTP(S) 地址。
// 只允许 http/https，拒绝 userinfo 与非法端口，避免把请求发送到
// file:// 等非预期协议，或让 URL 中的凭据信息被误当作主机。
func ValidateURL(raw string) error {
	value := strings.TrimSpace(raw)
	safe := RedactURL(value)
	if value == "" {
		return i18n.E("service.url_invalid", "url", safe)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if strings.Contains(parsed.Hostname(), "%") {
		// 拒绝 IPv6 zone ID，避免绕过字面量/IP 解析校验。
		return i18n.E("service.url_invalid", "url", safe)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return i18n.E("service.url_invalid", "url", safe)
		}
	}
	// 字面量地址在配置阶段就可以拦截；域名解析结果在拨号阶段再次校验。
	if ip := net.ParseIP(strings.Trim(parsed.Hostname(), "[]")); ip != nil && isBlockedIP(ip) {
		return i18n.E("service.url_blocked", "url", safe)
	}
	return nil
}

// ValidateServiceURL 校验部署面板/通知服务等用户配置的 HTTP(S) 地址。
// 公网目标必须使用 HTTPS；回环、私网和明确的本地域名允许 HTTP，便于
// 自建面板、内网 webhook 等合理场景。HTTP 目标会解析域名，只要解析结果
// 中出现公网地址就拒绝，避免把凭据或证书私钥明文发送到公网。
func ValidateServiceURL(raw string) error {
	if err := ValidateURL(raw); err != nil {
		return err
	}
	safe := RedactURL(strings.TrimSpace(raw))
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	if host == "" {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if isMetadataHost(host) {
		return i18n.E("service.url_blocked", "url", safe)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return i18n.E("service.url_blocked", "url", safe)
		}
		if ip.IsLoopback() || ip.IsPrivate() {
			return nil
		}
		return i18n.E("service.url_https_required", "url", safe)
	}
	addresses, lookupErr := lookupIPAddrTimeout(host)
	if lookupErr == nil {
		allLocal := len(addresses) > 0
		for _, item := range addresses {
			if isBlockedIP(item.IP) {
				return i18n.E("service.url_blocked", "url", safe)
			}
			if !item.IP.IsLoopback() && !item.IP.IsPrivate() {
				allLocal = false
			}
		}
		if allLocal {
			return nil
		}
		// 解析成功且存在公网地址：即使主机名看起来像内网名称，也必须 HTTPS。
		return i18n.E("service.url_https_required", "url", safe)
	}
	// 仅在 DNS 不可用时允许明确的本地后缀/单标签主机使用 HTTP；公网域名
	// 必须显式使用 HTTPS，避免把凭据发到明文公网。
	lower := strings.ToLower(host)
	if !strings.Contains(lower, ".") || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return nil
	}
	return i18n.E("service.url_https_required", "url", safe)
}

// RedactURL 返回适合写入错误/日志的 URL：去掉 userinfo、path、query 和 fragment。
func RedactURL(raw string) string {
	return redactURL(raw)
}

// ValidateWebhookURL 校验部署 webhook 地址。
// Webhook 会携带证书私钥，因此公网地址必须使用 HTTPS；
// 回环/私网/.local/.internal 目标允许 HTTP，便于本地接收端。
func ValidateWebhookURL(raw string) error {
	if err := ValidateURL(raw); err != nil {
		return err
	}
	safe := RedactURL(strings.TrimSpace(raw))
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return i18n.E("service.url_invalid", "url", safe)
	}
	if isMetadataHost(host) {
		return i18n.E("service.url_blocked", "url", safe)
	}
	// IP 字面量必须先于“无点主机名”判断，否则公网 IPv6 会被当成内网地址。
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return i18n.E("service.url_blocked", "url", safe)
		}
		if ip.IsLoopback() || ip.IsPrivate() {
			return nil
		}
		return i18n.E("service.deploy.webhook_https_required")
	}
	addresses, lookupErr := lookupIPAddrTimeout(host)
	if lookupErr == nil {
		allLocal := len(addresses) > 0
		for _, item := range addresses {
			if isBlockedIP(item.IP) {
				return i18n.E("service.url_blocked", "url", safe)
			}
			if !item.IP.IsLoopback() && !item.IP.IsPrivate() {
				allLocal = false
			}
		}
		if allLocal {
			return nil
		}
		return i18n.E("service.deploy.webhook_https_required")
	}
	if host == "localhost" || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
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
