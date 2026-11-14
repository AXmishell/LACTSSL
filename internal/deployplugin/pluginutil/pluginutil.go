// Package pluginutil 提供部署插件实现共用的配置与证书转换工具。
package pluginutil

import (
	"strings"
	"time"

	"lactssl/internal/deployplugin"
	"lactssl/internal/model"
)

// Merge 合并凭据认证字段与部署节点参数，部署节点中的同名参数优先。
// 插件实现只读取自己声明的字段，因此这里不做字段白名单过滤。
func Merge(auth, config map[string]string) map[string]string {
	out := make(map[string]string, len(auth)+len(config))
	for key, value := range auth {
		out[key] = value
	}
	for key, value := range config {
		out[key] = value
	}
	return out
}

// SplitList 按逗号、分号、空白或换行拆分多值配置。
func SplitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Truncate 按字符数截断文本，用于限制错误信息中的响应体大小。
func Truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}

// Certificate 把插件协议中的证书材料还原为内部结构。
func Certificate(value deployplugin.Certificate) *model.Certificate {
	out := &model.Certificate{
		Domains:       append([]string(nil), value.Domains...),
		PrimaryDomain: value.Domain,
		Issuer:        value.Issuer,
		Serial:        value.Serial,
		CertPEM:       value.CertPEM,
		FullchainPEM:  value.FullchainPEM,
		KeyPEM:        value.KeyPEM,
	}
	if out.PrimaryDomain == "" && len(out.Domains) > 0 {
		out.PrimaryDomain = out.Domains[0]
	}
	if parsed, err := time.Parse(time.RFC3339, value.NotBefore); err == nil {
		out.NotBefore = parsed
	}
	if parsed, err := time.Parse(time.RFC3339, value.NotAfter); err == nil {
		out.NotAfter = parsed
	}
	return out
}
