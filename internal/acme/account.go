package acme

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

type newAccountRequest struct {
	Contact                []string        `json:"contact"`
	TermsOfServiceAgreed   bool            `json:"termsOfServiceAgreed"`
	OnlyReturnExisting     bool            `json:"onlyReturnExisting"`
	ExternalAccountBinding json.RawMessage `json:"externalAccountBinding"`
}

type accountUpdateRequest struct {
	Contact []string `json:"contact"`
	Status  string   `json:"status"`
}

type accountView struct {
	Status  string   `json:"status"`
	Contact []string `json:"contact,omitempty"`
}

func (s *Server) handleNewAccount(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	if !s.accountLimiter.Allow(s.clientIP(r)) {
		writeProblemType(w, http.StatusTooManyRequests, ProblemRateLimited, "too many account creation attempts")
		return
	}
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	expectedURL := s.endpointBase(r, ep) + "/new-account"
	envelope, problem := parseJWS(body, expectedURL, s.Nonces)
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	jwk := envelope.Protected.JSONWebKey
	if jwk == nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "newAccount requires a jwk header")
		return
	}
	if envelope.Protected.KeyID != "" {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "newAccount must not use a kid header")
		return
	}
	if problem := verifyJWSWithJWK(envelope, jwk); problem != nil {
		writeProblem(w, problem)
		return
	}
	var request newAccountRequest
	if len(envelope.Payload) > 0 {
		if err := json.Unmarshal(envelope.Payload, &request); err != nil {
			writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "invalid newAccount payload")
			return
		}
	}
	contacts, problem := normalizeContacts(request.Contact)
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	thumbprint, err := jwkThumbprint(jwk)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to compute account key thumbprint")
		return
	}

	existing, err := s.Store.GetACMEAccountByThumbprint(ep.ID, thumbprint)
	if err == nil {
		now := s.now().UTC()
		_ = s.Store.TouchACMEAccount(existing.ID, now, now.Add(-time.Minute))
		w.Header().Set("Location", existing.Kid)
		writeJSON(w, http.StatusOK, accountView{Status: existing.Status, Contact: existing.Contact})
		return
	}
	if !store.IsNotFound(err) {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to query account")
		return
	}
	if request.OnlyReturnExisting {
		writeProblemType(w, http.StatusBadRequest, ProblemAccountDoesNotExist, "account does not exist")
		return
	}

	var eab *model.ACMEEABCredential
	if ep.EABRequired {
		eab, problem = s.verifyEAB(request.ExternalAccountBinding, ep, jwk, expectedURL)
		if problem != nil {
			writeProblem(w, problem)
			return
		}
	}

	publicID, err := NewPublicID()
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to generate account ID")
		return
	}
	kid := s.endpointBase(r, ep) + "/account/" + publicID
	account := &model.ACMEAccount{
		PublicID:   publicID,
		EndpointID: ep.ID,
		Kid:        kid,
		PublicJWK:  mustMarshalJWK(jwk),
		Thumbprint: thumbprint,
		Contact:    contacts,
		Status:     "valid",
	}
	eabID := int64(0)
	if eab != nil {
		eabID = eab.ID
	}
	if err := s.Store.CreateACMEAccount(account, eabID); err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemUnauthorized, "failed to create account: "+err.Error())
		return
	}
	w.Header().Set("Location", kid)
	writeJSON(w, http.StatusCreated, accountView{Status: account.Status, Contact: account.Contact})
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, err := s.Store.GetACMEAccountByPublicID(ep.ID, r.PathValue("id"))
	if err != nil {
		writeProblemType(w, http.StatusNotFound, ProblemAccountDoesNotExist, "account not found")
		return
	}
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	envelope, problem := parseJWS(body, account.Kid, s.Nonces)
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	if envelope.Protected.KeyID != account.Kid || envelope.Protected.JSONWebKey != nil {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "account JWS must use the account kid")
		return
	}
	if problem := verifyJWSWithAccount(envelope, account.PublicJWK); problem != nil {
		writeProblem(w, problem)
		return
	}
	now := s.now().UTC()
	_ = s.Store.TouchACMEAccount(account.ID, now, now.Add(-time.Minute))

	if len(envelope.Payload) == 0 {
		writeJSON(w, http.StatusOK, accountView{Status: account.Status, Contact: account.Contact})
		return
	}
	var update accountUpdateRequest
	if err := json.Unmarshal(envelope.Payload, &update); err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "invalid account update payload")
		return
	}
	contacts, problem := normalizeContacts(update.Contact)
	if problem != nil {
		writeProblem(w, problem)
		return
	}
	if len(update.Contact) > 0 {
		if err := s.Store.UpdateACMEAccountContact(account.ID, contacts); err != nil {
			writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to update account contact")
			return
		}
		account.Contact = contacts
	}
	if update.Status != "" {
		if update.Status != "deactivated" {
			writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "unsupported account status")
			return
		}
		if err := s.Store.UpdateACMEAccountStatus(account.ID, "deactivated"); err != nil {
			writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to deactivate account")
			return
		}
		account.Status = "deactivated"
	}
	writeJSON(w, http.StatusOK, accountView{Status: account.Status, Contact: account.Contact})
}

func (s *Server) verifyEAB(raw json.RawMessage, ep *model.ACMEEndpoint, accountJWK *jose.JSONWebKey, expectedURL string) (*model.ACMEEABCredential, *ProblemError) {
	if len(raw) == 0 {
		return nil, newProblemError(ProblemExternalAccountRequired, "external account binding is required", 400)
	}
	jws, err := jose.ParseSignedJSON(string(raw), []jose.SignatureAlgorithm{jose.HS256})
	if err != nil || len(jws.Signatures) != 1 {
		return nil, newProblemError(ProblemMalformed, "invalid external account binding", 400)
	}
	protected := jws.Signatures[0].Protected
	if protected.Algorithm != string(jose.HS256) || protected.KeyID == "" {
		return nil, newProblemError(ProblemMalformed, "invalid external account binding header", 400)
	}
	url, _ := headerString(protected, "url")
	if url != expectedURL {
		return nil, newProblemError(ProblemUnauthorized, "external account binding url mismatch", 403)
	}
	credential, err := s.Store.GetACMEEABCredentialByKid(protected.KeyID)
	if err != nil || credential.EndpointID != ep.ID {
		return nil, newProblemError(ProblemUnauthorized, "unknown external account binding key", 403)
	}
	if credential.UsedAt != nil {
		return nil, newProblemError(ProblemUnauthorized, "external account binding key has already been used", 403)
	}
	if credential.ExpiresAt != nil && !s.now().Before(*credential.ExpiresAt) {
		return nil, newProblemError(ProblemUnauthorized, "external account binding key has expired", 403)
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(credential.HMAC))
	if err != nil || len(key) < 32 {
		return nil, newProblemError(ProblemUnauthorized, "invalid external account binding key", 403)
	}
	payload, err := jws.Verify(key)
	if err != nil {
		return nil, newProblemError(ProblemUnauthorized, "external account binding signature failed", 403)
	}
	thumbprint, _, err := keyThumbprintFromJSON(payload)
	if err != nil {
		return nil, newProblemError(ProblemMalformed, "external account binding payload is not a JWK", 400)
	}
	accountThumbprint, err := jwkThumbprint(accountJWK)
	if err != nil || thumbprint != accountThumbprint {
		return nil, newProblemError(ProblemUnauthorized, "external account binding does not match account key", 403)
	}
	return credential, nil
}

func normalizeContacts(values []string) ([]string, *ProblemError) {
	if len(values) > 10 {
		return nil, newProblemError(ProblemInvalidContact, "too many contact addresses", 400)
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(value), "mailto:") {
			return nil, newProblemError(ProblemInvalidContact, "only mailto contacts are supported", 400)
		}
		address := strings.TrimSpace(value[len("mailto:"):])
		parsed, err := mail.ParseAddress(address)
		if err != nil {
			return nil, newProblemError(ProblemInvalidContact, "invalid contact email", 400)
		}
		normalized := "mailto:" + strings.ToLower(parsed.Address)
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	return out, nil
}

// NewPublicID 生成 ACME 资源使用的随机公开 ID。
func NewPublicID() (string, error) {
	return randomToken(16)
}

// NewEABKid 生成 EAB Key ID。
func NewEABKid() (string, error) {
	value, err := randomToken(16)
	if err != nil {
		return "", err
	}
	return "eab_" + value, nil
}

// NewEABSecret 生成 32 字节 EAB HMAC Key，使用 base64url 无填充编码。
func NewEABSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func mustMarshalJWK(jwk *jose.JSONWebKey) string {
	data, _ := json.Marshal(jwk.Public())
	return string(data)
}
