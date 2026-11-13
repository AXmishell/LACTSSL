package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/acme"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/util"
)

type acmeEndpointBody struct {
	Name                  *string   `json:"name"`
	Slug                  *string   `json:"slug"`
	CAID                  *int64    `json:"ca_id"`
	Enabled               *bool     `json:"enabled"`
	EABRequired           *bool     `json:"eab_required"`
	AllowedDomains        *[]string `json:"allowed_domains"`
	AllowWildcard         *bool     `json:"allow_wildcard"`
	AllowIP               *bool     `json:"allow_ip"`
	AllowedIPCIDRs        *[]string `json:"allowed_ip_cidrs"`
	HTTP01Enabled         *bool     `json:"http01_enabled"`
	DNS01Enabled          *bool     `json:"dns01_enabled"`
	TLSALPN01Enabled      *bool     `json:"tls_alpn_01_enabled"`
	AllowPrivateHTTP01    *bool     `json:"allow_private_http01"`
	AllowPrivateTLSALPN01 *bool     `json:"allow_private_tls_alpn_01"`
	HTTP01AllowedCIDRs    *[]string `json:"http01_allowed_cidrs"`
	TLSALPN01AllowedCIDRs *[]string `json:"tls_alpn_01_allowed_cidrs"`
	ValidityDays          *int      `json:"validity_days"`
	MaxOrdersPerHour      *int      `json:"max_orders_per_hour"`
}

type acmeEndpointView struct {
	model.ACMEEndpoint
	DirectoryURL string `json:"directory_url"`
	CAName       string `json:"ca_name"`
}

func (s *Server) acmeEndpointView(c *gin.Context, ep model.ACMEEndpoint) acmeEndpointView {
	caName := ""
	if ca, err := s.Store.GetPrivateCA(ep.CAID); err == nil {
		caName = ca.Name
	}
	return acmeEndpointView{
		ACMEEndpoint: ep,
		DirectoryURL: s.acmeDirectoryURL(c, ep.Slug),
		CAName:       caName,
	}
}

func (s *Server) acmeDirectoryURL(c *gin.Context, slug string) string {
	base := strings.TrimRight(strings.TrimSpace(s.ACMEBaseURL), "/")
	if base == "" {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		host := strings.TrimSpace(c.Request.Host)
		if !validPublicHost(host) {
			host = "localhost"
		}
		base = scheme + "://" + host
	}
	return base + "/acme/" + slug + "/directory"
}

func (s *Server) listACMEEndpoints(c *gin.Context) {
	list, err := s.Store.ListACMEEndpoints()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]acmeEndpointView, 0, len(list))
	for _, ep := range list {
		out = append(out, s.acmeEndpointView(c, ep))
	}
	ok(c, out)
}

func (s *Server) createACMEEndpoint(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	var body acmeEndpointBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ep := &model.ACMEEndpoint{
		Enabled:               false,
		EABRequired:           true,
		HTTP01Enabled:         true,
		DNS01Enabled:          true,
		ValidityDays:          90,
		MaxOrdersPerHour:      60,
		AllowedDomains:        []string{},
		AllowedIPCIDRs:        []string{},
		HTTP01AllowedCIDRs:    []string{},
		TLSALPN01AllowedCIDRs: []string{},
	}
	applyACMEEndpointBody(ep, &body)
	if err := s.validateACMEEndpoint(ep); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	publicID, err := acme.NewPublicID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ep.PublicID = publicID
	if err := s.Store.CreateACMEEndpoint(ep); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("acme", ep.ID, "info", "log.acme.endpoint_created", map[string]any{"name": ep.Name, "slug": ep.Slug})
	ok(c, s.acmeEndpointView(c, *ep))
}

func (s *Server) updateACMEEndpoint(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ep, err := s.Store.GetACMEEndpoint(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_endpoint_not_found")
		return
	}
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	var body acmeEndpointBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	applyACMEEndpointBody(ep, &body)
	if err := s.validateACMEEndpoint(ep); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.UpdateACMEEndpoint(ep); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("acme", ep.ID, "info", "log.acme.endpoint_updated", map[string]any{"name": ep.Name, "slug": ep.Slug})
	ok(c, s.acmeEndpointView(c, *ep))
}

func (s *Server) deleteACMEEndpoint(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ep, err := s.Store.GetACMEEndpoint(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_endpoint_not_found")
		return
	}
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	if err := s.Store.DeleteACMEEndpoint(id); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("acme", id, "warning", "log.acme.endpoint_deleted", map[string]any{"name": ep.Name, "slug": ep.Slug})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func applyACMEEndpointBody(ep *model.ACMEEndpoint, body *acmeEndpointBody) {
	if body.Name != nil {
		ep.Name = strings.TrimSpace(*body.Name)
	}
	if body.Slug != nil {
		ep.Slug = strings.ToLower(strings.TrimSpace(*body.Slug))
	}
	if body.CAID != nil {
		ep.CAID = *body.CAID
	}
	if body.Enabled != nil {
		ep.Enabled = *body.Enabled
	}
	if body.EABRequired != nil {
		ep.EABRequired = *body.EABRequired
	}
	if body.AllowedDomains != nil {
		ep.AllowedDomains = util.NormalizeDomains(*body.AllowedDomains)
	}
	if body.AllowWildcard != nil {
		ep.AllowWildcard = *body.AllowWildcard
	}
	if body.AllowIP != nil {
		ep.AllowIP = *body.AllowIP
	}
	if body.AllowedIPCIDRs != nil {
		ep.AllowedIPCIDRs = normalizeStrings(*body.AllowedIPCIDRs)
	}
	if body.HTTP01Enabled != nil {
		ep.HTTP01Enabled = *body.HTTP01Enabled
	}
	if body.DNS01Enabled != nil {
		ep.DNS01Enabled = *body.DNS01Enabled
	}
	if body.TLSALPN01Enabled != nil {
		ep.TLSALPN01Enabled = *body.TLSALPN01Enabled
	}
	if body.AllowPrivateHTTP01 != nil {
		ep.AllowPrivateHTTP01 = *body.AllowPrivateHTTP01
	}
	if body.AllowPrivateTLSALPN01 != nil {
		ep.AllowPrivateTLSALPN01 = *body.AllowPrivateTLSALPN01
	}
	if body.HTTP01AllowedCIDRs != nil {
		ep.HTTP01AllowedCIDRs = normalizeStrings(*body.HTTP01AllowedCIDRs)
	}
	if body.TLSALPN01AllowedCIDRs != nil {
		ep.TLSALPN01AllowedCIDRs = normalizeStrings(*body.TLSALPN01AllowedCIDRs)
	}
	if body.ValidityDays != nil {
		ep.ValidityDays = *body.ValidityDays
	}
	if body.MaxOrdersPerHour != nil {
		ep.MaxOrdersPerHour = *body.MaxOrdersPerHour
	}
}

func (s *Server) validateACMEEndpoint(ep *model.ACMEEndpoint) error {
	ep.Name = strings.TrimSpace(ep.Name)
	if ep.Name == "" {
		return i18n.E("api.error.acme_endpoint_name_required")
	}
	if !acme.ValidSlug(ep.Slug) {
		return i18n.E("api.error.acme_endpoint_slug_invalid")
	}
	if existing, err := s.Store.GetACMEEndpointBySlug(ep.Slug); err == nil && existing.ID != ep.ID {
		return i18n.E("api.error.acme_endpoint_slug_in_use", "slug", ep.Slug)
	}
	if ep.CAID <= 0 {
		return i18n.E("api.error.acme_endpoint_ca_required")
	}
	ca, err := s.Store.GetPrivateCA(ep.CAID)
	if err != nil {
		return i18n.E("api.error.private_ca_not_found")
	}
	if err := service.ValidateIssuingCA(ca); err != nil {
		return err
	}
	crlSupported, _, err := service.CARevocationCapabilities(ca)
	if err != nil {
		return err
	}
	if !crlSupported {
		return i18n.E("api.error.acme_endpoint_crl_required")
	}
	if ep.ValidityDays <= 0 {
		ep.ValidityDays = 90
	}
	if ep.ValidityDays < 1 || ep.ValidityDays > 3650 {
		return i18n.E("api.error.acme_endpoint_validity_range")
	}
	if ep.MaxOrdersPerHour <= 0 {
		ep.MaxOrdersPerHour = 60
	}
	if ep.MaxOrdersPerHour < 1 || ep.MaxOrdersPerHour > 10000 {
		return i18n.E("api.error.acme_endpoint_rate_range")
	}
	if !ep.HTTP01Enabled && !ep.DNS01Enabled && !ep.TLSALPN01Enabled {
		return i18n.E("api.error.acme_endpoint_challenge_required")
	}
	if ep.Enabled && len(ep.AllowedDomains) == 0 && !ep.AllowIP {
		return i18n.E("api.error.acme_endpoint_domain_required")
	}
	if ep.AllowIP && len(ep.AllowedIPCIDRs) == 0 {
		return i18n.E("api.error.acme_endpoint_ip_cidr_required")
	}
	for _, domain := range ep.AllowedDomains {
		if !acme.ValidDNSName(domain) {
			return i18n.E("api.error.acme_endpoint_domain_invalid", "domain", domain)
		}
	}
	for _, cidr := range ep.HTTP01AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return i18n.E("api.error.acme_endpoint_cidr_invalid", "cidr", cidr)
		}
	}
	for _, cidr := range ep.TLSALPN01AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return i18n.E("api.error.acme_endpoint_cidr_invalid", "cidr", cidr)
		}
	}
	for _, cidr := range ep.AllowedIPCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return i18n.E("api.error.acme_endpoint_cidr_invalid", "cidr", cidr)
		}
	}
	return nil
}

func (s *Server) listACMEEABCredentials(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if _, err := s.Store.GetACMEEndpoint(id); err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_endpoint_not_found")
		return
	}
	list, err := s.Store.ListACMEEABCredentials(id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) createACMEEABCredential(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ep, err := s.Store.GetACMEEndpoint(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_endpoint_not_found")
		return
	}
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	var body struct {
		Label       string `json:"label"`
		ExpiresDays int    `json:"expires_days"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if body.ExpiresDays < 0 || body.ExpiresDays > 3650 {
		failKey(c, http.StatusBadRequest, "api.error.acme_eab_expiry_range")
		return
	}
	kid, err := acme.NewEABKid()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	secret, err := acme.NewEABSecret()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	publicID, err := acme.NewPublicID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	credential := &model.ACMEEABCredential{
		PublicID:   publicID,
		EndpointID: ep.ID,
		Kid:        kid,
		Label:      strings.TrimSpace(body.Label),
		HMAC:       secret,
	}
	if body.ExpiresDays > 0 {
		expires := time.Now().UTC().AddDate(0, 0, body.ExpiresDays)
		credential.ExpiresAt = &expires
	}
	if err := s.Store.CreateACMEEABCredential(credential); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("acme", ep.ID, "info", "log.acme.eab_created", map[string]any{"kid": kid, "label": credential.Label})
	ok(c, gin.H{
		"id":         credential.ID,
		"kid":        credential.Kid,
		"label":      credential.Label,
		"hmac_key":   secret,
		"expires_at": credential.ExpiresAt,
		"created_at": credential.CreatedAt,
		"hmac_set":   true,
	})
}

func (s *Server) deleteACMEEABCredential(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	credential, err := s.Store.GetACMEEABCredential(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_eab_not_found")
		return
	}
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	if err := s.Store.DeleteACMEEABCredential(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("acme", credential.EndpointID, "warning", "log.acme.eab_deleted", map[string]any{"kid": credential.Kid})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
