package acme

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"time"

	"lactssl/internal/model"
)

type renewalInfoView struct {
	SuggestedWindow struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"suggestedWindow"`
}

func (s *Server) handleRenewalInfo(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !ValidSlug(slug) {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "ACME endpoint not found")
		return
	}
	ep, err := s.Store.GetACMEEndpointBySlug(slug)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "ACME endpoint not found")
		return
	}
	certificate, problem := s.certificateForCertID(ep, r.PathValue("certID"))
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	start, end := s.suggestedRenewalWindow(certificate)
	view := renewalInfoView{}
	view.SuggestedWindow.Start = start
	view.SuggestedWindow.End = end
	w.Header().Set("Retry-After", "21600")
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) certificateForCertID(ep *model.ACMEEndpoint, certID string) (*model.Certificate, *ProblemError) {
	aki, serial, err := parseCertID(certID)
	if err != nil {
		return nil, newProblemError(ProblemMalformed, "invalid ARI certificate ID", 400)
	}
	ca, err := s.Store.GetPrivateCA(ep.CAID)
	if err != nil {
		return nil, newProblemError(ProblemMalformed, "issuing CA not found", 404)
	}
	block, _ := pem.Decode([]byte(ca.CertPEM))
	if block == nil {
		return nil, newProblemError(ProblemServerInternal, "invalid CA certificate", 500)
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || len(caCert.SubjectKeyId) == 0 || string(caCert.SubjectKeyId) != string(aki) {
		return nil, newProblemError(ProblemMalformed, "certificate not found", 404)
	}
	certificate, err := s.Store.GetCertificateBySerial(ep.CAID, strings.ToUpper(serial.Text(16)))
	if err != nil {
		return nil, newProblemError(ProblemMalformed, "certificate not found", 404)
	}
	return certificate, nil
}

func parseCertID(value string) ([]byte, *big.Int, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ".", 2)
	if len(parts) != 2 {
		return nil, nil, errInvalidCertID
	}
	aki, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(aki) == 0 {
		return nil, nil, errInvalidCertID
	}
	serialBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(serialBytes) == 0 {
		return nil, nil, errInvalidCertID
	}
	return aki, new(big.Int).SetBytes(serialBytes), nil
}

func certificateID(cert *x509.Certificate) string {
	serial := cert.SerialNumber.Bytes()
	if len(serial) == 0 || serial[0]&0x80 != 0 {
		serial = append([]byte{0}, serial...)
	}
	return base64.RawURLEncoding.EncodeToString(cert.AuthorityKeyId) + "." + base64.RawURLEncoding.EncodeToString(serial)
}

var errInvalidCertID = &ProblemError{Type: ProblemMalformed, Detail: "invalid ARI certificate ID", Status: 400}

func (s *Server) suggestedRenewalWindow(certificate *model.Certificate) (time.Time, time.Time) {
	now := s.now().UTC()
	if certificate.RevokedAt != nil || !now.Before(certificate.NotAfter) {
		return now.Add(-2 * time.Hour), now.Add(-time.Hour)
	}
	lifetime := certificate.NotAfter.Sub(certificate.NotBefore)
	renewBefore := lifetime / 3
	if renewBefore > 30*24*time.Hour {
		renewBefore = 30 * 24 * time.Hour
	}
	if renewBefore < 24*time.Hour {
		renewBefore = 24 * time.Hour
	}
	start := certificate.NotAfter.Add(-renewBefore)
	if start.Before(certificate.NotBefore) {
		start = certificate.NotBefore
	}
	end := start.Add(24 * time.Hour)
	if end.After(certificate.NotAfter) {
		end = certificate.NotAfter
	}
	if !end.After(start) {
		end = start.Add(time.Hour)
	}
	return start, end
}
