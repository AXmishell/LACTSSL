// Package util 提供部署插件实现共用的配置与证书转换工具。
package util

import (
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	deploy "lactssl/providers/api/deploy"
)

// Merge 合并凭据认证字段与部署节点参数，部署节点中的同名参数优先。
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

// Certificate 把插件协议中的证书材料还原为部署实现使用的结构。
func Certificate(value protocol.Certificate) *deploy.NativeCertificate {
	out := &deploy.NativeCertificate{
		Domains:      append([]string(nil), value.Domains...),
		CertPEM:      value.CertPEM,
		FullchainPEM: value.FullchainPEM,
		KeyPEM:       value.KeyPEM,
		Issuer:       value.Issuer,
		Serial:       value.Serial,
		NotBefore:    value.NotBefore,
		NotAfter:     value.NotAfter,
	}
	out.Domain = value.Domain
	if out.Domain == "" && len(out.Domains) > 0 {
		out.Domain = out.Domains[0]
	}
	return out
}

// VerifyFailed 把验证错误转换为统一的失败检查项，err 为 nil 时返回 nil。
func VerifyFailed(loc i18n.Locale, err error) []protocol.VerifyCheck {
	if err == nil {
		return nil
	}
	return []protocol.VerifyCheck{{
		Key:     "verify",
		Status:  protocol.VerifyStatusFailed,
		Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()}),
	}}
}
