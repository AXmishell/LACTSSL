package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

// caConfigView 是权威CA页面返回的证书颁发机构信息。
type caConfigView struct {
	Value      string `json:"value"`
	Label      string `json:"label"`
	URL        string `json:"url"`
	EAB        bool   `json:"eab"`
	Desc       string `json:"desc"`
	EABKid     string `json:"eab_kid"`
	EABHMACSet bool   `json:"eab_hmac_set"`
	IsDefault  bool   `json:"is_default"`
}

func (s *Server) caConfigs(c *gin.Context) {
	defaultCA := s.setting("acme.ca", "letsencrypt")
	current := locale(c)
	list := make([]caConfigView, 0, len(service.CADefs))
	for _, ca := range service.LocalizeCADefs(current) {
		kid, _ := s.Store.GetSetting(service.CAEABKidSettingKey(ca.Value))
		hmac, _ := s.Store.GetSecret(service.CAEABHMACSettingKey(ca.Value))
		list = append(list, caConfigView{
			Value:      ca.Value,
			Label:      ca.Label,
			URL:        ca.URL,
			EAB:        ca.EAB,
			Desc:       ca.Desc,
			EABKid:     kid,
			EABHMACSet: hmac != "",
			IsDefault:  ca.Value == defaultCA,
		})
	}
	ok(c, list)
}

func (s *Server) updateCAConfig(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionACMEWrite) {
		return
	}
	ca, found := service.CAInfo(c.Param("value"))
	if !found {
		failKey(c, http.StatusNotFound, "api.error.ca_not_found")
		return
	}
	current := locale(c)
	ca = ca.Localized(current)
	if !ca.EAB {
		failKey(c, http.StatusBadRequest, "api.error.ca_no_eab", "ca", ca.Label)
		return
	}
	var body struct {
		EABKid  string `json:"eab_kid"`
		EABHMAC string `json:"eab_hmac"`
		Clear   bool   `json:"clear"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}

	kidKey := service.CAEABKidSettingKey(ca.Value)
	hmacKey := service.CAEABHMACSettingKey(ca.Value)
	oldKid, _ := s.Store.GetSetting(kidKey)
	oldHMAC, _ := s.Store.GetSecret(hmacKey)

	if body.Clear {
		_ = s.Store.DeleteSetting(kidKey)
		_ = s.Store.DeleteSetting(hmacKey)
		_ = s.Store.DeleteSetting(service.CAAccountKeySettingKey(ca.Value))
		s.Store.AddLogKey("settings", 0, "warning", "log.ca.eab_cleared", map[string]any{"ca": ca.Label})
		ok(c, gin.H{"message": i18n.T(current, "api.ok.eab_cleared", nil)})
		return
	}

	kid := strings.TrimSpace(body.EABKid)
	hmac := strings.TrimSpace(body.EABHMAC)
	if kid == "" {
		failKey(c, http.StatusBadRequest, "api.error.eab_kid_required")
		return
	}
	if hmac == "" && oldHMAC == "" {
		failKey(c, http.StatusBadRequest, "api.error.eab_hmac_required")
		return
	}
	if err := s.Store.SetSetting(kidKey, kid); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.save_eab_kid_failed")
		return
	}
	if hmac != "" {
		if err := s.Store.SetSecret(hmacKey, hmac); err != nil {
			failKey(c, http.StatusInternalServerError, "api.error.save_eab_hmac_failed")
			return
		}
	}
	// EAB 凭据变更后原 ACME 账户无法继续复用，清除账户密钥以便重新注册。
	if kid != oldKid || (hmac != "" && hmac != oldHMAC) {
		_ = s.Store.DeleteSetting(service.CAAccountKeySettingKey(ca.Value))
	}
	s.Store.AddLogKey("settings", 0, "info", "log.ca.eab_updated", map[string]any{"ca": ca.Label})
	ok(c, gin.H{"message": i18n.T(current, "api.ok.eab_saved", nil)})
}
