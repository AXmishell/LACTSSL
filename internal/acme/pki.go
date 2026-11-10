package acme

import (
	"encoding/base64"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/ocsp"
	"lactssl/internal/service"
)

const maxOCSPBody = 64 * 1024

func (s *Server) handleCRL(w http.ResponseWriter, r *http.Request) {
	caID, err := strconv.ParseInt(r.PathValue("ca"), 10, 64)
	if err != nil || caID <= 0 {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "CA not found")
		return
	}
	ca, err := s.Store.GetPrivateCA(caID)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "CA not found")
		return
	}
	certificates, err := s.Store.ListRevokedCertificates(caID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to load revocations")
		return
	}
	entries := make([]service.RevokedCertificateEntry, 0, len(certificates))
	for _, certificate := range certificates {
		if certificate.RevokedAt == nil {
			continue
		}
		serial, ok := new(big.Int).SetString(certificate.Serial, 16)
		if !ok {
			continue
		}
		entries = append(entries, service.RevokedCertificateEntry{
			Serial:    serial,
			RevokedAt: *certificate.RevokedAt,
			Reason:    certificate.RevokeReason,
		})
	}
	number, err := s.Store.NextCRLNumber(caID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to allocate CRL number")
		return
	}
	der, err := service.BuildCRL(ca, entries, number, s.now())
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pkix-crl")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(der)
}

func (s *Server) handleOCSP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxOCSPBody+1))
	if err != nil || len(body) > maxOCSPBody {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ocsp.MalformedRequestErrorResponse)
		return
	}
	s.writeOCSPResponse(w, r, body)
}

func (s *Server) handleOCSPGet(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.PathValue("req"))
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(raw)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ocsp.MalformedRequestErrorResponse)
		return
	}
	s.writeOCSPResponse(w, r, data)
}

func (s *Server) writeOCSPResponse(w http.ResponseWriter, r *http.Request, requestBytes []byte) {
	caID, err := strconv.ParseInt(r.PathValue("ca"), 10, 64)
	if err != nil || caID <= 0 {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "CA not found")
		return
	}
	ca, err := s.Store.GetPrivateCA(caID)
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "CA not found")
		return
	}
	request, err := ocsp.ParseRequest(requestBytes)
	if err != nil {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ocsp.MalformedRequestErrorResponse)
		return
	}
	status := ocsp.Unknown
	var revokedAt = s.now()
	reason := 0
	if certificate, err := s.Store.GetCertificateBySerial(caID, strings.ToUpper(request.SerialNumber.Text(16))); err == nil {
		if certificate.RevokedAt != nil {
			status = ocsp.Revoked
			revokedAt = *certificate.RevokedAt
			reason = certificate.RevokeReason
		} else {
			status = ocsp.Good
		}
	}
	der, err := service.BuildOCSPResponse(ca, request.SerialNumber, status, revokedAt, reason, s.now())
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/ocsp-response")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(der)
}
