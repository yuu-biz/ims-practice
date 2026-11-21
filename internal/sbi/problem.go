package sbi

import (
	"fmt"
	"net/http"
)

// ProblemDetails is the body of an error answer (TS 29.571 5.2.4.1, on
// RFC 9457), sent as application/problem+json. Cause names the
// application error in the words of the API, such as
// PDU_SESSION_NOT_AVAILABLE or USER_NOT_FOUND.
//
// SIMPLIFIED: TS 29.571 5.2.4.1. The attributes about access tokens,
// the NRF and API versions are left out; nothing here uses the NRF or
// OAuth 2.0.
type ProblemDetails struct {
	Type              string         `json:"type,omitempty"`
	Title             string         `json:"title,omitempty"`
	Status            int            `json:"status,omitempty"`
	Detail            string         `json:"detail,omitempty"`
	Instance          string         `json:"instance,omitempty"`
	Cause             string         `json:"cause,omitempty"`
	InvalidParams     []InvalidParam `json:"invalidParams,omitempty"`
	SupportedFeatures string         `json:"supportedFeatures,omitempty"`
}

// problemDetails gives the code here the ProblemDetails inside the body
// of an error answer. A type that embeds ProblemDetails has it too, so
// it can be the body of a ProblemError.
func (p *ProblemDetails) problemDetails() *ProblemDetails { return p }

// InvalidParam names one attribute that made a request fail (TS 29.571
// 5.2.4.6). Param is a JSON pointer (RFC 6901) into the body.
type InvalidParam struct {
	Param  string `json:"param"`
	Reason string `json:"reason,omitempty"`
}

// The application errors of TS 29.500 Table 5.2.7.2-1 that the
// handlers here answer. Each API has more of its own.
const (
	CauseInvalidMsgFormat    = "INVALID_MSG_FORMAT"
	CauseMandatoryIEMissing  = "MANDATORY_IE_MISSING"
	CauseMaxJSONSizeExceeded = "MAX_JSON_SIZE_EXCEEDED"
	CauseSystemFailure       = "SYSTEM_FAILURE"
)

// commonCauseStatus is the HTTP status that TS 29.500 Table 5.2.7.2-1
// gives each of the common application errors.
var commonCauseStatus = map[string]int{
	CauseInvalidMsgFormat:    http.StatusBadRequest,
	CauseMandatoryIEMissing:  http.StatusBadRequest,
	CauseMaxJSONSizeExceeded: http.StatusRequestEntityTooLarge,
	CauseSystemFailure:       http.StatusInternalServerError,
}

// ProblemBody is what the body of an error answer of an API is:
// ProblemDetails, or a type of the API that embeds it and adds
// attributes beside it (allOf in the OpenAPI, as ExtendedProblemDetails
// of TS 29.562 6.1.6.2.11 does). B is that type and the constraint is
// on *B: Go needs the pointer type named so that the code here can
// change the ProblemDetails inside a B.
type ProblemBody[B any] interface {
	*B
	problemDetails() *ProblemDetails
}

// ProblemError is an error answer: the HTTP status and the body. A
// client gets it for an answer that is not a success; an NF
// implementation returns it to make a handler answer with that status
// and body.
//
// Each API names its instance once with a type alias, such as
//
//	type ProblemError = sbi.ProblemError[ProblemDetails, *ProblemDetails]
//
// and builds its values with the methods of its API value.
type ProblemError[B any, PB ProblemBody[B]] struct {
	Status  int
	Problem B
	// apiName starts the text of Error, such as "n5". A value built
	// with a composite literal has none and says "sbi".
	apiName string
}

func (e *ProblemError[B, PB]) details() *ProblemDetails {
	return PB(&e.Problem).problemDetails()
}

func (e *ProblemError[B, PB]) Error() string {
	name := e.apiName
	if name == "" {
		name = "sbi"
	}
	if cause := e.details().Cause; cause != "" {
		return fmt.Sprintf("%s: %d %s", name, e.Status, cause)
	}
	return fmt.Sprintf("%s: %d %s", name, e.Status, http.StatusText(e.Status))
}

// API is what the code here needs to know of one API: the name its
// errors start with, the status of each of its application errors,
// and whether 303 is one of its answers. B and PB are the body of its
// error answers, as for ProblemError.
type API[B any, PB ProblemBody[B]] struct {
	// Name starts the text of the errors, such as "n5" or "nhss".
	Name string
	// CauseStatus is the HTTP status that the tables of the API give
	// each of its application errors. The common ones of TS 29.500
	// Table 5.2.7.2-1 need not be listed.
	CauseStatus map[string]int
	// AnswersSeeOther makes Send return a 303 as an answer rather than
	// as an error, as N5 needs for a Create of a context that exists
	// already (TS 29.514 4.2.2.2).
	AnswersSeeOther bool
}

// NewProblem returns the error for an application error, with the
// status that CauseStatus or TS 29.500 Table 5.2.7.2-1 gives it. A
// cause neither has gets 500.
func (a API[B, PB]) NewProblem(cause, detail string) *ProblemError[B, PB] {
	status, ok := a.CauseStatus[cause]
	if !ok {
		status, ok = commonCauseStatus[cause]
	}
	if !ok {
		status = http.StatusInternalServerError
	}
	e := &ProblemError[B, PB]{Status: status, apiName: a.Name}
	*e.details() = ProblemDetails{Status: status, Cause: cause, Detail: detail}
	return e
}
