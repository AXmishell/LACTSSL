package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

func (s *Server) listPrivateCAs(c *gin.Context) {
	list, err := s.Store.ListPrivateCAs()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) createPrivateCA(c *gin.Context) {
	// RSA 4096 生成开销较大，限制同一客户端的高频创建。
	if allowed, wait := s.limiter.Allow("privateca:create:"+clientRateKey(c), 10, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Name               string `json:"name"`
		CommonName         string `json:"common_name"`
		Organization       string `json:"organization"`
		OrganizationalUnit string `json:"organizational_unit"`
		Country            string `json:"country"`
		State              string `json:"state"`
		Locality           string `json:"locality"`
		Email              string `json:"email"`
		SerialNumber       string `json:"serial_number"`
		KeyType            string `json:"key_type"`
		ValidDays          int    `json:"valid_days"`
		ParentID           int64  `json:"parent_id"`
		Advanced           struct {
			PathLen      *int     `json:"path_len"`
			KeyUsage     []string `json:"key_usage"`
			ExtKeyUsage  []string `json:"ext_key_usage"`
			DNSNames     []string `json:"dns_names"`
			IPAddresses  []string `json:"ip_addresses"`
			PermittedDNS []string `json:"permitted_dns"`
			ExcludedDNS  []string `json:"excluded_dns"`
			PermittedIPs []string `json:"permitted_ips"`
			ExcludedIPs  []string `json:"excluded_ips"`
			CRLURLs      []string `json:"crl_urls"`
			OCSPURLs     []string `json:"ocsp_urls"`
			IssuerURL    string   `json:"issuer_url"`
			PolicyOIDs   []string `json:"policy_oids"`
			CPSURL       string   `json:"cps_url"`
		} `json:"advanced"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}

	request := service.PrivateCARequest{
		Name:               body.Name,
		CommonName:         body.CommonName,
		Organization:       body.Organization,
		OrganizationalUnit: body.OrganizationalUnit,
		Country:            body.Country,
		State:              body.State,
		Locality:           body.Locality,
		Email:              body.Email,
		SerialNumber:       body.SerialNumber,
		KeyType:            body.KeyType,
		ValidDays:          body.ValidDays,
		ParentID:           body.ParentID,
		PathLen:            body.Advanced.PathLen,
		KeyUsage:           body.Advanced.KeyUsage,
		ExtKeyUsage:        body.Advanced.ExtKeyUsage,
		DNSNames:           body.Advanced.DNSNames,
		IPAddresses:        body.Advanced.IPAddresses,
		PermittedDNS:       body.Advanced.PermittedDNS,
		ExcludedDNS:        body.Advanced.ExcludedDNS,
		PermittedIPs:       body.Advanced.PermittedIPs,
		ExcludedIPs:        body.Advanced.ExcludedIPs,
		CRLURLs:            body.Advanced.CRLURLs,
		OCSPURLs:           body.Advanced.OCSPURLs,
		IssuerURL:          body.Advanced.IssuerURL,
		PolicyOIDs:         body.Advanced.PolicyOIDs,
		CPSURL:             body.Advanced.CPSURL,
	}
	parentName := ""
	if body.ParentID > 0 {
		parent, err := s.Store.GetPrivateCA(body.ParentID)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.private_ca_parent_not_found")
			return
		}
		request.ParentCertPEM = parent.CertPEM
		request.ParentKeyPEM = parent.KeyPEM
		parentName = parent.Name
	}

	ca, err := service.GeneratePrivateCA(request)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ca.ParentName = parentName
	if err := s.Store.CreatePrivateCA(ca); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", ca.ID, "success", "log.private_ca.generated", map[string]any{"name": ca.Name, "common_name": ca.CommonName})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.private_ca_generated", nil)})
}

func (s *Server) importPrivateCA(c *gin.Context) {
	var body struct {
		Name     string `json:"name"`
		CertPEM  string `json:"cert_pem"`
		KeyPEM   string `json:"key_pem"`
		ParentID int64  `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ca, err := service.ParseImportedPrivateCA(body.Name, body.CertPEM, body.KeyPEM)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if body.ParentID > 0 {
		parent, err := s.Store.GetPrivateCA(body.ParentID)
		if err != nil {
			failKey(c, http.StatusBadRequest, "api.error.private_ca_parent_not_found")
			return
		}
		if err := service.ValidateChildCA(ca.CertPEM, parent.CertPEM); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
		ca.ParentID = parent.ID
		ca.ParentName = parent.Name
		ca.CAType = "intermediate"
	}
	if err := s.Store.CreatePrivateCA(ca); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", ca.ID, "warning", "log.private_ca.imported", map[string]any{"name": ca.Name, "common_name": ca.CommonName})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.private_ca_imported", nil)})
}

func (s *Server) downloadPrivateCA(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ca, err := s.Store.GetPrivateCA(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.private_ca_not_found")
		return
	}
	kind := c.DefaultQuery("type", "cert")
	if kind == "key" {
		if !s.requireSecondaryVerify(c, secondaryActionPrivateCAKeyExport) {
			return
		}
		user := getUser(c)
		if user == nil {
			failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
			return
		}
		if allowed, wait := s.limiter.Allow("keyexport:"+fmt.Sprintf("%d", user.ID), 30, time.Minute); !allowed {
			rateLimited(c, wait)
			return
		}
	}
	var content, suffix string
	switch kind {
	case "key":
		content, suffix = ca.KeyPEM, "key"
	case "chain":
		var builder strings.Builder
		current := ca
		visited := map[int64]bool{}
		for current != nil && !visited[current.ID] {
			visited[current.ID] = true
			builder.WriteString(strings.TrimSpace(current.CertPEM))
			builder.WriteString("\n")
			if current.ParentID <= 0 {
				break
			}
			parent, err := s.Store.GetPrivateCA(current.ParentID)
			if err != nil {
				break
			}
			current = parent
		}
		content, suffix = builder.String(), "chain.crt"
	default:
		content, suffix = ca.CertPEM, "crt"
	}
	filename := safeDownloadName(ca.CommonName) + "-" + ca.NotAfter.Format("20060102") + "." + suffix
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-pem-file; charset=utf-8", []byte(content))
}

func (s *Server) deletePrivateCA(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ca, err := s.Store.GetPrivateCA(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.private_ca_not_found")
		return
	}
	if err := s.Store.DeletePrivateCA(id); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("ca", id, "warning", "log.private_ca.deleted", map[string]any{"name": ca.Name})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

// safeDownloadName 只保留适合放入 Content-Disposition 的 ASCII 字符。
func safeDownloadName(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		case unicode.IsSpace(r), r == '.':
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		return "private-ca"
	}
	return name
}
