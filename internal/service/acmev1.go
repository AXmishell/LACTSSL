package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-jose/go-jose/v4"
	"lactssl/internal/i18n"
)

const (
	acmeV1BodyLimit = 4 << 20
	acmeV1HTTPTime  = 60 * time.Second
)

// 抽取为变量便于测试环境跳过真实的 DNS 传播等待。
var (
	acmeV1DNSCheckTimeout = 2 * time.Minute
	acmeV1DNSRetry        = 3 * time.Second
)

// acmeV1Directory 是 ACME v1 目录资源。
// v1 目录使用 new-reg / new-authz / new-cert 三个固定入口，
// 与 v2 的 newAccount / newOrder 完全不同。
type acmeV1Directory struct {
	NewReg    string `json:"new-reg"`
	NewAuthz  string `json:"new-authz"`
	NewCert   string `json:"new-cert"`
	RevokeURL string `json:"revoke-cert"`
}

type acmeV1Identifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type acmeV1Problem struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

type acmeV1Challenge struct {
	Type   string         `json:"type"`
	URI    string         `json:"uri"`
	Token  string         `json:"token"`
	Status string         `json:"status"`
	Error  *acmeV1Problem `json:"error,omitempty"`
}

type acmeV1Authorization struct {
	Status     string            `json:"status"`
	Identifier acmeV1Identifier  `json:"identifier"`
	Challenges []acmeV1Challenge `json:"challenges"`
}

type acmeV1Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// acmeV1Client 是 ACME v1 的最小客户端实现，仅用于证书签发。
// v1 已不再是主流协议，但部分内部 CA 仍然只提供旧目录接口。
type acmeV1Client struct {
	directoryURL string
	key          crypto.Signer
	jwk          jose.JSONWebKey
	http         *http.Client
	nonces       []string
}

func newACMEV1Client(directoryURL string, key crypto.PrivateKey) (*acmeV1Client, error) {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, i18n.E("service.acme.v1_account_key_invalid")
	}
	return &acmeV1Client{
		directoryURL: strings.TrimSpace(directoryURL),
		key:          signer,
		jwk:          jose.JSONWebKey{Key: signer.Public()},
		http:         &http.Client{Timeout: acmeV1HTTPTime},
	}, nil
}

// obtainV1 使用旧版 ACME v1 协议完成注册、DNS-01 验证与证书签发。
func (m *Manager) obtainV1(ca CADef, user *acmeUser, req ACMERequest, provider challenge.Provider, keyType certcrypto.KeyType) (*ACMEResult, error) {
	if ca.EAB {
		return nil, i18n.E("service.acme.v1_eab_unsupported")
	}
	client, err := newACMEV1Client(ca.URL, user.key)
	if err != nil {
		return nil, err
	}
	directory, err := client.directory()
	if err != nil {
		return nil, i18n.E("service.acme.v1_directory_failed", "error", err.Error())
	}
	_, newAuthzURL, err := client.register(directory, req.Email)
	if err != nil {
		return nil, i18n.E("service.acme.v1_register_failed", "error", err.Error())
	}

	authzURLs := make([]string, 0, len(req.Domains))
	newCertURL := directory.NewCert
	for _, domain := range req.Domains {
		authzURL, next, err := client.authorize(directory, newAuthzURL, domain, provider)
		if err != nil {
			return nil, err
		}
		authzURLs = append(authzURLs, authzURL)
		if next != "" {
			newCertURL = next
		}
	}

	certPEM, issuerPEM, keyPEM, leaf, err := client.obtainCertificate(newCertURL, req.Domains, authzURLs, keyType)
	if err != nil {
		return nil, i18n.E("service.acme.v1_certificate_failed", "error", err.Error())
	}

	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	fullchain := strings.TrimRight(string(certPEM), "\n")
	if len(issuerPEM) > 0 {
		fullchain += "\n" + strings.TrimRight(string(issuerPEM), "\n")
	}
	return &ACMEResult{
		CertPEM:      string(certPEM),
		KeyPEM:       string(keyPEM),
		FullchainPEM: fullchain + "\n",
		Issuer:       issuer,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}, nil
}

func (c *acmeV1Client) directory() (acmeV1Directory, error) {
	var directory acmeV1Directory
	req, err := http.NewRequest(http.MethodGet, c.directoryURL, nil)
	if err != nil {
		return directory, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return directory, err
	}
	defer resp.Body.Close()
	c.rememberNonce(resp.Header)
	body, err := io.ReadAll(io.LimitReader(resp.Body, acmeV1BodyLimit))
	if err != nil {
		return directory, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return directory, responseError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &directory); err != nil {
		return directory, err
	}
	if directory.NewReg == "" || directory.NewAuthz == "" || directory.NewCert == "" {
		return directory, errors.New("directory does not expose ACME v1 endpoints")
	}
	return directory, nil
}

func (c *acmeV1Client) register(directory acmeV1Directory, email string) (string, string, error) {
	payload := map[string]any{"resource": "new-reg"}
	if strings.TrimSpace(email) != "" {
		payload["contact"] = []string{"mailto:" + strings.TrimSpace(email)}
	}
	resp, err := c.post(directory.NewReg, payload)
	if err != nil {
		return "", "", err
	}
	regURL := resp.Header.Get("Location")
	if resp.Status == http.StatusConflict && regURL != "" {
		// 同一账户密钥重复注册时返回 409，直接复用已有注册地址。
		resp, err = c.post(regURL, map[string]any{"resource": "reg"})
		if err != nil {
			return "", "", err
		}
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return "", "", responseError(resp.Status, resp.Body)
	}
	links := parseLinkHeader(resp.Header.Values("Link"))
	if tos := links["terms-of-service"]; tos != "" && regURL != "" {
		agreement, err := c.post(regURL, map[string]any{"resource": "reg", "agreement": tos})
		if err != nil {
			return "", "", err
		}
		if agreement.Status < 200 || agreement.Status >= 300 {
			return "", "", responseError(agreement.Status, agreement.Body)
		}
	}
	newAuthzURL := links["next"]
	if newAuthzURL == "" {
		newAuthzURL = directory.NewAuthz
	}
	return regURL, newAuthzURL, nil
}

func (c *acmeV1Client) authorize(directory acmeV1Directory, newAuthzURL, domain string, provider challenge.Provider) (string, string, error) {
	identifier := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "*.")
	resp, err := c.post(newAuthzURL, map[string]any{
		"resource":   "new-authz",
		"identifier": acmeV1Identifier{Type: "dns", Value: identifier},
	})
	if err != nil {
		return "", "", i18n.E("service.acme.v1_authorization_failed", "domain", domain, "error", err.Error())
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return "", "", i18n.E("service.acme.v1_authorization_failed", "domain", domain, "error", responseError(resp.Status, resp.Body).Error())
	}
	var authz acmeV1Authorization
	if err := json.Unmarshal(resp.Body, &authz); err != nil {
		return "", "", i18n.E("service.acme.v1_authorization_failed", "domain", domain, "error", err.Error())
	}
	authzURL := resp.Header.Get("Location")
	links := parseLinkHeader(resp.Header.Values("Link"))
	next := links["next"]
	if next == "" {
		next = directory.NewCert
	}
	if authz.Status == "valid" {
		return authzURL, next, nil
	}
	var selected *acmeV1Challenge
	for index := range authz.Challenges {
		if strings.EqualFold(authz.Challenges[index].Type, "dns-01") {
			selected = &authz.Challenges[index]
			break
		}
	}
	if selected == nil {
		return "", "", i18n.E("service.acme.v1_dns01_missing", "domain", domain)
	}

	keyAuth, err := c.keyAuthorization(selected.Token)
	if err != nil {
		return "", "", err
	}
	if err := provider.Present(domain, selected.Token, keyAuth); err != nil {
		return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", err.Error())
	}
	defer func() { _ = provider.CleanUp(domain, selected.Token, keyAuth) }()

	c.waitForDNS(identifier, keyAuth)
	resp, err = c.post(selected.URI, map[string]any{
		"resource":         "challenge",
		"type":             selected.Type,
		"token":            selected.Token,
		"keyAuthorization": keyAuth,
	})
	if err != nil {
		return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", err.Error())
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", responseError(resp.Status, resp.Body).Error())
	}
	var challenge acmeV1Challenge
	if err := json.Unmarshal(resp.Body, &challenge); err != nil {
		return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", err.Error())
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		switch strings.ToLower(challenge.Status) {
		case "valid":
			return authzURL, next, nil
		case "invalid":
			detail := "challenge marked invalid"
			if challenge.Error != nil && challenge.Error.Detail != "" {
				detail = challenge.Error.Detail
			}
			return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", detail)
		}
		if time.Now().After(deadline) {
			return "", "", i18n.E("service.acme.v1_challenge_timeout", "domain", domain)
		}
		time.Sleep(2 * time.Second)
		poll, err := c.get(selected.URI)
		if err != nil {
			return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", err.Error())
		}
		if poll.Status < 200 || poll.Status >= 300 {
			return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", responseError(poll.Status, poll.Body).Error())
		}
		if err := json.Unmarshal(poll.Body, &challenge); err != nil {
			return "", "", i18n.E("service.acme.v1_challenge_failed", "domain", domain, "error", err.Error())
		}
	}
}

// waitForDNS 在最长 2 分钟内检查 _acme-challenge TXT 是否生效；
// 检查失败时仍然继续提交挑战，兼容递归 DNS 尚未刷新但权威 DNS 已生效的情况。
func (c *acmeV1Client) waitForDNS(domain, keyAuth string) {
	if acmeV1DNSCheckTimeout <= 0 {
		return
	}
	sum := sha256.Sum256([]byte(keyAuth))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	name := "_acme-challenge." + strings.TrimSuffix(domain, ".") + "."
	resolver := net.DefaultResolver
	deadline := time.Now().Add(acmeV1DNSCheckTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		values, err := resolver.LookupTXT(ctx, name)
		cancel()
		if err == nil {
			for _, value := range values {
				if value == expected {
					return
				}
			}
		}
		time.Sleep(acmeV1DNSRetry)
	}
}

func (c *acmeV1Client) obtainCertificate(newCertURL string, domains, authzURLs []string, keyType certcrypto.KeyType) ([]byte, []byte, []byte, *x509.Certificate, error) {
	certKey, err := certcrypto.GeneratePrivateKey(keyType)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(domains) == 0 {
		return nil, nil, nil, nil, errors.New("no domains")
	}
	template := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: append([]string(nil), domains...),
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, certKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// 与旧版客户端保持一致：第一个域名是 CN，其余域名的授权地址写入 authorizations。
	authorizations := make([]string, 0, len(authzURLs))
	if len(authzURLs) > 1 {
		authorizations = append(authorizations, authzURLs[1:]...)
	}
	resp, err := c.post(newCertURL, map[string]any{
		"resource":       "new-cert",
		"csr":            base64.RawURLEncoding.EncodeToString(csrDER),
		"authorizations": authorizations,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	keyPEM := certcrypto.PEMEncode(certKey)
	certURL := newCertURL
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if resp.Status == http.StatusCreated || resp.Status == http.StatusOK {
			if len(resp.Body) == 0 {
				return nil, nil, nil, nil, errors.New("empty certificate response")
			}
			leafDER, err := certificateDER(resp.Body)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			leaf, err := x509.ParseCertificate(leafDER)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
			issuerPEM := []byte(nil)
			links := parseLinkHeader(resp.Header.Values("Link"))
			if up := links["up"]; up != "" {
				issuerPEM, _ = c.fetchIssuer(up)
			}
			return leafPEM, issuerPEM, keyPEM, leaf, nil
		}
		if resp.Status != http.StatusAccepted {
			return nil, nil, nil, nil, responseError(resp.Status, resp.Body)
		}
		if time.Now().After(deadline) {
			return nil, nil, nil, nil, errors.New("timed out waiting for certificate")
		}
		wait := 2 * time.Second
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 && seconds <= 60 {
				wait = time.Duration(seconds) * time.Second
			}
		}
		time.Sleep(wait)
		if location := resp.Header.Get("Location"); location != "" {
			certURL = location
		}
		resp, err = c.get(certURL)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
}

func (c *acmeV1Client) fetchIssuer(url string) ([]byte, error) {
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, responseError(resp.Status, resp.Body)
	}
	der, err := certificateDER(resp.Body)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func certificateDER(data []byte) ([]byte, error) {
	if block, _ := pem.Decode(data); block != nil && block.Type == "CERTIFICATE" {
		return block.Bytes, nil
	}
	if _, err := x509.ParseCertificate(data); err == nil {
		return data, nil
	}
	return nil, errors.New("response does not contain a certificate")
}

func (c *acmeV1Client) keyAuthorization(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", errors.New("empty challenge token")
	}
	sum, err := c.jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return token + "." + base64.RawURLEncoding.EncodeToString(sum), nil
}

// post 签名并提交 JWS。返回结构包含状态码，便于处理注册冲突等非 2xx 响应。
// 服务端提示 badNonce 时使用新 nonce 重试一次。
func (c *acmeV1Client) post(url string, payload any) (*acmeV1Response, error) {
	resp, err := c.postOnce(url, payload)
	if err != nil {
		return nil, err
	}
	if resp.Status == http.StatusBadRequest && bytes.Contains(resp.Body, []byte("badNonce")) {
		return c.postOnce(url, payload)
	}
	return resp, nil
}

func (c *acmeV1Client) postOnce(url string, payload any) (*acmeV1Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	nonce, err := c.nonce()
	if err != nil {
		return nil, err
	}
	alg, err := jwsAlgorithm(c.key)
	if err != nil {
		return nil, err
	}
	opts := (&jose.SignerOptions{}).WithHeader("nonce", nonce).WithHeader("url", url).WithHeader("jwk", c.jwk)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: c.key}, opts)
	if err != nil {
		return nil, err
	}
	signed, err := signer.Sign(body)
	if err != nil {
		return nil, err
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(compact))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/jose+json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.rememberNonce(resp.Header)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, acmeV1BodyLimit))
	if err != nil {
		return nil, err
	}
	return &acmeV1Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: raw}, nil
}

func (c *acmeV1Client) get(url string) (*acmeV1Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.rememberNonce(resp.Header)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, acmeV1BodyLimit))
	if err != nil {
		return nil, err
	}
	return &acmeV1Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: raw}, nil
}

func (c *acmeV1Client) rememberNonce(header http.Header) {
	if nonce := strings.TrimSpace(header.Get("Replay-Nonce")); nonce != "" {
		c.nonces = append(c.nonces[:0], nonce)
	}
}

func (c *acmeV1Client) nonce() (string, error) {
	if len(c.nonces) > 0 {
		nonce := c.nonces[len(c.nonces)-1]
		c.nonces = c.nonces[:0]
		return nonce, nil
	}
	req, err := http.NewRequest(http.MethodHead, c.directoryURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	nonce := strings.TrimSpace(resp.Header.Get("Replay-Nonce"))
	if nonce == "" {
		req, err = http.NewRequest(http.MethodGet, c.directoryURL, nil)
		if err != nil {
			return "", err
		}
		resp, err = c.http.Do(req)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		nonce = strings.TrimSpace(resp.Header.Get("Replay-Nonce"))
	}
	if nonce == "" {
		return "", errors.New("server did not provide a Replay-Nonce header")
	}
	return nonce, nil
}

func jwsAlgorithm(key crypto.Signer) (jose.SignatureAlgorithm, error) {
	switch pub := key.Public().(type) {
	case *rsa.PublicKey:
		return jose.RS256, nil
	case *ecdsa.PublicKey:
		switch pub.Curve {
		case elliptic.P256():
			return jose.ES256, nil
		case elliptic.P384():
			return jose.ES384, nil
		case elliptic.P521():
			return jose.ES512, nil
		}
	}
	return "", errors.New("unsupported account key type")
}

var linkPattern = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?([^";]+)"?`)

func parseLinkHeader(values []string) map[string]string {
	links := map[string]string{}
	for _, value := range values {
		for _, match := range linkPattern.FindAllStringSubmatch(value, -1) {
			if len(match) == 3 {
				links[strings.ToLower(strings.TrimSpace(match[2]))] = strings.TrimSpace(match[1])
			}
		}
	}
	return links
}

func responseError(status int, body []byte) error {
	var problem acmeV1Problem
	if json.Unmarshal(body, &problem) == nil {
		if problem.Detail != "" {
			return fmt.Errorf("%s: %s", problem.Type, problem.Detail)
		}
		if problem.Type != "" {
			return errors.New(problem.Type)
		}
	}
	text := strings.TrimSpace(string(bytes.TrimSpace(body)))
	if len(text) > 300 {
		text = text[:300]
	}
	if text == "" {
		text = http.StatusText(status)
	}
	return fmt.Errorf("HTTP %d: %s", status, text)
}
