// Package netutil 提供与网络相关的通用解析工具。
package netutil

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// Source 表示最终客户端 IP 来自哪一类输入。
type Source string

const (
	// SourceRemote 表示直接使用 TCP 对端地址。
	SourceRemote Source = "remote"
	// SourceXForwardedFor 表示来自 X-Forwarded-For。
	SourceXForwardedFor Source = "x-forwarded-for"
	// SourceXRealIP 表示来自 X-Real-IP。
	SourceXRealIP Source = "x-real-ip"
	// SourceForwarded 表示来自 RFC 7239 Forwarded。
	SourceForwarded Source = "forwarded"
	// SourceCFConnectingIP 表示来自 Cloudflare CF-Connecting-IP。
	SourceCFConnectingIP Source = "cf-connecting-ip"
)

const (
	defaultMaxHops        = 32
	defaultMaxHeaderBytes = 8 << 10
)

// Result 是客户端 IP 解析结果。IP 无效表示 RemoteAddr 本身无法解析。
type Result struct {
	IP       netip.Addr
	Source   Source
	Skipped  int
	Warnings []string
}

// Options 控制解析器对有界输入的限制与可选信任模式。
type Options struct {
	MaxHops         int
	MaxHeaderBytes  int
	EnableForwarded bool
	Cloudflare      bool
	PeerVerified    bool // mTLS 等独立于 IP 的代理身份验证已通过
}

// DefaultOptions 返回生产环境使用的默认限制。
func DefaultOptions() Options {
	return Options{MaxHops: defaultMaxHops, MaxHeaderBytes: defaultMaxHeaderBytes}
}

// TrustedSet 是预编译的可信代理集合。
type TrustedSet struct {
	prefixes []netip.Prefix
}

// CompileTrusted 把配置中的 IP/CIDR 预编译为前缀集合。
// 空项会被忽略；非法项返回错误，调用方应安全降级为空集合。
func CompileTrusted(values []string) (TrustedSet, error) {
	set := TrustedSet{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return TrustedSet{}, fmt.Errorf("无效的可信代理 CIDR %q: %w", raw, err)
			}
			if prefix.Addr().Zone() != "" {
				return TrustedSet{}, fmt.Errorf("可信代理不支持带 zone 的地址: %s", raw)
			}
			prefix, err = normalizePrefix(prefix)
			if err != nil {
				return TrustedSet{}, err
			}
			if prefix.Bits() == 0 {
				return TrustedSet{}, fmt.Errorf("可信代理不允许使用 /0 网段: %s", raw)
			}
			set.prefixes = append(set.prefixes, prefix)
			continue
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return TrustedSet{}, fmt.Errorf("无效的可信代理 IP %q: %w", raw, err)
		}
		if addr.Zone() != "" {
			return TrustedSet{}, fmt.Errorf("可信代理不支持带 zone 的地址: %s", raw)
		}
		addr = addr.Unmap()
		set.prefixes = append(set.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return set, nil
}

func normalizePrefix(prefix netip.Prefix) (netip.Prefix, error) {
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("IPv4-mapped 网段前缀过短: %s", prefix)
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked(), nil
}

// Contains 判断地址是否属于可信集合。地址会先做 Unmap。
func (s TrustedSet) Contains(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range s.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Empty 表示集合中没有可信项。
func (s TrustedSet) Empty() bool {
	return len(s.prefixes) == 0
}

// ParsePeer 解析 TCP 对端地址。兼容带端口、裸 IP、方括号与 zone 写法。
func ParsePeer(remoteAddr string) (netip.Addr, bool) {
	raw := strings.TrimSpace(remoteAddr)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	if index := strings.LastIndexByte(raw, '%'); index >= 0 {
		raw = raw[:index]
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// ParseHeaderIP 解析转发头中的单个地址。XFF 标准不带端口，因此带端口的项视为非法。
func ParseHeaderIP(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "[]")
	if raw == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// Resolve 计算最终客户端 IP。
//
// 只有 TCP 对端命中可信集合时才读取转发头；X-Forwarded-For 从右向左跳过可信项，
// 遇到非法项则整个头作废；随后尝试 X-Real-IP；全部不可用时回退对端地址。
func Resolve(remoteAddr string, header http.Header, trusted TrustedSet) Result {
	return ResolveWithOptions(remoteAddr, header, trusted, DefaultOptions())
}

// ResolveWithOptions 与 Resolve 相同，但允许调整跳数与头长度上限。
func ResolveWithOptions(remoteAddr string, header http.Header, trusted TrustedSet, opts Options) Result {
	if opts.MaxHops <= 0 {
		opts.MaxHops = defaultMaxHops
	}
	if opts.MaxHeaderBytes <= 0 {
		opts.MaxHeaderBytes = defaultMaxHeaderBytes
	}

	peer, ok := ParsePeer(remoteAddr)
	if !ok {
		return Result{Source: SourceRemote, Warnings: []string{"invalid_remote_addr"}}
	}
	peerTrusted := trusted.Contains(peer)
	if !peerTrusted && !opts.PeerVerified {
		return Result{IP: peer, Source: SourceRemote}
	}

	warnings := make([]string, 0, 3)
	if opts.Cloudflare && peerTrusted {
		if raw := strings.TrimSpace(header.Get("CF-Connecting-IP")); raw != "" {
			if len(raw) > opts.MaxHeaderBytes {
				warnings = append(warnings, "cf_connecting_ip_too_large")
			} else if ip, ok := ParseHeaderIP(raw); ok && !trusted.Contains(ip) {
				return Result{IP: ip, Source: SourceCFConnectingIP, Warnings: warnings}
			} else {
				warnings = append(warnings, "cf_connecting_ip_unusable")
			}
		}
	}

	if values := header.Values("X-Forwarded-For"); len(values) > 0 {
		raw := strings.Join(values, ",")
		if len(raw) > opts.MaxHeaderBytes {
			warnings = append(warnings, "x_forwarded_for_too_large")
		} else if ip, skipped, ok := resolveChain(raw, trusted, opts.MaxHops); ok {
			return Result{IP: ip, Source: SourceXForwardedFor, Skipped: skipped, Warnings: warnings}
		} else {
			warnings = append(warnings, "x_forwarded_for_unusable")
		}
	}

	if raw := strings.TrimSpace(header.Get("X-Real-IP")); raw != "" {
		if len(raw) > opts.MaxHeaderBytes {
			warnings = append(warnings, "x_real_ip_too_large")
		} else if ip, ok := ParseHeaderIP(raw); ok && !trusted.Contains(ip) {
			return Result{IP: ip, Source: SourceXRealIP, Skipped: 0, Warnings: warnings}
		} else {
			warnings = append(warnings, "x_real_ip_unusable")
		}
	}

	if opts.EnableForwarded {
		if values := header.Values("Forwarded"); len(values) > 0 {
			raw := strings.Join(values, ",")
			if len(raw) > opts.MaxHeaderBytes {
				warnings = append(warnings, "forwarded_too_large")
			} else if ip, skipped, ok := resolveForwarded(raw, trusted, opts.MaxHops); ok {
				return Result{IP: ip, Source: SourceForwarded, Skipped: skipped, Warnings: warnings}
			} else {
				warnings = append(warnings, "forwarded_unusable")
			}
		}
	}

	return Result{IP: peer, Source: SourceRemote, Warnings: append(warnings, "headers_unusable")}
}

func resolveChain(raw string, trusted TrustedSet, maxHops int) (netip.Addr, int, bool) {
	items := strings.Split(raw, ",")
	if len(items) > maxHops {
		return netip.Addr{}, 0, false
	}
	skipped := 0
	for index := len(items) - 1; index >= 0; index-- {
		ip, ok := ParseHeaderIP(items[index])
		if !ok {
			return netip.Addr{}, skipped, false
		}
		if trusted.Contains(ip) {
			skipped++
			continue
		}
		return ip, skipped, true
	}
	// 所有项都可信：没有可识别的客户端，交给 X-Real-IP 或对端兜底。
	return netip.Addr{}, skipped, false
}

// resolveForwarded 解析 RFC 7239 Forwarded，从右向左跳过可信代理。
func resolveForwarded(raw string, trusted TrustedSet, maxHops int) (netip.Addr, int, bool) {
	elements := splitQuoted(raw, ',')
	if len(elements) > maxHops {
		return netip.Addr{}, 0, false
	}
	skipped := 0
	for index := len(elements) - 1; index >= 0; index-- {
		node, ok := forwardedForNode(elements[index])
		if !ok {
			return netip.Addr{}, skipped, false
		}
		ip, ok := parseForwardedNode(node)
		if !ok {
			return netip.Addr{}, skipped, false
		}
		if trusted.Contains(ip) {
			skipped++
			continue
		}
		return ip, skipped, true
	}
	return netip.Addr{}, skipped, false
}

func forwardedForNode(element string) (string, bool) {
	for _, parameter := range splitQuoted(element, ';') {
		key, value, ok := strings.Cut(parameter, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "for") {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func parseForwardedNode(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = unquoted
		} else {
			raw = strings.Trim(raw, `"`)
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "unknown") || strings.HasPrefix(raw, "_") {
		return netip.Addr{}, false
	}
	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			return netip.Addr{}, false
		}
		host := raw[1:end]
		if rest := raw[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") || !validNodePort(rest[1:]) {
				return netip.Addr{}, false
			}
		}
		return ParseHeaderIP(host)
	}
	// IPv4 可带端口；IPv6 不带方括号时按纯地址解析，避免误判末尾冒号段。
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if !validNodePort(port) {
			return netip.Addr{}, false
		}
		return ParseHeaderIP(host)
	}
	return ParseHeaderIP(raw)
}

func validNodePort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port >= 0 && port <= 65535
}

// splitQuoted 按分隔符切分，忽略双引号内的分隔符与转义字符。
func splitQuoted(raw string, separator byte) []string {
	out := make([]string, 0, 4)
	start := 0
	inQuote := false
	escaped := false
	for index := 0; index < len(raw); index++ {
		ch := raw[index]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if ch == separator && !inQuote {
			out = append(out, raw[start:index])
			start = index + 1
		}
	}
	out = append(out, raw[start:])
	return out
}
