// Package edgegrid 实现 Akamai EdgeGrid EG1-HMAC-SHA256 签名。
package edgegrid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const authType = "EG1-HMAC-SHA256"

// Config 是 EdgeGrid 凭据。
type Config struct {
	Host         string
	ClientToken  string
	ClientSecret string
	AccessToken  string
	AccountKey   string
	HeaderToSign []string
	Now          func() time.Time
}

// Sign 给请求添加 Authorization。
func (c Config) Sign(req *http.Request, body []byte) error {
	if c.Host == "" || c.ClientToken == "" || c.ClientSecret == "" || c.AccessToken == "" {
		return fmt.Errorf("edgegrid: host, client_token, client_secret and access_token are required")
	}
	if req.URL.Host == "" {
		req.URL.Host = c.Host
	}
	if req.URL.Scheme == "" {
		req.URL.Scheme = "https"
	}
	if c.AccountKey != "" {
		values := req.URL.Query()
		values.Set("accountSwitchKey", c.AccountKey)
		req.URL.RawQuery = values.Encode()
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	timestamp := now().UTC().Format("20060102T15:04:05-0700")
	nonce := randomNonce()

	authPrefix := fmt.Sprintf("%s client_token=%s;access_token=%s;timestamp=%s;nonce=%s;",
		authType, c.ClientToken, c.AccessToken, timestamp, nonce)

	msgPath := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		msgPath += "?" + req.URL.RawQuery
	}
	contentHash := ""
	if req.Method == http.MethodPost && len(body) > 0 {
		sum := sha256.Sum256(body)
		contentHash = base64.StdEncoding.EncodeToString(sum[:])
	}
	msg := strings.Join([]string{
		req.Method,
		req.URL.Scheme,
		req.URL.Host,
		msgPath,
		canonicalHeaders(req.Header, c.HeaderToSign),
		contentHash,
		authPrefix,
	}, "\t")

	signingKey := hmacBase64([]byte(c.ClientSecret), []byte(timestamp))
	signature := hmacBase64(signingKey, []byte(msg))
	req.Header.Set("Authorization", authPrefix+"signature="+string(signature))
	return nil
}

func canonicalHeaders(header http.Header, toSign []string) string {
	if len(toSign) == 0 {
		return ""
	}
	allowed := make(map[string]bool, len(toSign))
	for _, key := range toSign {
		allowed[strings.ToLower(key)] = true
	}
	keys := make([]string, 0, len(header))
	for key := range header {
		if allowed[strings.ToLower(key)] {
			keys = append(keys, strings.ToLower(key))
		}
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		value := strings.TrimSpace(header.Get(key))
		out = append(out, key+":"+value)
	}
	return strings.Join(out, "\t")
}

func hmacBase64(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return []byte(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func randomNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
