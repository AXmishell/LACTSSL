package acme

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

type orderIdentifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type newOrderRequest struct {
	Identifiers []orderIdentifier `json:"identifiers"`
	Replaces    string            `json:"replaces"`
	NotBefore   string            `json:"notBefore"`
	NotAfter    string            `json:"notAfter"`
}

type orderView struct {
	Status         string            `json:"status"`
	Expires        time.Time         `json:"expires,omitempty"`
	Identifiers    []orderIdentifier `json:"identifiers"`
	Authorizations []string          `json:"authorizations"`
	Finalize       string            `json:"finalize"`
	Certificate    string            `json:"certificate,omitempty"`
	Replaces       string            `json:"replaces,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type authorizationView struct {
	Identifier orderIdentifier `json:"identifier"`
	Status     string          `json:"status"`
	Expires    time.Time       `json:"expires,omitempty"`
	Challenges []challengeView `json:"challenges"`
	Wildcard   bool            `json:"wildcard,omitempty"`
}

type challengeView struct {
	Type      string     `json:"type"`
	URL       string     `json:"url"`
	Status    string     `json:"status"`
	Token     string     `json:"token"`
	Validated *time.Time `json:"validated,omitempty"`
	Error     *Problem   `json:"error,omitempty"`
}

func (s *Server) handleNewOrder(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	var request newOrderRequest
	if err := json.Unmarshal(envelope.Payload, &request); err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "invalid newOrder payload")
		return
	}
	identifiers, problem := validateOrderIdentifiers(ep, request.Identifiers)
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	replaces := strings.TrimSpace(request.Replaces)
	if replaces != "" {
		replaced, problem := s.certificateForCertID(ep, replaces)
		if problem != nil {
			writeProblemType(w, http.StatusBadRequest, ProblemRejectedIdentifier, "replaces certificate not found")
			return
		}
		if replaced.ACMEAccountID != account.ID {
			writeProblemType(w, http.StatusBadRequest, ProblemRejectedIdentifier, "replaces certificate belongs to another account")
			return
		}
		if !sharesIdentifier(identifiers, replaced.Domains) {
			writeProblemType(w, http.StatusBadRequest, ProblemRejectedIdentifier, "replaces certificate shares no identifier with the order")
			return
		}
		active, err := s.Store.HasActiveACMEOrderReplaces(ep.ID, replaces)
		if err != nil {
			writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to check replacement order")
			return
		}
		if active {
			writeProblemType(w, http.StatusConflict, ProblemAlreadyReplaced, "certificate already has an active replacement order")
			return
		}
	}
	limit := ep.MaxOrdersPerHour
	if limit <= 0 {
		limit = 60
	}
	count, err := s.Store.CountACMEOrdersSince(account.ID, s.now().Add(-time.Hour))
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to check order rate limit")
		return
	}
	if count >= int64(limit) {
		writeProblemType(w, http.StatusTooManyRequests, ProblemRateLimited, "order rate limit exceeded")
		return
	}

	expires := s.now().Add(time.Hour).UTC()
	orderPublicID, err := NewPublicID()
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to generate order ID")
		return
	}
	order := &model.ACMEOrder{
		PublicID:    orderPublicID,
		EndpointID:  ep.ID,
		AccountID:   account.ID,
		Status:      "pending",
		Identifiers: make([]string, 0, len(identifiers)),
		Replaces:    replaces,
		ExpiresAt:   &expires,
	}
	authzs := make([]*model.ACMEAuthorization, 0, len(identifiers))
	for _, identifier := range identifiers {
		order.Identifiers = append(order.Identifiers, identifier.Value)
		authzPublicID, err := NewPublicID()
		if err != nil {
			writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to generate authorization ID")
			return
		}
		wildcard := identifier.Type == "dns" && strings.HasPrefix(identifier.Value, "*.")
		authz := &model.ACMEAuthorization{
			PublicID:       authzPublicID,
			Identifier:     identifier.Value,
			IdentifierType: identifier.Type,
			Wildcard:       wildcard,
			Status:         "pending",
			ExpiresAt:      &expires,
		}
		if wildcard {
			if !ep.DNS01Enabled {
				writeProblemType(w, http.StatusBadRequest, ProblemRejectedIdentifier, "wildcard identifiers require DNS-01")
				return
			}
			authz.Challenges = append(authz.Challenges, s.newChallenge("dns-01"))
		} else if identifier.Type == "ip" {
			if ep.HTTP01Enabled {
				authz.Challenges = append(authz.Challenges, s.newChallenge("http-01"))
			}
			if ep.TLSALPN01Enabled {
				authz.Challenges = append(authz.Challenges, s.newChallenge("tls-alpn-01"))
			}
		} else {
			if ep.HTTP01Enabled {
				authz.Challenges = append(authz.Challenges, s.newChallenge("http-01"))
			}
			if ep.DNS01Enabled {
				authz.Challenges = append(authz.Challenges, s.newChallenge("dns-01"))
			}
			if ep.TLSALPN01Enabled {
				authz.Challenges = append(authz.Challenges, s.newChallenge("tls-alpn-01"))
			}
		}
		if len(authz.Challenges) == 0 {
			writeProblemType(w, http.StatusBadRequest, ProblemRejectedIdentifier, "no challenge type is enabled for this identifier")
			return
		}
		authzs = append(authzs, authz)
	}
	if err := s.Store.CreateACMEOrder(order, authzs); err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to create order: "+err.Error())
		return
	}
	w.Header().Set("Location", s.orderURL(r, ep, order.PublicID))
	writeJSON(w, http.StatusCreated, s.orderView(r, ep, order))
}

func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	order, err := s.Store.GetACMEOrderByPublicID(ep.ID, r.PathValue("id"))
	if err != nil || order.AccountID != account.ID {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "order not found")
		return
	}
	if len(envelope.Payload) != 0 {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "order does not support updates")
		return
	}
	if s.orderExpired(order) && order.Status != "valid" {
		order.Status = "invalid"
		_ = s.Store.UpdateACMEOrderStatus(order.ID, "invalid", "order expired", 0)
	}
	writeJSON(w, http.StatusOK, s.orderView(r, ep, order))
}

func (s *Server) handleAuthorization(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	authz, order, ok := s.authorizationForRequest(w, ep, account, r.PathValue("id"))
	if !ok {
		return
	}
	if len(envelope.Payload) == 0 {
		writeJSON(w, http.StatusOK, s.authorizationView(r, ep, authz))
		return
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(envelope.Payload, &request); err != nil || request.Status != "deactivated" {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "unsupported authorization update")
		return
	}
	_ = s.Store.UpdateACMEAuthorizationStatus(authz.ID, "deactivated")
	_ = s.Store.UpdateACMEOrderStatus(order.ID, "invalid", "authorization deactivated", 0)
	authz.Status = "deactivated"
	writeJSON(w, http.StatusOK, s.authorizationView(r, ep, authz))
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	challenge, authz, order, ok := s.challengeForRequest(w, ep, account, r.PathValue("id"))
	if !ok {
		return
	}
	if len(envelope.Payload) == 0 {
		writeJSON(w, http.StatusOK, s.challengeView(r, ep, challenge))
		return
	}
	if challenge.Status == "valid" || challenge.Status == "invalid" || authz.Status == "invalid" || order.Status == "invalid" {
		writeJSON(w, http.StatusOK, s.challengeView(r, ep, challenge))
		return
	}
	if s.orderExpired(order) || s.authzExpired(authz) {
		_ = s.Store.UpdateACMEChallengeStatus(challenge.ID, "invalid", "challenge expired", nil)
		_ = s.Store.UpdateACMEAuthorizationStatus(authz.ID, "invalid")
		_ = s.Store.UpdateACMEOrderStatus(order.ID, "invalid", "challenge expired", 0)
		challenge.Status = "invalid"
		writeJSON(w, http.StatusOK, s.challengeView(r, ep, challenge))
		return
	}
	_ = s.Store.UpdateACMEChallengeStatus(challenge.ID, "processing", "", nil)
	problem := s.validateChallenge(r.Context(), ep, account, authz, challenge)
	now := s.now().UTC()
	if problem != nil {
		_ = s.Store.UpdateACMEChallengeStatus(challenge.ID, "invalid", problem.Detail, nil)
		_ = s.Store.UpdateACMEAuthorizationStatus(authz.ID, "invalid")
		_ = s.Store.UpdateACMEOrderStatus(order.ID, "invalid", problem.Detail, 0)
		challenge.Status = "invalid"
		challenge.Error = problem.Detail
		writeJSON(w, http.StatusOK, s.challengeView(r, ep, challenge))
		return
	}
	_ = s.Store.UpdateACMEChallengeStatus(challenge.ID, "valid", "", &now)
	_ = s.Store.UpdateACMEAuthorizationStatus(authz.ID, "valid")
	challenge.Status = "valid"
	challenge.ValidatedAt = &now
	authz.Status = "valid"
	if s.allAuthorizationsValid(order.ID) {
		_ = s.Store.UpdateACMEOrderStatus(order.ID, "ready", "", 0)
	}
	writeJSON(w, http.StatusOK, s.challengeView(r, ep, challenge))
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	order, err := s.Store.GetACMEOrderByPublicID(ep.ID, r.PathValue("id"))
	if err != nil || order.AccountID != account.ID {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "order not found")
		return
	}
	if order.Status == "valid" && order.CertificateID > 0 {
		writeJSON(w, http.StatusOK, s.orderView(r, ep, order))
		return
	}
	if s.orderExpired(order) {
		_ = s.Store.UpdateACMEOrderStatus(order.ID, "invalid", "order expired", 0)
		writeProblemType(w, http.StatusForbidden, ProblemOrderNotReady, "order has expired")
		return
	}
	if order.Status != "ready" {
		writeProblemType(w, http.StatusForbidden, ProblemOrderNotReady, "order is not ready for finalization")
		return
	}
	var request struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(envelope.Payload, &request); err != nil || strings.TrimSpace(request.CSR) == "" {
		writeProblemType(w, http.StatusBadRequest, ProblemBadCSR, "finalize requires a CSR")
		return
	}
	csrDER, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(request.CSR))
	if err != nil {
		if csrDER, err = base64.URLEncoding.DecodeString(strings.TrimSpace(request.CSR)); err != nil {
			writeProblemType(w, http.StatusBadRequest, ProblemBadCSR, "CSR is not valid base64url")
			return
		}
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemBadCSR, "failed to parse CSR: "+err.Error())
		return
	}
	ca, err := s.Store.GetPrivateCA(ep.CAID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "issuing CA not found")
		return
	}
	chain, err := service.BuildCAChainPEM(s.Store, ca)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to build CA chain")
		return
	}
	authzs, err := s.Store.ListACMEAuthorizations(order.ID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to load authorizations")
		return
	}
	identifiers := make([]service.CertificateIdentifier, 0, len(authzs))
	for _, authz := range authzs {
		identifiers = append(identifiers, service.CertificateIdentifier{Type: authz.IdentifierType, Value: authz.Identifier})
	}
	validity := ep.ValidityDays
	if validity <= 0 {
		validity = 90
	}
	crlSupported, ocspSupported, err := service.CARevocationCapabilities(ca)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, err.Error())
		return
	}
	options := service.SignCSROptions{
		NotBefore: s.now().UTC().Add(-5 * time.Minute),
		NotAfter:  s.now().UTC().AddDate(0, 0, validity),
		ChainPEM:  chain,
	}
	if crlSupported {
		options.CRLURLs = []string{s.baseURL(r) + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/crl"}
	}
	if ocspSupported {
		options.OCSPURLs = []string{s.baseURL(r) + "/pki/" + strconv.FormatInt(ca.ID, 10) + "/ocsp"}
	}
	issued, err := service.SignCSR(ca, csr, identifiers, options)
	if err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemBadCSR, err.Error())
		return
	}
	primary := order.Identifiers[0]
	certificate := &model.Certificate{
		Domains:       append([]string(nil), order.Identifiers...),
		PrimaryDomain: primary,
		Issuer:        issued.Issuer,
		CA:            fmt.Sprintf("private:%d", ca.ID),
		Source:        "acme",
		PrivateCAID:   ca.ID,
		ACMEOrderID:   order.ID,
		ACMEAccountID: account.ID,
		CertPEM:       issued.CertPEM,
		KeyPEM:        "",
		FullchainPEM:  issued.FullchainPEM,
		Serial:        issued.Serial,
		NotBefore:     issued.NotBefore,
		NotAfter:      issued.NotAfter,
		Status:        "valid",
	}
	if err := s.Store.CreateCertificate(certificate); err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to save certificate")
		return
	}
	_ = s.Store.UpdateACMEOrderStatus(order.ID, "valid", "", certificate.ID)
	order.Status = "valid"
	order.CertificateID = certificate.ID
	writeJSON(w, http.StatusOK, s.orderView(r, ep, order))
}

func (s *Server) handleCertificate(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, _, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	order, err := s.Store.GetACMEOrderByPublicID(ep.ID, r.PathValue("id"))
	if err != nil || order.AccountID != account.ID || order.Status != "valid" || order.CertificateID <= 0 {
		writeProblemType(w, http.StatusNotFound, ProblemOrderNotReady, "certificate is not available")
		return
	}
	certificate, err := s.Store.GetCertificate(order.CertificateID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "certificate not found")
		return
	}
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(certificate.FullchainPEM))
}

func (s *Server) authenticateAccount(w http.ResponseWriter, r *http.Request, ep *model.ACMEEndpoint) (*model.ACMEAccount, *jwsEnvelope, bool) {
	body, ok := s.readBody(w, r)
	if !ok {
		return nil, nil, false
	}
	expectedURL := s.baseURL(r) + r.URL.Path
	envelope, problem := parseJWS(body, expectedURL, s.Nonces)
	if problem != nil {
		writeProblem(w, problem)
		return nil, nil, false
	}
	if envelope.Protected.KeyID == "" || envelope.Protected.JSONWebKey != nil {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "account JWS must use a kid header")
		return nil, nil, false
	}
	account, err := s.Store.GetACMEAccountByKid(envelope.Protected.KeyID)
	if err != nil || account.EndpointID != ep.ID || account.Status != "valid" {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "account is not valid for this endpoint")
		return nil, nil, false
	}
	if problem := verifyJWSWithAccount(envelope, account.PublicJWK); problem != nil {
		writeProblem(w, problem)
		return nil, nil, false
	}
	_ = s.Store.TouchACMEAccount(account.ID)
	return account, envelope, true
}

func (s *Server) authorizationForRequest(w http.ResponseWriter, ep *model.ACMEEndpoint, account *model.ACMEAccount, publicID string) (*model.ACMEAuthorization, *model.ACMEOrder, bool) {
	authz, err := s.Store.GetACMEAuthorizationByPublicID(publicID)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "authorization not found")
		return nil, nil, false
	}
	order, err := s.Store.GetACMEOrderByID(authz.OrderID)
	if err != nil || order.EndpointID != ep.ID || order.AccountID != account.ID {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "authorization not found")
		return nil, nil, false
	}
	return authz, order, true
}

func (s *Server) challengeForRequest(w http.ResponseWriter, ep *model.ACMEEndpoint, account *model.ACMEAccount, publicID string) (*model.ACMEChallenge, *model.ACMEAuthorization, *model.ACMEOrder, bool) {
	challenge, err := s.Store.GetACMEChallengeByPublicID(publicID)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "challenge not found")
		return nil, nil, nil, false
	}
	authz, err := s.Store.GetACMEAuthorizationByID(challenge.AuthzID)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "challenge not found")
		return nil, nil, nil, false
	}
	order, err := s.Store.GetACMEOrderByID(authz.OrderID)
	if err != nil || order.EndpointID != ep.ID || order.AccountID != account.ID {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "challenge not found")
		return nil, nil, nil, false
	}
	return challenge, authz, order, true
}

func (s *Server) validateChallenge(ctx context.Context, ep *model.ACMEEndpoint, account *model.ACMEAccount, authz *model.ACMEAuthorization, challenge *model.ACMEChallenge) *ProblemError {
	select {
	case s.validationSem <- struct{}{}:
		defer func() { <-s.validationSem }()
	case <-ctx.Done():
		return newProblemError(ProblemServerInternal, "challenge validation cancelled", 500)
	}
	keyAuthorization := challenge.Token + "." + account.Thumbprint
	switch challenge.Type {
	case "http-01":
		base := s.HTTP01
		if base == nil {
			base = NewHTTP01Validator()
		}
		validator := *base
		validator.AllowPrivate = ep.AllowPrivateHTTP01
		validator.AllowedCIDRs = append([]string(nil), ep.HTTP01AllowedCIDRs...)
		return asProblemError(validator.Validate(ctx, authz.Identifier, challenge.Token, keyAuthorization))
	case "dns-01":
		base := s.DNS01
		if base == nil {
			base = NewDNS01Validator()
		}
		validator := *base
		return asProblemError(validator.Validate(ctx, authz.Identifier, keyAuthorization))
	case "tls-alpn-01":
		base := s.TLSALPN01
		if base == nil {
			base = NewTLSALPN01Validator()
		}
		validator := *base
		validator.AllowPrivate = ep.AllowPrivateTLSALPN01
		validator.AllowedCIDRs = append([]string(nil), ep.TLSALPN01AllowedCIDRs...)
		return asProblemError(validator.Validate(ctx, authz.Identifier, keyAuthorization))
	default:
		return newProblemError(ProblemMalformed, "unsupported challenge type", 400)
	}
}

func asProblemError(err error) *ProblemError {
	if err == nil {
		return nil
	}
	if problem, ok := err.(*ProblemError); ok {
		return problem
	}
	return newProblemError(ProblemServerInternal, err.Error(), 500)
}

func (s *Server) allAuthorizationsValid(orderID int64) bool {
	authzs, err := s.Store.ListACMEAuthorizations(orderID)
	if err != nil {
		return false
	}
	for _, authz := range authzs {
		if authz.Status != "valid" {
			return false
		}
	}
	return true
}

func (s *Server) orderExpired(order *model.ACMEOrder) bool {
	return order.ExpiresAt != nil && !s.now().Before(*order.ExpiresAt)
}

func (s *Server) authzExpired(authz *model.ACMEAuthorization) bool {
	return authz.ExpiresAt != nil && !s.now().Before(*authz.ExpiresAt)
}

func (s *Server) newChallenge(kind string) model.ACMEChallenge {
	token, _ := randomToken(32)
	return model.ACMEChallenge{
		PublicID: mustRandomID(),
		Type:     kind,
		Token:    token,
		Status:   "pending",
	}
}

func mustRandomID() string {
	value, _ := randomToken(16)
	return value
}

func validateOrderIdentifiers(ep *model.ACMEEndpoint, values []orderIdentifier) ([]orderIdentifier, *ProblemError) {
	if len(values) == 0 {
		return nil, newProblemError(ProblemMalformed, "at least one identifier is required", 400)
	}
	if len(values) > 100 {
		return nil, newProblemError(ProblemMalformed, "too many identifiers", 400)
	}
	out := make([]orderIdentifier, 0, len(values))
	seen := map[string]bool{}
	for _, item := range values {
		kind := strings.ToLower(strings.TrimSpace(item.Type))
		value := strings.TrimSpace(item.Value)
		switch kind {
		case "dns":
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			if !validDNSIdentifier(value) {
				return nil, newProblemError(ProblemRejectedIdentifier, "invalid DNS identifier: "+item.Value, 400)
			}
			wildcard := strings.HasPrefix(value, "*.")
			if wildcard && !ep.AllowWildcard {
				return nil, newProblemError(ProblemRejectedIdentifier, "wildcard identifiers are not enabled for this endpoint", 400)
			}
			if !domainAllowed(value, ep.AllowedDomains) {
				return nil, newProblemError(ProblemRejectedIdentifier, "identifier is not in the endpoint allowlist: "+value, 400)
			}
			key := "dns:" + value
			if seen[key] {
				return nil, newProblemError(ProblemMalformed, "duplicate identifier: "+value, 400)
			}
			seen[key] = true
			out = append(out, orderIdentifier{Type: "dns", Value: value})
		case "ip":
			ip := net.ParseIP(value)
			if ip == nil {
				return nil, newProblemError(ProblemRejectedIdentifier, "invalid IP identifier: "+item.Value, 400)
			}
			if !ep.AllowIP {
				return nil, newProblemError(ProblemRejectedIdentifier, "IP identifiers are not enabled for this endpoint", 400)
			}
			if !ipAllowedByIdentifier(ip, ep.AllowedIPCIDRs) {
				return nil, newProblemError(ProblemRejectedIdentifier, "IP identifier is not in the endpoint allowlist: "+ip.String(), 400)
			}
			key := "ip:" + ip.String()
			if seen[key] {
				return nil, newProblemError(ProblemMalformed, "duplicate identifier: "+ip.String(), 400)
			}
			seen[key] = true
			out = append(out, orderIdentifier{Type: "ip", Value: ip.String()})
		default:
			return nil, newProblemError(ProblemUnsupportedIdentifier, "only dns and ip identifiers are supported", 400)
		}
	}
	return out, nil
}

func ipAllowedByIdentifier(ip net.IP, cidrs []string) bool {
	if len(cidrs) == 0 {
		return false
	}
	for _, value := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidDNSName 校验 ACME 订单与域名白名单中使用的 DNS 名称。
func ValidDNSName(value string) bool {
	return validDNSIdentifier(value)
}

func validDNSIdentifier(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	name := strings.TrimPrefix(value, "*.")
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func sharesIdentifier(identifiers []orderIdentifier, certificateDomains []string) bool {
	values := make(map[string]bool, len(certificateDomains))
	for _, value := range certificateDomains {
		values[strings.ToLower(strings.TrimSpace(value))] = true
	}
	for _, identifier := range identifiers {
		if values[strings.ToLower(identifier.Value)] {
			return true
		}
	}
	return false
}

func domainAllowed(identifier string, allowed []string) bool {
	base := strings.TrimPrefix(strings.ToLower(identifier), "*.")
	for _, item := range allowed {
		item = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item)), "*.")
		if item == "" {
			continue
		}
		if base == item || strings.HasSuffix(base, "."+item) {
			return true
		}
	}
	return false
}

func (s *Server) orderURL(r *http.Request, ep *model.ACMEEndpoint, publicID string) string {
	return s.endpointBase(r, ep) + "/order/" + publicID
}

func (s *Server) authzURL(r *http.Request, ep *model.ACMEEndpoint, publicID string) string {
	return s.endpointBase(r, ep) + "/authz/" + publicID
}

func (s *Server) challengeURL(r *http.Request, ep *model.ACMEEndpoint, publicID string) string {
	return s.endpointBase(r, ep) + "/challenge/" + publicID
}

func (s *Server) finalizeURL(r *http.Request, ep *model.ACMEEndpoint, publicID string) string {
	return s.endpointBase(r, ep) + "/finalize/" + publicID
}

func (s *Server) certificateURL(r *http.Request, ep *model.ACMEEndpoint, publicID string) string {
	return s.endpointBase(r, ep) + "/cert/" + publicID
}

func (s *Server) orderView(r *http.Request, ep *model.ACMEEndpoint, order *model.ACMEOrder) orderView {
	view := orderView{
		Status:      order.Status,
		Identifiers: make([]orderIdentifier, 0, len(order.Identifiers)),
		Finalize:    s.finalizeURL(r, ep, order.PublicID),
		Replaces:    order.Replaces,
		Error:       order.Error,
	}
	if order.ExpiresAt != nil {
		view.Expires = *order.ExpiresAt
	}
	authzs, _ := s.Store.ListACMEAuthorizations(order.ID)
	view.Authorizations = make([]string, 0, len(authzs))
	for _, authz := range authzs {
		view.Identifiers = append(view.Identifiers, orderIdentifier{Type: authz.IdentifierType, Value: authz.Identifier})
		view.Authorizations = append(view.Authorizations, s.authzURL(r, ep, authz.PublicID))
	}
	if len(view.Identifiers) == 0 {
		for _, value := range order.Identifiers {
			view.Identifiers = append(view.Identifiers, orderIdentifier{Type: "dns", Value: value})
		}
	}
	if order.CertificateID > 0 {
		view.Certificate = s.certificateURL(r, ep, order.PublicID)
	}
	return view
}

func (s *Server) authorizationView(r *http.Request, ep *model.ACMEEndpoint, authz *model.ACMEAuthorization) authorizationView {
	view := authorizationView{
		Identifier: orderIdentifier{Type: authz.IdentifierType, Value: authz.Identifier},
		Status:     authz.Status,
		Wildcard:   authz.Wildcard,
		Challenges: []challengeView{},
	}
	if authz.ExpiresAt != nil {
		view.Expires = *authz.ExpiresAt
	}
	challenges, _ := s.Store.ListACMEChallenges(authz.ID)
	for _, challenge := range challenges {
		view.Challenges = append(view.Challenges, s.challengeView(r, ep, &challenge))
	}
	return view
}

func (s *Server) challengeView(r *http.Request, ep *model.ACMEEndpoint, challenge *model.ACMEChallenge) challengeView {
	view := challengeView{
		Type:      challenge.Type,
		URL:       s.challengeURL(r, ep, challenge.PublicID),
		Status:    challenge.Status,
		Token:     challenge.Token,
		Validated: challenge.ValidatedAt,
	}
	if challenge.Error != "" {
		view.Error = &Problem{Type: ProblemIncorrectResponse, Detail: challenge.Error, Status: 400}
	}
	return view
}
