package acme

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-jose/go-jose/v4"
)

func (s *Server) handleKeyChange(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.endpoint(w, r)
	if !ok {
		return
	}
	account, outer, ok := s.authenticateAccount(w, r, ep)
	if !ok {
		return
	}
	expectedURL := s.baseURL(r) + r.URL.Path
	inner, err := jose.ParseSignedJSON(string(outer.Payload), allowedJWSAlgorithms)
	if err != nil || len(inner.Signatures) != 1 {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "keyChange payload is not a valid inner JWS")
		return
	}
	protected := inner.Signatures[0].Protected
	if protected.Nonce != "" || protected.KeyID != "" || protected.JSONWebKey == nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "inner JWS must contain jwk and omit kid/nonce")
		return
	}
	url, _ := headerString(protected, "url")
	if url != expectedURL {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "inner JWS url mismatch")
		return
	}
	newJWK := protected.JSONWebKey
	if !newJWK.Valid() || !newJWK.IsPublic() {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "inner JWS jwk must be a public key")
		return
	}
	if problem := checkAlgorithm(newJWK, protected.Algorithm); problem != nil {
		writeProblem(w, problem)
		return
	}
	payload, err := inner.Verify(newJWK)
	if err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "inner JWS signature verification failed")
		return
	}
	var request struct {
		Account string          `json:"account"`
		OldKey  json.RawMessage `json:"oldKey"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		writeProblemType(w, http.StatusBadRequest, ProblemMalformed, "invalid keyChange payload")
		return
	}
	if strings.TrimSpace(request.Account) != account.Kid {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "keyChange account mismatch")
		return
	}
	oldThumbprint, _, err := keyThumbprintFromJSON(request.OldKey)
	if err != nil || oldThumbprint != account.Thumbprint {
		writeProblemType(w, http.StatusUnauthorized, ProblemUnauthorized, "keyChange oldKey mismatch")
		return
	}
	newThumbprint, err := jwkThumbprint(newJWK)
	if err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to compute new key thumbprint")
		return
	}
	if existing, err := s.Store.GetACMEAccountByThumbprint(ep.ID, newThumbprint); err == nil && existing.ID != account.ID {
		w.Header().Set("Location", existing.Kid)
		writeProblemType(w, http.StatusConflict, ProblemMalformed, "new account key is already in use")
		return
	}
	if err := s.Store.UpdateACMEAccountKey(account.ID, mustMarshalJWK(newJWK), newThumbprint); err != nil {
		writeProblemType(w, http.StatusInternalServerError, ProblemServerInternal, "failed to update account key")
		return
	}
	account.PublicJWK = mustMarshalJWK(newJWK)
	account.Thumbprint = newThumbprint
	writeJSON(w, http.StatusOK, accountView{Status: account.Status, Contact: account.Contact})
}
