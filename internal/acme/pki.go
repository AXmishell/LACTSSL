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
	revocations, err := s.Store.ListRevocations(caID)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to load revocations")
		return
	}
	entries := make([]service.RevokedCertificateEntry, 0, len(revocations))
	for _, revocation := range revocations {
		serial, ok := new(big.Int).SetString(revocation.Serial, 16)
		if !ok {
			continue
		}
		entries = append(entries, service.RevokedCertificateEntry{
			Serial:    serial,
			RevokedAt: revocation.RevokedAt,
			Reason:    revocation.Reason,
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

// cachedOCSPResponse 返回 60 秒内已签名的 OCSP 响应，避免重复使用 CA 私钥签名。
func (s *Server) cachedOCSPResponse(key string) ([]byte, bool) {
	s.ocspMu.Lock()
	defer s.ocspMu.Unlock()
	entry, ok := s.ocspCache[key]
	if !ok {
		return nil, false
	}
	if !s.now().Before(entry.expires) {
		delete(s.ocspCache, key)
		return nil, false
	}
	return entry.der, true
}

// cacheOCSPResponse 写入 OCSP 响应缓存；超过容量时先清理过期项，必要时整体丢弃。
func (s *Server) cacheOCSPResponse(key string, der []byte, expires time.Time) {
	s.ocspMu.Lock()
	defer s.ocspMu.Unlock()
	if s.ocspCache == nil {
		s.ocspCache = make(map[string]ocspCacheEntry)
	}
	if len(s.ocspCache) >= 4096 {
		now := s.now()
		for itemKey, entry := range s.ocspCache {
			if !now.Before(entry.expires) {
				delete(s.ocspCache, itemKey)
			}
		}
		if len(s.ocspCache) >= 4096 {
			s.ocspCache = make(map[string]ocspCacheEntry)
		}
	}
	s.ocspCache[key] = ocspCacheEntry{der: append([]byte(nil), der...), expires: expires}
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
	revokedAt := s.now()
	reason := 0
	serial := strings.ToUpper(request.SerialNumber.Text(16))
	// 独立吊销表优先：证书行被删除后，OCSP 仍必须返回 revoked。
	if revocation, revoked, lookupErr := s.Store.GetRevocation(caID, serial); lookupErr != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to load revocation")
		return
	} else if revoked {
		status = ocsp.Revoked
		revokedAt = revocation.RevokedAt
		reason = revocation.Reason
	} else if certificate, err := s.Store.GetCertificateBySerial(caID, serial); err == nil {
		// 兼容旧数据：吊销表尚未回填时，以证书行状态为准。
		if certificate.RevokedAt != nil {
			status = ocsp.Revoked
			revokedAt = *certificate.RevokedAt
			reason = certificate.RevokeReason
		} else {
			status = ocsp.Good
		}
	}
	cacheKey := ""
	if status != ocsp.Unknown {
		cacheKey = fmt.Sprintf("%d|%s|%d|%d|%d", caID, request.SerialNumber.Text(16), int(status), revokedAt.Unix(), reason)
		if cached, ok := s.cachedOCSPResponse(cacheKey); ok {
			w.Header().Set("Content-Type", "application/ocsp-response")
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}
	}
	der, err := service.BuildOCSPResponse(ca, request.SerialNumber, status, revokedAt, reason, s.now())
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, err.Error())
		return
	}
	if cacheKey != "" {
		s.cacheOCSPResponse(cacheKey, der, s.now().Add(time.Minute))
	}
	w.Header().Set("Content-Type", "application/ocsp-response")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(der)
}
