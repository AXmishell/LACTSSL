package acme

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ocsp"
	"lactssl/internal/service"
)

const (
	maxOCSPBody  = 64 * 1024
	crlCacheTTL  = 5 * time.Minute
	pkiRateLimit = 600
	pkiRateWin   = time.Minute
)

// crlCacheEntry 缓存同一吊销集合的 CRL，避免每次请求都自增编号并重新签名。
type crlCacheEntry struct {
	der     []byte
	digest  [32]byte
	number  int64
	builtAt time.Time
}

func (s *Server) handleCRL(w http.ResponseWriter, r *http.Request) {
	if !s.pkiLimiter.Allow(s.clientIP(r)) {
		writeProblemType(w, http.StatusTooManyRequests, ProblemRateLimited, "too many PKI requests")
		return
	}
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
	digest := revocationDigest(caID, entries)

	s.crlMu.Lock()
	cached, found := s.crlCache[caID]
	if found && cached.digest == digest && time.Since(cached.builtAt) < crlCacheTTL {
		der := cached.der
		s.crlMu.Unlock()
		writeCRL(w, der)
		return
	}
	number := int64(0)
	if found && cached.digest == digest && cached.number > 0 {
		number = cached.number
	}
	s.crlMu.Unlock()

	if number == 0 {
		number, err = s.Store.NextCRLNumber(caID)
		if err != nil {
			writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to allocate CRL number")
			return
		}
	}
	der, err := service.BuildCRL(ca, entries, number, s.now())
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, err.Error())
		return
	}
	s.crlMu.Lock()
	s.crlCache[caID] = crlCacheEntry{der: der, digest: digest, number: number, builtAt: time.Now()}
	s.crlMu.Unlock()
	writeCRL(w, der)
}

// revocationDigest 生成吊销集合摘要，用于判断 CRL 是否需要新编号。
func revocationDigest(caID int64, entries []service.RevokedCertificateEntry) [32]byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "ca:%d\n", caID)
	for _, entry := range entries {
		serial := ""
		if entry.Serial != nil {
			serial = entry.Serial.Text(16)
		}
		fmt.Fprintf(hash, "%s|%d|%d\n", serial, entry.RevokedAt.Unix(), entry.Reason)
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func writeCRL(w http.ResponseWriter, der []byte) {
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
	if !s.pkiLimiter.Allow(s.clientIP(r)) {
		writeProblemType(w, http.StatusTooManyRequests, ProblemRateLimited, "too many PKI requests")
		return
	}
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
