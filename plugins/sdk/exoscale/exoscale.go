// Package exoscale 实现 Exoscale EXO2-HMAC-SHA256 请求签名。
package exoscale

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Signer 给 Exoscale API 请求添加 Authorization。
type Signer struct {
	APIKey    string
	APISecret string
	Now       func() time.Time
}

// Sign 计算签名。
func (s Signer) Sign(req *http.Request, body []byte) error {
	if s.APIKey == "" || s.APISecret == "" {
		return fmt.Errorf("exoscale: api key and secret are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	expiration := now().UTC().Add(10 * time.Minute)

	names, values := queryValues(req)
	headerParts := []string{"EXO2-HMAC-SHA256 credential=" + s.APIKey}
	if len(names) > 0 {
		headerParts = append(headerParts, "signed-query-args="+strings.Join(names, ";"))
	}
	headerParts = append(headerParts, fmt.Sprintf("expires=%d", expiration.Unix()))

	parts := []string{
		fmt.Sprintf("%s %s", req.Method, req.URL.EscapedPath()),
		string(body),
		values,
		"",
		fmt.Sprintf("%d", expiration.Unix()),
	}
	mac := hmac.New(sha256.New, []byte(s.APISecret))
	mac.Write([]byte(strings.Join(parts, "\n")))
	headerParts = append(headerParts, "signature="+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Authorization", strings.Join(headerParts, ","))
	return nil
}

func queryValues(req *http.Request) ([]string, string) {
	query := req.URL.Query()
	names := make([]string, 0, len(query))
	for name, values := range query {
		if len(values) == 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var joined strings.Builder
	for _, name := range names {
		joined.WriteString(query.Get(name))
	}
	return names, joined.String()
}
