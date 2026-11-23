// Package ucloud 实现 UCloud API 的 SHA1 签名。
//
// UCloud 的签名规则是：除 Signature 外的参数按 key 字典序排列，
// 依次拼接 key + value，再追加私钥，最后取 SHA1 十六进制。
package ucloud

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Sign 计算 UCloud Signature。
func Sign(params map[string]string, privateKey string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if strings.EqualFold(key, "Signature") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(params[key])
	}
	b.WriteString(privateKey)
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Encode 生成带 PublicKey 与 Signature 的表单请求体。
func Encode(params map[string]string, publicKey, privateKey string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	if publicKey != "" {
		values.Set("PublicKey", publicKey)
	}
	flat := make(map[string]string, len(values))
	for key := range values {
		flat[key] = values.Get(key)
	}
	values.Set("Signature", Sign(flat, privateKey))
	return values.Encode()
}

// String 把常见类型转换为 API 参数值。
func String(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case bool:
		return strconv.FormatBool(item)
	case int:
		return strconv.Itoa(item)
	case int64:
		return strconv.FormatInt(item, 10)
	case []string:
		return strings.Join(item, ",")
	default:
		return ""
	}
}
