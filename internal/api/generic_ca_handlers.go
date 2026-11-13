package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/acme"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
)

type acmeCABody struct {
	Name         string `json:"name"`
	DirectoryURL string `json:"directory_url"`
	Protocol     string `json:"protocol"`
	EABRequired  bool   `json:"eab_required"`
	EABKid       string `json:"eab_kid"`
	EABHMAC      string `json:"eab_hmac"`
}

func (s *Server) listACMECAs(c *gin.Context) {
	list, err := s.Store.ListACMECAs()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) createACMECA(c *gin.Context) {
	var body acmeCABody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	ca := &model.ACMECA{
		Name:         strings.TrimSpace(body.Name),
		DirectoryURL: strings.TrimSpace(body.DirectoryURL),
		Protocol:     strings.ToLower(strings.TrimSpace(body.Protocol)),
		EABRequired:  body.EABRequired,
		EABKid:       strings.TrimSpace(body.EABKid),
		EABHMAC:      strings.TrimSpace(body.EABHMAC),
	}
	if err := s.validateACMECA(ca, false); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	publicID, err := acme.NewPublicID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ca.PublicID = publicID
	if err := s.Store.CreateACMECA(ca); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", ca.ID, "info", "log.ca.acme_ca_created", map[string]any{"name": ca.Name})
	ok(c, ca)
}

func (s *Server) updateACMECA(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	existing, err := s.Store.GetACMECA(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_ca_not_found")
		return
	}
	var body acmeCABody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	hmac := strings.TrimSpace(body.EABHMAC)
	ca := &model.ACMECA{
		ID:           existing.ID,
		PublicID:     existing.PublicID,
		Name:         strings.TrimSpace(body.Name),
		DirectoryURL: strings.TrimSpace(body.DirectoryURL),
		Protocol:     strings.ToLower(strings.TrimSpace(body.Protocol)),
		EABRequired:  body.EABRequired,
		EABKid:       strings.TrimSpace(body.EABKid),
		EABHMAC:      hmac,
	}
	if err := s.validateACMECA(ca, existing.EABHMACSet); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.UpdateACMECA(ca, hmac); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	// 目录地址、协议或 EAB 凭据变化后，原账户密钥不再可靠，清理后下次签发会重新注册。
	eabChanged := existing.EABRequired != ca.EABRequired || existing.EABKid != ca.EABKid ||
		(hmac != "" && hmac != existing.EABHMAC)
	if existing.DirectoryURL != ca.DirectoryURL || existing.Protocol != ca.Protocol || eabChanged {
		_ = s.Store.DeleteSetting(service.CAAccountKeySettingKey(service.ACMECAValue(ca.ID)))
	}
	s.Store.AddLogKey("ca", ca.ID, "info", "log.ca.acme_ca_updated", map[string]any{"name": ca.Name})
	updated, err := s.Store.GetACMECA(ca.ID)
	if err != nil {
		ok(c, ca)
		return
	}
	updated.EABHMAC = ""
	ok(c, updated)
}

func (s *Server) deleteACMECA(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ca, err := s.Store.GetACMECA(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_ca_not_found")
		return
	}
	if s.setting("acme.ca", "letsencrypt") == service.ACMECAValue(id) {
		failKey(c, http.StatusConflict, "api.error.acme_ca_is_default")
		return
	}
	if err := s.Store.DeleteACMECA(id); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	_ = s.Store.DeleteSetting(service.CAAccountKeySettingKey(service.ACMECAValue(id)))
	s.Store.AddLogKey("ca", id, "warning", "log.ca.acme_ca_deleted", map[string]any{"name": ca.Name})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func (s *Server) validateACMECA(ca *model.ACMECA, hmacSet bool) error {
	if ca.Name == "" {
		return i18n.E("api.error.acme_ca_name_required")
	}
	if len(ca.Name) > 80 {
		return i18n.E("api.error.acme_ca_name_too_long")
	}
	if ca.DirectoryURL == "" {
		return i18n.E("api.error.acme_ca_url_required")
	}
	parsed, err := url.Parse(ca.DirectoryURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return i18n.E("api.error.acme_ca_url_invalid")
	}
	if ca.Protocol == "" {
		ca.Protocol = "v2"
	}
	if ca.Protocol != "v1" && ca.Protocol != "v2" {
		return i18n.E("api.error.acme_ca_protocol_invalid")
	}
	if ca.Protocol == "v1" && ca.EABRequired {
		return i18n.E("api.error.acme_ca_v1_eab_unsupported")
	}
	if ca.EABRequired && ca.EABKid == "" {
		return i18n.E("api.error.acme_ca_eab_kid_required")
	}
	if ca.EABRequired && ca.EABHMAC == "" && !hmacSet {
		return i18n.E("api.error.acme_ca_eab_hmac_required")
	}
	existing, err := s.Store.GetACMECAByName(ca.Name)
	if err == nil && existing.ID != ca.ID {
		return i18n.E("api.error.acme_ca_name_exists")
	}
	if err != nil && !store.IsNotFound(err) {
		return err
	}
	return nil
}
