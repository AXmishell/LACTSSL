package acme

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
)

func (s *Server) handleRevokeCert(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, envelope, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	var request struct {
		Certificate string `json:"certificate"`
		Reason      int    `json:"reason"`
	}
	if err := json.Unmarshal(envelope.Payload, &request); err != nil || strings.TrimSpace(request.Certificate) == "" {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "revokeCert requires a certificate")
		return
	}
	der, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(request.Certificate))
	if err != nil {
		if der, err = base64.URLEncoding.DecodeString(strings.TrimSpace(request.Certificate)); err != nil {
			writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "certificate is not valid base64url")
			return
		}
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "failed to parse certificate")
		return
	}
	serial := strings.ToUpper(certificate.SerialNumber.Text(16))
	stored, err := s.Store.GetCertificateBySerial(ep.CAID, serial)
	if err != nil || stored.ACMEAccountID != account.ID {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "certificate not found")
		return
	}
	storedBlock, _ := pem.Decode([]byte(stored.CertPEM))
	if storedBlock == nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "certificate not found")
		return
	}
	storedCert, err := x509.ParseCertificate(storedBlock.Bytes)
	if err != nil || !bytes.Equal(storedCert.Raw, certificate.Raw) {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "certificate not found")
		return
	}
	if stored.RevokedAt != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	reason := request.Reason
	if reason < 0 || reason > 10 || reason == 7 {
		reason = 0
	}
	if err := s.Store.RevokeCertificate(stored.ID, reason, s.now().UTC()); err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to revoke certificate")
		return
	}
	w.WriteHeader(http.StatusOK)
}
