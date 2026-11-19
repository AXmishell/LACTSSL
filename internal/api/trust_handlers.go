package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/truststore"
)

func (s *Server) listTrustedCAs(c *gin.Context) {
	list, err := s.Store.ListTrustedCAs()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) importTrustedCA(c *gin.Context) {
	var body struct {
		Name    string `json:"name"`
		CertPEM string `json:"cert_pem"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	name := strings.TrimSpace(body.Name)
	if len([]rune(name)) > 128 {
		failKey(c, http.StatusBadRequest, "api.error.trusted_ca_name_invalid")
		return
	}
	certs, err := truststore.ParseCAPEM(body.CertPEM)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	existing, err := s.Store.ListTrustedCAs()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	seen := make(map[string]bool, len(existing)+len(certs))
	for _, ca := range existing {
		seen[strings.ToUpper(ca.Fingerprint)] = true
	}
	items := make([]*model.TrustedCA, 0, len(certs))
	for _, cert := range certs {
		item := truststore.CAFromCertificate(cert, name)
		item.Name = trustedCAName(item.Name, cert.Subject.CommonName, len(certs) > 1)
		if seen[strings.ToUpper(item.Fingerprint)] {
			failKey(c, http.StatusConflict, "api.error.trusted_ca_duplicate", "name", item.Name)
			return
		}
		seen[strings.ToUpper(item.Fingerprint)] = true
		items = append(items, item)
	}
	if err := s.Store.CreateTrustedCAs(items); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	// 信任链文件供部署/通知插件子进程读取，变更后立即重建。
	if err := s.syncTrustBundle(); err != nil {
		for _, item := range items {
			_ = s.Store.DeleteTrustedCA(item.ID)
		}
		failErr(c, http.StatusInternalServerError, i18n.E("api.error.trust_bundle_sync_failed", "error", err.Error()))
		return
	}
	for _, item := range items {
		s.Store.AddLogKey("settings", item.ID, "warning", "log.trusted_ca.imported",
			map[string]any{"name": item.Name, "subject": item.Subject, "fingerprint": item.Fingerprint})
	}
	ok(c, gin.H{
		"message": i18n.T(locale(c), "api.ok.trusted_ca_imported", map[string]any{"count": len(items)}),
		"count":   len(items),
	})
}

func (s *Server) deleteTrustedCA(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	ca, err := s.Store.GetTrustedCA(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.trusted_ca_not_found")
		return
	}
	if err := s.Store.DeleteTrustedCA(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if err := s.syncTrustBundle(); err != nil {
		// 文件仍是旧信任链，恢复数据库记录，避免界面与子进程看到的状态不一致。
		_ = s.Store.CreateTrustedCAs([]*model.TrustedCA{ca})
		failErr(c, http.StatusInternalServerError, i18n.E("api.error.trust_bundle_sync_failed", "error", err.Error()))
		return
	}
	s.Store.AddLogKey("settings", id, "warning", "log.trusted_ca.deleted",
		map[string]any{"name": ca.Name, "subject": ca.Subject, "fingerprint": ca.Fingerprint})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.trusted_ca_deleted", nil)})
}

func (s *Server) downloadTrustBundle(c *gin.Context) {
	data := truststore.BundlePEM(s.Manager.TrustedPEMs())
	c.Header("Content-Disposition", "attachment; filename=\"lactssl-trusted-ca-bundle.pem\"")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-pem-file; charset=utf-8", data)
}

// syncTrustBundle 在数据目录可用时重建信任链文件，并立即刷新进程环境。
// 导入/删除 CA 后无需重启，插件子进程下次启动即可读取新的信任链。
func (s *Server) syncTrustBundle() error {
	if strings.TrimSpace(s.DataDir) == "" {
		return nil
	}
	path, err := s.Manager.SyncTrustBundle(s.DataDir)
	if err != nil || path == "" {
		return err
	}
	if truststore.HasSystemBundle() {
		return truststore.SetEnv(path)
	}
	return nil
}

// trustedCAName 让一次导入多张证书时仍能在列表中区分根 CA 与下级 CA。
func trustedCAName(name, commonName string, multiple bool) string {
	name = strings.TrimSpace(name)
	commonName = strings.TrimSpace(commonName)
	if !multiple || commonName == "" || name == "" || strings.Contains(name, commonName) {
		return name
	}
	return name + " · " + commonName
}
