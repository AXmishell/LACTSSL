package acme

import (
	"encoding/json"
	"net/http"
)

// RFC 8555 标准错误类型。
const (
	ProblemMalformed               = "urn:ietf:params:acme:error:malformed"
	ProblemBadNonce                = "urn:ietf:params:acme:error:badNonce"
	ProblemBadSignatureAlgorithm   = "urn:ietf:params:acme:error:badSignatureAlgorithm"
	ProblemAccountDoesNotExist     = "urn:ietf:params:acme:error:accountDoesNotExist"
	ProblemExternalAccountRequired = "urn:ietf:params:acme:error:externalAccountRequired"
	ProblemInvalidContact          = "urn:ietf:params:acme:error:invalidContact"
	ProblemUnsupportedIdentifier   = "urn:ietf:params:acme:error:unsupportedIdentifier"
	ProblemUnauthorized            = "urn:ietf:params:acme:error:unauthorized"
	ProblemNotImplemented          = "urn:ietf:params:acme:error:notImplemented"
	ProblemServerInternal          = "urn:ietf:params:acme:error:serverInternal"
	ProblemConnection              = "urn:ietf:params:acme:error:connection"
	ProblemDNS                     = "urn:ietf:params:acme:error:dns"
	ProblemIncorrectResponse       = "urn:ietf:params:acme:error:incorrectResponse"
	ProblemOrderNotReady           = "urn:ietf:params:acme:error:orderNotReady"
	ProblemBadCSR                  = "urn:ietf:params:acme:error:badCSR"
	ProblemRateLimited             = "urn:ietf:params:acme:error:rateLimited"
	ProblemRejectedIdentifier      = "urn:ietf:params:acme:error:rejectedIdentifier"
	ProblemAlreadyReplaced         = "urn:ietf:params:acme:error:alreadyReplaced"
)

// Problem 是 ACME 协议错误响应体。
type Problem struct {
	Type        string    `json:"type"`
	Detail      string    `json:"detail,omitempty"`
	Status      int       `json:"status,omitempty"`
	Subproblems []Problem `json:"subproblems,omitempty"`
}

// ProblemError 是可在验证器与协议层之间传递的错误。
type ProblemError struct {
	Type   string
	Detail string
	Status int
}

func (e *ProblemError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Detail
	}
	return e.Type
}

func newProblemError(typ, detail string, status int) *ProblemError {
	if status == 0 {
		status = http.StatusBadRequest
	}
	return &ProblemError{Type: typ, Detail: detail, Status: status}
}

func writeProblem(w http.ResponseWriter, err *ProblemError) {
	status := err.Status
	if status == 0 {
		status = http.StatusBadRequest
	}
	body, _ := json.Marshal(Problem{Type: err.Type, Detail: err.Detail, Status: status})
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeProblemType(w http.ResponseWriter, status int, typ, detail string) {
	writeProblem(w, newProblemError(typ, detail, status))
}
