package service

import (
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/acme"
	acmeapi "github.com/go-acme/lego/v4/acme/api"
	"github.com/go-acme/lego/v4/certcrypto"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

const (
	// 零信验证记录由服务端 tokenPath 决定，等待用户手动添加。
	zotrusPollInterval = 5 * time.Second
	zotrusPollTimeout  = 5 * time.Minute
	// 超过该时间的待验证订单视为已过期；重新运行任务会生成新订单。
	zotrusOrderMaxAge = 7 * 24 * time.Hour
	zotrusUserAgent   = "LACTSSL/1.0"
)

// ZotrusPendingError 表示订单已创建，任务需要暂停等待用户添加 DNS 记录。
type ZotrusPendingError struct {
	Order *model.ZotrusOrder
}

func (e *ZotrusPendingError) Error() string {
	return "zero trust order is waiting for manual DNS validation"
}

func (e *ZotrusPendingError) LocalizedMessage() (string, map[string]any) {
	return "task.message.waiting_dns", nil
}

// ZotrusStillPendingError 表示用户点击继续后，零信在本次轮询窗口内仍未完成验证。
// 任务保持等待状态，用户可以检查记录后再次点击继续。
type ZotrusStillPendingError struct {
	OrderID int64
}

func (e *ZotrusStillPendingError) Error() string {
	return "zero trust order is still pending DNS validation"
}

func (e *ZotrusStillPendingError) LocalizedMessage() (string, map[string]any) {
	return "service.zotrus.still_pending", nil
}

// GetZotrusOrder 返回任务当前的零信订单（不包含私钥与 CSR）。
func (m *Manager) GetZotrusOrder(taskID int64) (*model.ZotrusOrder, error) {
	order, err := m.Store.GetZotrusOrderByTask(taskID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, i18n.E("service.zotrus.order_missing")
	}
	return order, nil
}

// ResetZotrusOrder 删除任务当前的零信订单，供“重新生成验证记录”使用。
func (m *Manager) ResetZotrusOrder(taskID int64) error {
	if taskID <= 0 {
		return i18n.E("task.error.not_found")
	}
	return m.Store.DeleteZotrusOrderByTask(taskID)
}

// ResetStaleZotrusOrder 在任务域名或私钥类型变化后丢弃旧订单，
// 避免用户继续验证一张与当前任务不匹配的证书。
func (m *Manager) ResetStaleZotrusOrder(task *model.Task) bool {
	if task == nil || task.CAPoolID != 0 || strings.TrimSpace(task.CA) != "zotrus" {
		return false
	}
	order, err := m.Store.GetZotrusOrderByTask(task.ID)
	if err != nil || order == nil {
		return false
	}
	if !sameDomainSet(order.Domains, task.Domains) || order.KeyType != string(certificateKeyType(task.KeyType)) {
		_ = m.Store.DeleteZotrusOrder(order.ID)
		return true
	}
	return false
}

// createZotrusOrder 创建零信标准 ACME 订单，并提取每条 dns-01 挑战的
// tokenPath / tokenType。返回的订单已持久化，任务可以安全暂停。
func (m *Manager) createZotrusOrder(task *model.Task, ca CADef, domains []string, keyType certcrypto.KeyType, email, eabKid, eabHMAC string) (*model.ZotrusOrder, error) {
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if strings.HasPrefix(domain, "*.") {
			return nil, i18n.E("service.zotrus.wildcard_unsupported")
		}
		if net.ParseIP(domain) != nil {
			return nil, i18n.E("service.zotrus.ip_unsupported")
		}
	}

	// 同一任务重复运行时复用尚未过期的订单，避免不断产生 pending 订单。
	if existing, err := m.Store.GetZotrusOrderByTask(task.ID); err != nil {
		return nil, err
	} else if existing != nil {
		reusable := (existing.Status == model.ZotrusOrderPending || existing.Status == model.ZotrusOrderVerifying) &&
			time.Since(existing.CreatedAt) < zotrusOrderMaxAge &&
			sameDomainSet(existing.Domains, domains) &&
			existing.KeyType == string(keyType)
		if reusable {
			return existing, nil
		}
		if err := m.Store.DeleteZotrusOrder(existing.ID); err != nil {
			return nil, err
		}
	}

	// 先通过 lego 完成账户注册，拿到 account URI 与账户私钥。
	_, user, err := m.newACMEClient(ca, email, certcrypto.EC256, nil, eabKid, eabHMAC)
	if err != nil {
		return nil, err
	}
	if user == nil || user.reg == nil || strings.TrimSpace(user.reg.URI) == "" {
		return nil, i18n.E("service.zotrus.account_missing")
	}

	core, transport, err := newZotrusCore(ca, keyType, user.key, user.reg.URI)
	if err != nil {
		return nil, err
	}

	order, err := core.Orders.New(domains)
	if err != nil {
		return nil, i18n.Wrap("service.zotrus.order_create_failed", err, "error", err.Error())
	}
	orderURL := zotrusAbsoluteURL(core.GetDirectory().NewOrderURL, order.Location)
	if orderURL == "" {
		return nil, i18n.E("service.zotrus.order_url_missing")
	}

	finalizeURL := zotrusAbsoluteURL(orderURL, order.Finalize)
	if finalizeURL == "" {
		finalizeURL = strings.TrimRight(orderURL, "/") + "/finalize"
	}

	challenges := make([]model.ZotrusChallenge, 0, len(order.Authorizations))
	for _, rawAuthzURL := range order.Authorizations {
		authzURL := zotrusAbsoluteURL(orderURL, rawAuthzURL)
		challenge, err := m.zotrusChallengeForAuthz(core, transport, authzURL, domains)
		if err != nil {
			return nil, err
		}
		challenges = append(challenges, challenge)
	}
	if len(challenges) == 0 {
		return nil, i18n.E("service.zotrus.challenge_missing")
	}

	privateKey, err := certcrypto.GeneratePrivateKey(keyType)
	if err != nil {
		return nil, i18n.Wrap("service.zotrus.csr_failed", err, "error", err.Error())
	}
	csrDER, err := certcrypto.CreateCSR(privateKey, zotrusCSROptions(domains))
	if err != nil {
		return nil, i18n.Wrap("service.zotrus.csr_failed", err, "error", err.Error())
	}

	pending := &model.ZotrusOrder{
		TaskID:      task.ID,
		Domains:     append([]string(nil), domains...),
		KeyType:     string(keyType),
		OrderURL:    orderURL,
		AccountURI:  user.reg.URI,
		FinalizeURL: finalizeURL,
		KeyPEM:      string(certcrypto.PEMEncode(privateKey)),
		CSRDER:      base64.RawURLEncoding.EncodeToString(csrDER),
		Challenges:  challenges,
		Status:      model.ZotrusOrderPending,
	}
	if err := m.Store.CreateZotrusOrder(pending); err != nil {
		return nil, i18n.Wrap("service.zotrus.order_save_failed", err, "error", err.Error())
	}
	logger().Info("零信订单已创建", "task_id", task.ID, "order_id", pending.ID, "order_url", orderURL, "challenges", len(challenges))
	return pending, nil
}

// completeZotrusOrder 在用户确认已添加 DNS 记录后继续订单：
// 提交挑战、等待订单进入 ready/valid、finalize 并下载证书。
func (m *Manager) completeZotrusOrder(ca CADef, keyType certcrypto.KeyType, email string, order *model.ZotrusOrder) (*ACMEResult, error) {
	if order == nil {
		return nil, i18n.E("service.zotrus.order_missing")
	}
	user, err := m.loadACMEUser(ca.Value, email)
	if err != nil {
		return nil, err
	}
	accountURI := strings.TrimSpace(order.AccountURI)
	if accountURI == "" {
		return nil, i18n.E("service.zotrus.account_missing")
	}

	core, _, err := newZotrusCore(ca, keyType, user.key, accountURI)
	if err != nil {
		return nil, err
	}

	if err := m.Store.UpdateZotrusOrderStatus(order.ID, model.ZotrusOrderVerifying, order.CertificateURL, ""); err != nil {
		return nil, err
	}

	// 提交尚未完成的挑战。重复提交可能返回 authorization must be pending，
	// 这类错误不阻断后续轮询，真正的结论以订单状态为准。
	for index := range order.Challenges {
		challenge := &order.Challenges[index]
		if challenge.Status == acme.StatusValid || strings.TrimSpace(challenge.ChallengeURL) == "" {
			continue
		}
		if _, challengeErr := core.Challenges.New(challenge.ChallengeURL); challengeErr != nil {
			var problem *acme.ProblemDetails
			if errors.As(challengeErr, &problem) {
				switch problem.Type {
				case "urn:ietf:params:acme:error:accountDoesNotExist", "urn:ietf:params:acme:error:unauthorized":
					_ = m.Store.UpdateZotrusOrderStatus(order.ID, model.ZotrusOrderFailed, "", problem.Error())
					return nil, i18n.Wrap("service.zotrus.challenge_failed", challengeErr, "error", problem.Error())
				}
			}
			logger().Warn("零信挑战提交失败", "order_id", order.ID, "domain", challenge.Domain, "error", SafeError(challengeErr))
		}
	}

	csrDER, err := base64.RawURLEncoding.DecodeString(order.CSRDER)
	if err != nil || len(csrDER) == 0 {
		return nil, i18n.E("service.zotrus.csr_invalid")
	}

	deadline := time.Now().Add(zotrusPollTimeout)
	var (
		finalized      bool
		lastFinalizeAt time.Time
		lastStatus     string
	)
	for {
		current, err := core.Orders.Get(order.OrderURL)
		if err != nil {
			return nil, i18n.Wrap("service.zotrus.order_poll_failed", err, "error", err.Error())
		}
		if current.Status != lastStatus {
			lastStatus = current.Status
			logger().Info("零信订单状态变化", "order_id", order.ID, "status", current.Status)
		}

		switch current.Status {
		case acme.StatusValid:
			certURL := strings.TrimSpace(current.Certificate)
			if certURL == "" {
				certURL = strings.TrimSpace(order.CertificateURL)
			}
			return m.downloadZotrusCertificate(core, order, certURL)
		case acme.StatusInvalid:
			message := zotrusProblemText(current.Error)
			if message == "" {
				message = current.Status
			}
			_ = m.Store.UpdateZotrusOrderStatus(order.ID, model.ZotrusOrderFailed, "", message)
			return nil, i18n.E("service.zotrus.order_invalid", "error", message)
		case acme.StatusReady:
			if !finalized && time.Since(lastFinalizeAt) >= 15*time.Second {
				lastFinalizeAt = time.Now()
				if err := m.finalizeZotrusOrder(core, order, csrDER); err != nil {
					if !errors.Is(err, errZotrusOrderNotReady) {
						return nil, err
					}
					logger().Warn("零信订单尚未就绪，稍后重试 finalize", "order_id", order.ID)
				} else {
					finalized = true
				}
			}
		case acme.StatusPending:
			allValid, authzErr := m.refreshZotrusAuthorizations(core, order)
			if authzErr != nil {
				return nil, authzErr
			}
			if allValid && !finalized && time.Since(lastFinalizeAt) >= 15*time.Second {
				lastFinalizeAt = time.Now()
				if err := m.finalizeZotrusOrder(core, order, csrDER); err != nil {
					if !errors.Is(err, errZotrusOrderNotReady) {
						return nil, err
					}
					logger().Warn("零信订单尚未就绪，稍后重试 finalize", "order_id", order.ID)
				} else {
					finalized = true
				}
			}
		case acme.StatusProcessing:
			// 正在验证或签发，继续轮询。
		default:
			return nil, i18n.E("service.zotrus.order_status_unexpected", "status", current.Status)
		}

		if time.Now().After(deadline) {
			_ = m.Store.UpdateZotrusOrderChallenges(order.ID, order.Challenges, model.ZotrusOrderPending, "")
			return nil, &ZotrusStillPendingError{OrderID: order.ID}
		}
		time.Sleep(zotrusPollInterval)
	}
}

var errZotrusOrderNotReady = errors.New("zotrus order is not ready")

func (m *Manager) finalizeZotrusOrder(core *acmeapi.Core, order *model.ZotrusOrder, csrDER []byte) error {
	finalizeURL := strings.TrimSpace(order.FinalizeURL)
	if finalizeURL == "" {
		return i18n.E("service.zotrus.finalize_missing")
	}
	_, err := core.Orders.UpdateForCSR(finalizeURL, csrDER)
	if err == nil {
		return nil
	}
	var problem *acme.ProblemDetails
	if errors.As(err, &problem) {
		if strings.Contains(strings.ToLower(problem.Type), "ordernotready") ||
			strings.Contains(strings.ToLower(problem.Detail), "not ready") {
			return errZotrusOrderNotReady
		}
	}
	return i18n.Wrap("service.zotrus.finalize_failed", err, "error", err.Error())
}

// refreshZotrusAuthorizations 检查 authorization 状态；零信的授权状态可能
// 不按 RFC 更新，因此这里只把 valid 视为“可尝试 finalize”，不把 pending 当失败。
func (m *Manager) refreshZotrusAuthorizations(core *acmeapi.Core, order *model.ZotrusOrder) (bool, error) {
	allValid := true
	for index := range order.Challenges {
		challenge := &order.Challenges[index]
		if challenge.Status == acme.StatusValid {
			continue
		}
		if strings.TrimSpace(challenge.AuthorizationURL) == "" {
			allValid = false
			continue
		}
		authz, err := core.Authorizations.Get(challenge.AuthorizationURL)
		if err != nil {
			allValid = false
			continue
		}
		switch authz.Status {
		case acme.StatusValid:
			challenge.Status = acme.StatusValid
		case acme.StatusInvalid, acme.StatusDeactivated, acme.StatusExpired, acme.StatusRevoked:
			message := ""
			for _, item := range authz.Challenges {
				if item.Status == acme.StatusInvalid && item.Error != nil {
					message = zotrusProblemText(item.Error)
					break
				}
			}
			if message == "" {
				message = authz.Status
			}
			challenge.Status = acme.StatusInvalid
			challenge.Error = message
			_ = m.Store.UpdateZotrusOrderChallenges(order.ID, order.Challenges, model.ZotrusOrderFailed, message)
			return false, i18n.E("service.zotrus.authorization_invalid", "domain", challenge.Domain, "error", message)
		default:
			allValid = false
		}
	}
	_ = m.Store.UpdateZotrusOrderChallenges(order.ID, order.Challenges, model.ZotrusOrderVerifying, "")
	return allValid, nil
}

func (m *Manager) downloadZotrusCertificate(core *acmeapi.Core, order *model.ZotrusOrder, certURL string) (*ACMEResult, error) {
	certURL = zotrusAbsoluteURL(order.OrderURL, certURL)
	if certURL == "" {
		return nil, i18n.E("service.zotrus.certificate_missing")
	}
	bundle, issuer, err := core.Certificates.Get(certURL, true)
	if err != nil {
		return nil, i18n.Wrap("service.zotrus.certificate_download_failed", err, "error", err.Error())
	}
	leaf, err := pickLeaf(bundle, order.Domains)
	if err != nil {
		return nil, err
	}
	fullchain, err := BuildFullchainPEM(leaf, bundle, issuer)
	if err != nil {
		return nil, i18n.Wrap("service.acme.chain_parse_failed", err, "error", err.Error())
	}
	issuerName := leaf.Issuer.CommonName
	if issuerName == "" && len(leaf.Issuer.Organization) > 0 {
		issuerName = leaf.Issuer.Organization[0]
	}
	result := &ACMEResult{
		CertPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})),
		KeyPEM:       order.KeyPEM,
		FullchainPEM: fullchain,
		Issuer:       issuerName,
		Serial:       strings.ToUpper(leaf.SerialNumber.Text(16)),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
	}
	if err := validateACMEResult(order.Domains, result); err != nil {
		return nil, err
	}
	_ = m.Store.UpdateZotrusOrderStatus(order.ID, model.ZotrusOrderIssued, certURL, "")
	return result, nil
}

// zotrusChallengeForAuthz 从 authorization 原文中读取 lego 不认识的
// tokenPath / tokenType，并转换成用户可添加的 DNS 记录。
func (m *Manager) zotrusChallengeForAuthz(core *acmeapi.Core, transport *upLinkTransport, authzURL string, domains []string) (model.ZotrusChallenge, error) {
	authz, err := core.Authorizations.Get(authzURL)
	if err != nil {
		return model.ZotrusChallenge{}, i18n.Wrap("service.zotrus.authorization_get_failed", err, "error", err.Error())
	}

	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(authz.Identifier.Value), ".")), "*.")
	if domain == "" {
		return model.ZotrusChallenge{}, i18n.E("service.zotrus.authz_domain_missing")
	}
	if authz.Status == acme.StatusValid {
		// 零信可能复用此前已完成的授权；此时不需要用户再添加记录。
		return model.ZotrusChallenge{
			Domain:           domain,
			AuthorizationURL: authzURL,
			Status:           acme.StatusValid,
		}, nil
	}
	if !sameDomainSet([]string{domain}, domains) && !matchesRequestedDomain(domain, domains) {
		// authorization 的 identifier 一般是证书域名之一；这里只做告警级别校验，
		// 避免上游返回别名导致整个任务失败。
		logger().Warn("零信 authorization 域名不在请求列表中", "authz_domain", domain, "domains", strings.Join(domains, ","))
	}

	challengeType := ""
	challengeURL := ""
	token := ""
	tokenPath := ""
	tokenType := ""
	status := ""
	message := ""
	raw := transport.AuthorizationPayload(authzURL)
	if len(raw) > 0 {
		if payload, ok := parseZotrusAuthorization(raw); ok {
			if value := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(payload.Identifier.Value), ".")), "*."); value != "" {
				domain = value
			}
			for _, item := range payload.Challenges {
				if !strings.EqualFold(item.Type, "dns-01") {
					continue
				}
				challengeType = item.Type
				challengeURL = item.URL
				token = item.Token
				tokenPath = item.TokenPath
				tokenType = item.TokenType
				status = item.Status
				message = zotrusProblemText(item.Error)
				break
			}
		}
	}
	if challengeType == "" {
		for _, item := range authz.Challenges {
			if !strings.EqualFold(item.Type, "dns-01") {
				continue
			}
			challengeType = item.Type
			challengeURL = item.URL
			token = item.Token
			status = item.Status
			break
		}
	}
	if challengeType == "" {
		return model.ZotrusChallenge{}, i18n.E("service.zotrus.dns_challenge_missing", "domain", domain)
	}
	challengeURL = zotrusAbsoluteURL(authzURL, challengeURL)
	if strings.TrimSpace(token) == "" {
		return model.ZotrusChallenge{}, i18n.E("service.zotrus.token_missing", "domain", domain)
	}

	recordName, err := zotrusDNSRecordName(domain, tokenPath)
	if err != nil {
		return model.ZotrusChallenge{}, err
	}
	recordType, err := zotrusDNSRecordType(tokenType)
	if err != nil {
		return model.ZotrusChallenge{}, err
	}
	recordValue := strings.TrimSpace(token)
	if recordType == "TXT" && strings.TrimSpace(tokenPath) == "" {
		// 兼容标准 ACME 服务端：没有 tokenPath 时按 RFC 8555 写
		// `_acme-challenge.<domain>`，值为 SHA-256(keyAuth) 的 Base64URL。
		if keyAuth, keyErr := core.GetKeyAuthorization(token); keyErr == nil {
			sum := sha256.Sum256([]byte(keyAuth))
			recordValue = base64.RawURLEncoding.EncodeToString(sum[:])
		}
	}

	return model.ZotrusChallenge{
		Domain:           domain,
		AuthorizationURL: authzURL,
		ChallengeURL:     challengeURL,
		Token:            token,
		TokenPath:        tokenPath,
		TokenType:        tokenType,
		RecordName:       recordName,
		RecordType:       recordType,
		RecordValue:      recordValue,
		Status:           firstNonEmpty(status, acme.StatusPending),
		Error:            message,
	}, nil
}

type zotrusAuthorizationPayload struct {
	Status     string `json:"status"`
	Identifier struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"identifier"`
	Challenges []struct {
		Type      string               `json:"type"`
		URL       string               `json:"url"`
		Status    string               `json:"status"`
		Token     string               `json:"token"`
		TokenPath string               `json:"tokenPath"`
		TokenType string               `json:"tokenType"`
		Error     *acme.ProblemDetails `json:"error,omitempty"`
	} `json:"challenges"`
}

func zotrusAbsoluteURL(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if parsed.IsAbs() {
		return ref
	}
	baseURL, err := url.Parse(strings.TrimSpace(base))
	if err != nil || baseURL == nil {
		return ref
	}
	return baseURL.ResolveReference(parsed).String()
}

func parseZotrusAuthorization(raw []byte) (zotrusAuthorizationPayload, bool) {
	var payload zotrusAuthorizationPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return zotrusAuthorizationPayload{}, false
	}
	if len(payload.Challenges) == 0 {
		return zotrusAuthorizationPayload{}, false
	}
	return payload, true
}

func newZotrusCore(ca CADef, keyType certcrypto.KeyType, accountKey crypto.PrivateKey, accountURI string) (*acmeapi.Core, *upLinkTransport, error) {
	httpClient, transport := newACMECompatibilityClient(&http.Client{Timeout: 60 * time.Second})
	core, err := acmeapi.New(httpClient, zotrusUserAgent, caDirectoryURL(ca, keyType), strings.TrimSpace(accountURI), accountKey)
	if err != nil {
		return nil, nil, i18n.Wrap("service.zotrus.client_init_failed", err, "error", err.Error())
	}
	return core, transport, nil
}

func zotrusCSROptions(domains []string) certcrypto.CSROptions {
	if len(domains) == 0 {
		return certcrypto.CSROptions{}
	}
	commonName := ""
	if len(domains[0]) <= 64 {
		commonName = domains[0]
	}
	san := make([]string, 0, len(domains))
	if commonName != "" {
		san = append(san, commonName)
	}
	for _, domain := range domains {
		if commonName != "" && strings.EqualFold(domain, commonName) {
			continue
		}
		san = append(san, domain)
	}
	return certcrypto.CSROptions{Domain: commonName, SAN: san}
}

func zotrusDNSRecordName(domain, tokenPath string) (string, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return "", i18n.E("service.zotrus.authz_domain_missing")
	}
	tokenPath = strings.TrimSpace(tokenPath)
	if tokenPath == "" {
		return "_acme-challenge." + domain, nil
	}
	if strings.Contains(tokenPath, "/") {
		return "", i18n.E("service.zotrus.token_path_invalid", "path", tokenPath)
	}
	full := strings.TrimSuffix(tokenPath, ".")
	label := strings.TrimPrefix(full, "_")
	lower := strings.ToLower(label)
	if lower == domain || strings.HasSuffix(lower, "."+domain) {
		return full, nil
	}
	return "_" + label + "." + domain, nil
}

func zotrusDNSRecordType(value string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "TXT":
		return "TXT", nil
	case "CNAME":
		return "CNAME", nil
	default:
		return "", i18n.E("service.zotrus.record_type_invalid", "type", value)
	}
}

func zotrusProblemText(problem *acme.ProblemDetails) string {
	if problem == nil {
		return ""
	}
	if strings.TrimSpace(problem.Detail) != "" {
		return problem.Detail
	}
	return problem.Error()
}

func matchesRequestedDomain(domain string, domains []string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	for _, item := range domains {
		if strings.EqualFold(strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(item), ".")), "*."), domain) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// zotrusSummary 用于日志与错误信息，避免把完整挑战结构打进日志。
func zotrusSummary(order *model.ZotrusOrder) string {
	if order == nil {
		return ""
	}
	parts := make([]string, 0, len(order.Challenges))
	for _, challenge := range order.Challenges {
		parts = append(parts, fmt.Sprintf("%s %s %s", challenge.RecordName, challenge.RecordType, challenge.RecordValue))
	}
	return strings.Join(parts, "; ")
}
