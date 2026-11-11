package acme

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/store"
)

const maxACMEBody = 64 * 1024

// Server 实现 ACME v2 协议的服务端核心。
type Server struct {
	Store     *store.Store
	BaseURL   string
	Nonces    *NonceStore
	HTTP01    *HTTP01Validator
	DNS01     *DNS01Validator
	TLSALPN01 *TLSALPN01Validator
	Now       func() time.Time

	validationSem  chan struct{}
	accountLimiter *ipRateLimiter
}

// NewServer 创建 ACME 协议服务。
func NewServer(st *store.Store, baseURL string) *Server {
	return &Server{
		Store:          st,
		BaseURL:        strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Nonces:         NewNonceStore(),
		HTTP01:         NewHTTP01Validator(),
		DNS01:          NewDNS01Validator(),
		TLSALPN01:      NewTLSALPN01Validator(),
		Now:            time.Now,
		validationSem:  make(chan struct{}, 8),
		accountLimiter: newIPRateLimiter(30, time.Hour),
	}
}

// Handler 返回 ACME 协议处理器。处理器可挂载在独立端口，也可挂到主路由的 /acme 前缀。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /acme/{slug}/directory", s.handleDirectory)
	mux.HandleFunc("GET /acme/{slug}/new-nonce", s.handleNewNonce)
	mux.HandleFunc("POST /acme/{slug}/new-account", s.handleNewAccount)
	mux.HandleFunc("POST /acme/{slug}/account/{id}", s.handleAccount)
	mux.HandleFunc("POST /acme/{slug}/new-order", s.handleNewOrder)
	mux.HandleFunc("POST /acme/{slug}/order/{id}", s.handleOrder)
	mux.HandleFunc("POST /acme/{slug}/authz/{id}", s.handleAuthorization)
	mux.HandleFunc("POST /acme/{slug}/challenge/{id}", s.handleChallenge)
	mux.HandleFunc("POST /acme/{slug}/finalize/{id}", s.handleFinalize)
	mux.HandleFunc("POST /acme/{slug}/cert/{id}", s.handleCertificate)
	mux.HandleFunc("POST /acme/{slug}/revoke-cert", s.handleRevokeCert)
	mux.HandleFunc("POST /acme/{slug}/key-change", s.handleKeyChange)
	mux.HandleFunc("GET /acme/{slug}/renewal-info/{certID}", s.handleRenewalInfo)
	mux.HandleFunc("GET /pki/{ca}/crl", s.handleCRL)
	mux.HandleFunc("POST /pki/{ca}/ocsp", s.handleOCSP)
	mux.HandleFunc("GET /pki/{ca}/ocsp/{req}", s.handleOCSPGet)
	mux.HandleFunc("/", s.handleNotFound)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if nonce, err := s.Nonces.New(); err == nil {
			w.Header().Set("Replay-Nonce", nonce)
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	base := s.endpointBase(r, ep)
	dir := map[string]any{
		"newNonce":    base + "/new-nonce",
		"newAccount":  base + "/new-account",
		"newOrder":    base + "/new-order",
		"revokeCert":  base + "/revoke-cert",
		"keyChange":   base + "/key-change",
		"renewalInfo": base + "/renewal-info",
		"meta": map[string]any{
			"externalAccountRequired": ep.EABRequired,
		},
	}
	writeJSON(w, http.StatusOK, dir)
}

func (s *Server) handleNewNonce(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.endpoint(w, r); !ok {
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/acme/") {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "ACME resource not found")
		return
	}
	writeProblemType(w, http.StatusNotFound, ProblemMalformed, "not found")
}

func (s *Server) endpoint(w http.ResponseWriter, r *http.Request) (*model.ACMEEndpoint, bool) {
	slug := r.PathValue("slug")
	if !ValidSlug(slug) {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "ACME endpoint not found")
		return nil, false
	}
	ep, err := s.Store.GetACMEEndpointBySlug(slug)
	if err != nil || !ep.Enabled {
		writeProblemType(w, http.StatusNotFound, ProblemMalformed, "ACME endpoint not found")
		return nil, false
	}
	return ep, true
}

func (s *Server) endpointBase(r *http.Request, ep *model.ACMEEndpoint) string {
	return s.baseURL(r) + "/acme/" + ep.Slug
}

func (s *Server) baseURL(r *http.Request) string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.ContentLength > maxACMEBody {
		writeProblemType(w, http.StatusRequestEntityTooLarge, ProblemMalformed, "request body is too large")
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxACMEBody+1))
	if err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "failed to read request body")
		return nil, false
	}
	if len(body) > maxACMEBody {
		writeProblemType(w, http.StatusRequestEntityTooLarge, ProblemMalformed, "request body is too large")
		return nil, false
	}
	return body, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ValidSlug 校验 ACME 端点 URL 段。
func ValidSlug(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			if index == 0 && r == '-' {
				return false
			}
			continue
		}
		return false
	}
	return !strings.HasSuffix(value, "-")
}
