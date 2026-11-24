package util

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/providers/api/deploy"
)

// Client 返回部署插件共用的出站 HTTP 客户端。
// skip_verify=true 仅用于用户明确允许的面板自签证书场景。
func Client(cfg map[string]string) *http.Client {
	return outbound.NewClient(30*time.Second, Bool(cfg["skip_verify"]))
}

// ClientWithTimeout 返回指定超时的出站客户端。
func ClientWithTimeout(cfg map[string]string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return outbound.NewClient(timeout, Bool(cfg["skip_verify"]))
}

// BaseURL 清理并校验用户填写的 HTTP(S) 地址。
// 公网目标必须使用 HTTPS；回环/私网面板允许 HTTP，便于自建服务。
func BaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if err := outbound.ValidateServiceURL(value); err != nil {
		return "", err
	}
	return value, nil
}

// JoinURL 拼接路径，避免出现重复或缺失的斜杠。
func JoinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// Bool 解析常见布尔写法。
func Bool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

// Int 解析整数，失败时返回默认值。
func Int(value string, fallback int) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return number
}

// FirstDomain 返回证书主域名，优先 domain，其次 domains 的第一个。
func FirstDomain(cert *deploy.NativeCertificate) string {
	if cert == nil {
		return ""
	}
	if domain := strings.TrimSpace(cert.Domain); domain != "" {
		return domain
	}
	if len(cert.Domains) > 0 {
		return strings.TrimSpace(cert.Domains[0])
	}
	return ""
}

// Domains 返回证书包含的全部域名（去重、去空）。
func Domains(cert *deploy.NativeCertificate) []string {
	if cert == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(cert.Domains)+1)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	add(cert.Domain)
	for _, domain := range cert.Domains {
		add(domain)
	}
	return out
}

// DomainMatch 判断证书域名与目标域名是否匹配，支持通配符。
func DomainMatch(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if wildcardMatches(left, right) || wildcardMatches(right, left) {
		return true
	}
	return false
}

// wildcardMatches 判断 pattern 是否为 *.example.com，且 host 只比通配符
// 多一层标签。RFC 通配符证书不覆盖 a.b.example.com。
func wildcardMatches(pattern, host string) bool {
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}
	suffix := pattern[1:] // .example.com
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	return label != "" && !strings.Contains(label, ".")
}

// DomainsMatch 判断两组域名是否存在交集。
func DomainsMatch(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if DomainMatch(a, b) {
				return true
			}
		}
	}
	return false
}

// MatchAny 判断目标是否与任一证书域名匹配。
func MatchAny(certDomains, targets []string) bool {
	return DomainsMatch(certDomains, targets)
}

// CertName 生成默认证书名称：主域名 + 时间戳。
func CertName(cert *deploy.NativeCertificate, prefix string) string {
	domain := FirstDomain(cert)
	if domain == "" {
		domain = "certificate"
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = domain
	}
	return prefix + "-" + time.Now().Format("20060102150405")
}

// CleanName 把域名转换为适合做资源名称的字符串。
func CleanName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// RawQuery 构造查询字符串，空值会被忽略。
func RawQuery(values map[string]string) string {
	query := url.Values{}
	for key, value := range values {
		if strings.TrimSpace(value) != "" {
			query.Set(key, value)
		}
	}
	return query.Encode()
}
