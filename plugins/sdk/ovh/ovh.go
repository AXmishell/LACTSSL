// Package ovh 实现 OVH API 的 $1$ 签名。
package ovh

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Signer 使用 Application Key / Secret / Consumer Key 签名。
type Signer struct {
	ApplicationKey    string
	ApplicationSecret string
	ConsumerKey       string
	Now               func() time.Time
}

// Sign 给请求添加 OVH 头。timestamp 使用 OVH 服务器时间。
func (s Signer) Sign(req *http.Request, body []byte, timestamp int64) {
	if s.Now != nil && timestamp == 0 {
		timestamp = s.Now().Unix()
	}
	req.Header.Set("X-Ovh-Application", s.ApplicationKey)
	req.Header.Set("X-Ovh-Consumer", s.ConsumerKey)
	req.Header.Set("X-Ovh-Timestamp", fmt.Sprint(timestamp))
	target := fmt.Sprintf("%s+%s+%s+%s+%s+%d",
		s.ApplicationSecret, s.ConsumerKey, req.Method, req.URL.String(), string(body), timestamp)
	sum := sha1.Sum([]byte(target))
	req.Header.Set("X-Ovh-Signature", "$1$"+hex.EncodeToString(sum[:]))
}

// Endpoint 把 ovh-eu/ovh-ca/ovh-us 映射到 API 根地址。
func Endpoint(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "ovh-eu":
		return "https://eu.api.ovh.com/1.0"
	case "ovh-ca":
		return "https://ca.api.ovh.com/1.0"
	case "ovh-us":
		return "https://api.us.ovhcloud.com/1.0"
	default:
		return ""
	}
}
