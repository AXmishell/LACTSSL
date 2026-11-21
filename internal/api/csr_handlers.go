package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

type csrRequestBody struct {
	CSR string `json:"csr"`
	CA  string `json:"ca"`
	// PrivateCAID 与 ca=private-ca:<id> 等价，便于客户端直接指定私有 CA。
	PrivateCAID    int64  `json:"private_ca_id"`
	Challenge      string `json:"challenge"`
	DNSAccountID   int64  `json:"dns_account_id"`
	IdempotencyKey string `json:"idempotency_key"`
	WaitSeconds    int    `json:"wait_seconds"`
}

type csrRequestErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type csrRequestView struct {
	ID            string               `json:"id"`
	Status        string               `json:"status"`
	Identifiers   []string             `json:"identifiers"`
	PrimaryDomain string               `json:"primary_domain"`
	CA            string               `json:"ca"`
	Challenge     string               `json:"challenge"`
	CSRHash       string               `json:"csr_sha256"`
	DNSAccountID  int64                `json:"dns_account_id,omitempty"`
	DNSRoutes     []model.DNSRoute     `json:"dns_routes,omitempty"`
	ResolvedBy    string               `json:"resolved_by,omitempty"`
	ResolvedZone  string               `json:"resolved_zone,omitempty"`
	Attempts      int                  `json:"attempts"`
	Retryable     bool                 `json:"retryable"`
	CertificateID int64                `json:"certificate_id,omitempty"`
	Issuer        string               `json:"issuer,omitempty"`
	Serial        string               `json:"serial,omitempty"`
	NotBefore     *time.Time           `json:"not_before,omitempty"`
	NotAfter      *time.Time           `json:"not_after,omitempty"`
	Error         *csrRequestErrorView `json:"error,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	ExpiresAt     *time.Time           `json:"expires_at,omitempty"`
	CompletedAt   *time.Time           `json:"completed_at,omitempty"`
	KeyAvailable  bool                 `json:"key_available"`
}

// createCSRRequest 接受客户端 CSR，创建异步申请。服务端不会生成或保存私钥：
// 公共 CA 申请由服务端完成域名验证并提交 CSR finalize，私有 CA 申请则由
// 服务端使用指定私有 CA 直接签名。
func (s *Server) createCSRRequest(c *gin.Context) {
	var body csrRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if body.WaitSeconds < 0 || body.WaitSeconds > 30 {
		failKey(c, http.StatusBadRequest, "api.error.csr_wait_invalid")
		return
	}
	rateKey := "csr:create:" + s.clientRateKey(c)
	if key := apiKeyFromContext(c); key != nil {
		rateKey = "csr:create:key:" + strconv.FormatInt(key.ID, 10)
	}
	if allowed, wait := s.allow(ratelimit.CSRCreate, rateKey); !allowed {
		rateLimited(c, wait)
		return
	}

	ca := strings.TrimSpace(body.CA)
	privateCAID := body.PrivateCAID
	if privateCAID < 0 {
		failKey(c, http.StatusBadRequest, "api.error.csr_private_ca_id_invalid")
		return
	}
	if privateCAID > 0 {
		if ca != "" {
			if id, ok := service.ParsePrivateCAValue(ca); !ok || id != privateCAID {
				failKey(c, http.StatusBadRequest, "api.error.csr_private_ca_conflict")
				return
			}
		}
		ca = service.PrivateCAValue(privateCAID)
	}
	if ca == "" {
		ca = s.setting("acme.ca", "letsencrypt")
	}
	if id, isPrivate := service.ParsePrivateCAValue(ca); isPrivate {
		privateCAID = id
	} else {
		privateCAID = 0
	}
	isPrivateCA := privateCAID > 0
	if isPrivateCA && !s.requireAPIKeyScope(c, apiScopePrivateCAWrite) {
		return
	}

	var info *service.CSRInfo
	var err error
	if isPrivateCA {
		info, err = service.ParseCSRInfoForPrivateCA(body.CSR)
	} else {
		info, err = service.ParseCSRInfo(body.CSR)
	}
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}

	challenge := strings.ToLower(strings.TrimSpace(body.Challenge))
	if isPrivateCA {
		// 私有 CA 由服务端直接签名，不使用 ACME 域名验证。
		challenge = "private"
		privateCA, err := s.Store.GetPrivateCA(privateCAID)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.private_ca_not_found")
			return
		}
		if err := service.ValidateIssuingCA(privateCA); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
	} else {
		if challenge == "" {
			challenge = "dns01"
		}
		if challenge != "dns01" {
			failKey(c, http.StatusBadRequest, "api.error.csr_challenge_unsupported")
			return
		}
		caDef, err := s.Manager.ResolveCA(ca)
		if err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		// 自定义 CA 或需要 EAB 的 CA 会使用管理员保存的上游账户/EAB 凭据，
		// 受限 API Key 必须额外具备 ca:read 才能借用。
		if key := apiKeyFromContext(c); key != nil && (caDef.Custom || caDef.EAB) &&
			!key.HasScope(apiScopeCARead) && !key.HasScope(apiScopeCAWrite) {
			failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeCARead)
			return
		}
		if strings.EqualFold(caDef.Protocol, "v1") {
			failKey(c, http.StatusBadRequest, "api.error.csr_ca_v1_unsupported")
			return
		}
		// API 密钥代表受限机器身份，不能凭 certificates:request 借用管理员的任意
		// DNS 凭据。要求同时具备 dns_accounts:read，明确把“可使用的 DNS 账户”
		// 纳入密钥授权范围；浏览器会话代表管理员本人，不受此限制。
		if key := apiKeyFromContext(c); key != nil && !key.HasScope(apiScopeDNSRead) && !key.HasScope(apiScopeDNSWrite) {
			failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeDNSRead)
			return
		}
		if strings.TrimSpace(s.setting("acme.email", "")) == "" {
			failKey(c, http.StatusBadRequest, "api.error.csr_email_required")
			return
		}
	}
	allowed := service.ParseAllowedDomains(s.setting("csr.allowed_domains", ""))
	allowAll := strings.EqualFold(strings.TrimSpace(s.setting("csr.allow_all_domains", "false")), "true")
	if isPrivateCA {
		err = service.ValidatePrivateCSRDomains(info.DNSNames, allowed, allowAll)
	} else {
		err = service.ValidateCSRDomains(info.DNSNames, allowed, allowAll)
	}
	if err != nil {
		failErr(c, http.StatusForbidden, err)
		return
	}

	dnsRoutes := []model.DNSRoute{}
	dnsAccountID := int64(0)
	resolvedBy := ""
	resolvedZone := ""
	if !isPrivateCA {
		dnsRoutes, dnsAccountID, resolvedBy, resolvedZone, err = s.resolveCSRDNSRoutes(c, info.DNSNames, body.DNSAccountID)
		if err != nil {
			failErr(c, csrResolveStatus(err), err)
			return
		}
		// 自动解析已按密钥允许集合过滤；显式指定的账户仍需单独校验。
		if !s.requireAPIKeyDNSAccountsUse(c, service.DNSRouteAccountIDs(dnsRoutes)) {
			return
		}
		for _, accountID := range service.DNSRouteAccountIDs(dnsRoutes) {
			if _, err := s.Store.GetDNSAccount(accountID); err != nil {
				failKey(c, http.StatusNotFound, "api.error.dns_account_not_found")
				return
			}
		}
	}
	routesHash := service.DNSRoutesHash(dnsRoutes)

	idempotencyKey := strings.TrimSpace(body.IdempotencyKey)
	if len(idempotencyKey) > 128 {
		failKey(c, http.StatusBadRequest, "api.error.csr_idempotency_too_long")
		return
	}
	apiKeyID := int64(0)
	if key := apiKeyFromContext(c); key != nil {
		apiKeyID = key.ID
	}
	if idempotencyKey != "" {
		existing, err := s.Store.GetCSRRequestByIdempotency(apiKeyID, idempotencyKey)
		if err == nil {
			if existing.CSRHash != info.SHA256 || existing.CA != ca || !csrDNSRoutesMatch(existing, dnsRoutes, routesHash) {
				failKey(c, http.StatusConflict, "api.error.csr_idempotency_conflict")
				return
			}
			s.respondCSRRequest(c, existing, csrResponseStatus(existing))
			return
		}
		if !store.IsNotFound(err) {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	// 同一 CSR + CA + DNS 账户已存在在途记录时直接复用，避免重复消耗上游配额。
	if existing, err := s.Store.GetActiveCSRRequestByHash(info.SHA256, ca, apiKeyID, dnsAccountID, routesHash); err == nil {
		s.respondCSRRequest(c, existing, csrResponseStatus(existing))
		return
	} else if !store.IsNotFound(err) {
		failErr(c, http.StatusInternalServerError, err)
		return
	}

	publicID, err := util.RandomHex(16)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour)
	req := &model.CSRRequest{
		PublicID:       "csr_" + publicID,
		CSRPEM:         info.PEM,
		CSRHash:        info.SHA256,
		Identifiers:    info.DNSNames,
		PrimaryDomain:  info.PrimaryDomain,
		CA:             ca,
		Challenge:      challenge,
		DNSAccountID:   dnsAccountID,
		DNSRoutes:      dnsRoutes,
		DNSRoutesHash:  routesHash,
		ResolvedBy:     resolvedBy,
		ResolvedZone:   resolvedZone,
		Status:         model.CSRStatusPending,
		IdempotencyKey: idempotencyKey,
		APIKeyID:       apiKeyID,
		ExpiresAt:      &expires,
	}
	if user := getUser(c); user != nil {
		req.UserID = user.ID
	}
	if err := s.Store.CreateCSRRequest(req); err != nil {
		if store.CSRRequestExists(err) {
			if existing, getErr := s.Store.GetActiveCSRRequestByHash(info.SHA256, ca, apiKeyID, dnsAccountID, routesHash); getErr == nil {
				s.respondCSRRequest(c, existing, csrResponseStatus(existing))
				return
			}
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("csr", req.ID, "info", "log.csr.created", map[string]any{
		"domains": strings.Join(info.DNSNames, ", "),
		"ca":      ca,
	})

	if body.WaitSeconds > 0 {
		deadline := time.Now().Add(time.Duration(body.WaitSeconds) * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			latest, err := s.Store.GetCSRRequest(req.ID)
			if err != nil {
				break
			}
			if latest.Status != model.CSRStatusPending && latest.Status != model.CSRStatusRunning {
				s.respondCSRRequest(c, latest, http.StatusOK)
				return
			}
			req = latest
		}
	}
	s.respondCSRRequest(c, req, http.StatusAccepted)
}

// resolveCSRDNSRoutes 决定本次 CSR 每个域名使用哪个 DNS 账户。
// 显式 dns_account_id 优先并覆盖全部域名；省略时按管理员维护的 zone 绑定逐域名解析。
// 解析在创建时完成并落库，避免绑定变更导致在途申请被重新路由。
func (s *Server) resolveCSRDNSRoutes(c *gin.Context, domains []string, explicitID int64) ([]model.DNSRoute, int64, string, string, error) {
	bindings, err := s.Store.ListEnabledDNSZoneBindings()
	if err != nil {
		return nil, 0, "", "", err
	}
	// 受限密钥只能看到自己允许的账户，避免自动解析到无权使用的账户。
	bindings = filterDNSBindingsForAPIKey(c, bindings)
	if explicitID > 0 {
		routes, err := service.ExplicitDNSRoutesForDomains(domains, explicitID)
		if err != nil {
			return nil, 0, "", "", err
		}
		return routes, explicitID, "explicit", service.MatchDNSAccountForDomains(domains, bindings, explicitID), nil
	}
	routes, err := service.ResolveDNSRoutesForDomains(domains, bindings)
	if err != nil {
		return nil, 0, "", "", err
	}
	accountIDs := service.DNSRouteAccountIDs(routes)
	primary := int64(0)
	if len(accountIDs) == 1 {
		primary = accountIDs[0]
	}
	longest := ""
	for _, route := range routes {
		if len(route.Zone) > len(longest) {
			longest = route.Zone
		}
	}
	return routes, primary, "binding", longest, nil
}

// csrDNSAccountIDs 返回 CSR 实际使用的 DNS 账户集合。
func csrDNSAccountIDs(req *model.CSRRequest) []int64 {
	if req == nil {
		return nil
	}
	ids := service.DNSRouteAccountIDs(req.DNSRoutes)
	if len(ids) == 0 && req.DNSAccountID > 0 {
		ids = []int64{req.DNSAccountID}
	}
	return ids
}

// csrDNSRoutesMatch 兼容升级前只保存单一账户的历史记录。
func csrDNSRoutesMatch(existing *model.CSRRequest, routes []model.DNSRoute, routesHash string) bool {
	if existing == nil {
		return false
	}
	if existing.DNSRoutesHash != "" {
		return existing.DNSRoutesHash == routesHash
	}
	accountIDs := service.DNSRouteAccountIDs(routes)
	return len(accountIDs) == 1 && existing.DNSAccountID == accountIDs[0]
}

func csrResolveStatus(err error) int {
	var localized *i18n.Error
	if errors.As(err, &localized) {
		switch localized.Key {
		case "service.csr.dns_account_ambiguous":
			return http.StatusConflict
		case "service.csr.dns_account_unresolved", "service.csr.dns_zone_invalid", "service.csr.dns_required":
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

func csrResponseStatus(req *model.CSRRequest) int {
	if req.Status == model.CSRStatusPending || req.Status == model.CSRStatusRunning {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func (s *Server) listCSRRequests(c *gin.Context) {
	apiKeyID := int64(0)
	if key := apiKeyFromContext(c); key != nil {
		apiKeyID = key.ID
	}
	list, err := s.Store.ListCSRRequests(apiKeyID, 200)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	views := make([]*csrRequestView, 0, len(list))
	for index := range list {
		views = append(views, s.csrRequestView(c, &list[index]))
	}
	ok(c, views)
}

func (s *Server) getCSRRequest(c *gin.Context) {
	req, found := s.lookupCSRRequest(c)
	if !found {
		return
	}
	ok(c, s.csrRequestView(c, req))
}

func (s *Server) downloadCSRRequestCertificate(c *gin.Context) {
	req, found := s.lookupCSRRequest(c)
	if !found {
		return
	}
	if req.Status != model.CSRStatusIssued || req.CertificateID <= 0 {
		failKey(c, http.StatusConflict, "api.error.csr_not_ready")
		return
	}
	cert, err := s.Store.GetCertificate(req.CertificateID)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.cert_not_found")
		return
	}
	kind := c.DefaultQuery("type", "fullchain")
	var content, suffix string
	switch kind {
	case "cert":
		content, suffix = cert.CertPEM, "crt"
	case "fullchain":
		content, suffix = cert.FullchainPEM, "fullchain.crt"
	default:
		failKey(c, http.StatusBadRequest, "api.error.csr_certificate_type_invalid")
		return
	}
	filename := safeDownloadName(cert.PrimaryDomain) + "-" + cert.NotAfter.Format("20060102") + "." + suffix
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-pem-file; charset=utf-8", []byte(content))
}

func (s *Server) cancelCSRRequest(c *gin.Context) {
	req, found := s.lookupCSRRequest(c)
	if !found {
		return
	}
	cancelled, err := s.Store.SetCSRRequestCancelled(req.ID)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !cancelled {
		failKey(c, http.StatusConflict, "api.error.csr_not_cancellable")
		return
	}
	s.Store.AddLogKey("csr", req.ID, "warning", "log.csr.cancelled", nil)
	updated, err := s.Store.GetCSRRequest(req.ID)
	if err != nil {
		ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.csr_cancelled", nil)})
		return
	}
	ok(c, s.csrRequestView(c, updated))
}

func (s *Server) retryCSRRequest(c *gin.Context) {
	req, found := s.lookupCSRRequest(c)
	if !found {
		return
	}
	// 密钥的允许集合可能在申请创建后被收紧，重试前重新校验。
	if !s.requireAPIKeyDNSAccountsUse(c, csrDNSAccountIDs(req)) {
		return
	}
	retried, err := s.Store.RetryCSRRequest(req.ID)
	if err != nil {
		if store.CSRRequestExists(err) {
			failKey(c, http.StatusConflict, "api.error.csr_already_active")
			return
		}
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !retried {
		failKey(c, http.StatusConflict, "api.error.csr_not_retryable")
		return
	}
	s.Store.AddLogKey("csr", req.ID, "info", "log.csr.retried", nil)
	updated, err := s.Store.GetCSRRequest(req.ID)
	if err != nil {
		ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.csr_retried", nil)})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": s.csrRequestView(c, updated)})
}

// lookupCSRRequest 查询申请并校验归属。API 密钥只能访问自己创建的申请；
// 会话管理员可访问全部申请。不存在与无权限统一返回 404，避免枚举。
func (s *Server) lookupCSRRequest(c *gin.Context) (*model.CSRRequest, bool) {
	publicID := strings.TrimSpace(c.Param("id"))
	if publicID == "" {
		failKey(c, http.StatusNotFound, "api.error.csr_request_not_found")
		return nil, false
	}
	req, err := s.Store.GetCSRRequestByPublicID(publicID)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.csr_request_not_found")
		return nil, false
	}
	if key := apiKeyFromContext(c); key != nil && req.APIKeyID != key.ID {
		failKey(c, http.StatusNotFound, "api.error.csr_request_not_found")
		return nil, false
	}
	return req, true
}

func (s *Server) respondCSRRequest(c *gin.Context, req *model.CSRRequest, status int) {
	c.JSON(status, gin.H{"success": true, "data": s.csrRequestView(c, req)})
}

func (s *Server) csrRequestView(c *gin.Context, req *model.CSRRequest) *csrRequestView {
	if req == nil {
		return nil
	}
	view := &csrRequestView{
		ID:            req.PublicID,
		Status:        req.Status,
		Identifiers:   append([]string(nil), req.Identifiers...),
		PrimaryDomain: req.PrimaryDomain,
		CA:            req.CA,
		Challenge:     req.Challenge,
		CSRHash:       req.CSRHash,
		DNSAccountID:  req.DNSAccountID,
		DNSRoutes:     append([]model.DNSRoute(nil), req.DNSRoutes...),
		ResolvedBy:    req.ResolvedBy,
		ResolvedZone:  req.ResolvedZone,
		Attempts:      req.Attempts,
		Retryable:     req.Retryable,
		CertificateID: req.CertificateID,
		CreatedAt:     req.CreatedAt,
		UpdatedAt:     req.UpdatedAt,
		ExpiresAt:     req.ExpiresAt,
		CompletedAt:   req.CompletedAt,
		KeyAvailable:  false,
	}
	if req.ErrorMessage != "" {
		view.Error = &csrRequestErrorView{
			Code:    req.ErrorCode,
			Message: i18n.Render(locale(c), req.ErrorMessage),
		}
	}
	if req.CertificateID > 0 {
		if cert, err := s.Store.GetCertificate(req.CertificateID); err == nil {
			view.Issuer = cert.Issuer
			view.Serial = cert.Serial
			view.NotBefore = &cert.NotBefore
			view.NotAfter = &cert.NotAfter
			view.KeyAvailable = cert.KeyAvailable
		}
	}
	return view
}
