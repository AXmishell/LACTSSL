package util

import "strings"

// ChainPEM 去掉 fullchain 中的第一张叶子证书，返回中间证书链。
func ChainPEM(fullchain string) string {
	blocks := strings.Split(fullchain, "-----END CERTIFICATE-----")
	if len(blocks) < 2 {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks[1:] {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block)
		b.WriteString("\n-----END CERTIFICATE-----\n")
	}
	return b.String()
}
