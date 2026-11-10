package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"

	"github.com/go-jose/go-jose/v4"
)

var allowedJWSAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

type jwsEnvelope struct {
	JWS       *jose.JSONWebSignature
	Protected jose.Header
	Payload   []byte
}

// parseJWS 解析并完成 URL、nonce 和签名结构校验。
func parseJWS(body []byte, expectedURL string, nonces *NonceStore) (*jwsEnvelope, *ProblemError) {
	if len(body) == 0 {
		return nil, newProblemError(ProblemMalformed, "empty JWS body", 400)
	}
	jws, err := jose.ParseSignedJSON(string(body), allowedJWSAlgorithms)
	if err != nil {
		return nil, newProblemError(ProblemMalformed, "invalid JWS: "+err.Error(), 400)
	}
	if len(jws.Signatures) != 1 {
		return nil, newProblemError(ProblemMalformed, "exactly one JWS signature is required", 400)
	}
	protected := jws.Signatures[0].Protected
	if protected.Algorithm == "" {
		return nil, newProblemError(ProblemMalformed, "JWS alg header is required", 400)
	}
	if protected.Nonce == "" {
		return nil, newProblemError(ProblemBadNonce, "JWS nonce header is required", 400)
	}
	url, _ := headerString(protected, "url")
	if url == "" {
		return nil, newProblemError(ProblemMalformed, "JWS url header is required", 400)
	}
	if url != expectedURL {
		return nil, newProblemError(ProblemUnauthorized, "JWS url does not match the request URL", 403)
	}
	if nonces == nil || !nonces.Consume(protected.Nonce) {
		return nil, newProblemError(ProblemBadNonce, "JWS nonce is invalid or already used", 400)
	}
	return &jwsEnvelope{JWS: jws, Protected: protected}, nil
}

func verifyJWSWithJWK(envelope *jwsEnvelope, jwk *jose.JSONWebKey) *ProblemError {
	if jwk == nil || !jwk.Valid() || !jwk.IsPublic() {
		return newProblemError(ProblemMalformed, "JWS jwk must be a valid public key", 400)
	}
	if err := checkAlgorithm(jwk, envelope.Protected.Algorithm); err != nil {
		return err
	}
	payload, err := envelope.JWS.Verify(jwk)
	if err != nil {
		return newProblemError(ProblemMalformed, "JWS signature verification failed", 400)
	}
	envelope.Payload = payload
	return nil
}

func verifyJWSWithAccount(envelope *jwsEnvelope, accountJWK string) *ProblemError {
	var jwk jose.JSONWebKey
	if err := json.Unmarshal([]byte(accountJWK), &jwk); err != nil || !jwk.Valid() {
		return newProblemError(ProblemServerInternal, "account key is invalid", 500)
	}
	if err := checkAlgorithm(&jwk, envelope.Protected.Algorithm); err != nil {
		return err
	}
	payload, err := envelope.JWS.Verify(&jwk)
	if err != nil {
		return newProblemError(ProblemMalformed, "JWS signature verification failed", 400)
	}
	envelope.Payload = payload
	return nil
}

func checkAlgorithm(jwk *jose.JSONWebKey, alg string) *ProblemError {
	allowed := map[string]bool{}
	switch key := jwk.Key.(type) {
	case *rsa.PublicKey:
		if key.N == nil || key.N.BitLen() < 2048 {
			return newProblemError(ProblemBadSignatureAlgorithm, "RSA account key must be at least 2048 bits", 400)
		}
		for _, value := range []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512"} {
			allowed[value] = true
		}
	case *ecdsa.PublicKey:
		switch key.Curve {
		case elliptic.P256():
			allowed["ES256"] = true
		case elliptic.P384():
			allowed["ES384"] = true
		case elliptic.P521():
			allowed["ES512"] = true
		default:
			return newProblemError(ProblemBadSignatureAlgorithm, "unsupported ECDSA curve", 400)
		}
	case ed25519.PublicKey:
		allowed["EdDSA"] = true
	default:
		return newProblemError(ProblemBadSignatureAlgorithm, "unsupported account key type", 400)
	}
	if !allowed[alg] {
		return newProblemError(ProblemBadSignatureAlgorithm, "JWS alg is not valid for the account key", 400)
	}
	return nil
}

func headerString(header jose.Header, name string) (string, bool) {
	value, ok := header.ExtraHeaders[jose.HeaderKey(name)]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func jwkThumbprint(jwk *jose.JSONWebKey) (string, error) {
	sum, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

func keyThumbprintFromJSON(data []byte) (string, *jose.JSONWebKey, error) {
	var jwk jose.JSONWebKey
	if err := json.Unmarshal(data, &jwk); err != nil {
		return "", nil, err
	}
	if !jwk.Valid() || !jwk.IsPublic() {
		return "", nil, errInvalidJWK
	}
	thumbprint, err := jwkThumbprint(&jwk)
	return thumbprint, &jwk, err
}

var errInvalidJWK = &ProblemError{Type: ProblemMalformed, Detail: "invalid public JWK", Status: 400}
