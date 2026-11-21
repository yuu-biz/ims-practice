package n5

import (
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// ProblemDetails is the body of an error answer (TS 29.571 5.2.4.1),
// sent as application/problem+json. Cause names the application error
// in the words of the API, such as PDU_SESSION_NOT_AVAILABLE.
type ProblemDetails = sbi.ProblemDetails

// InvalidParam names one attribute that made a request fail (TS 29.571
// 5.2.4.6).
type InvalidParam = sbi.InvalidParam

// ProblemError is an error answer: the HTTP status and the
// ProblemDetails of its body. The client returns it for an answer that
// is not a success; a PCF or AF implementation returns it to make the
// handler answer with that status and body.
type ProblemError = sbi.ProblemError[ProblemDetails, *ProblemDetails]

// The application errors of TS 29.514 Table 5.7.3-1 that the IMS
// procedures meet, and the common ones of TS 29.500 Table 5.2.7.2-1
// that the handlers here answer.
const (
	CauseInvalidServiceInformation                = "INVALID_SERVICE_INFORMATION"
	CauseFilterRestrictions                       = "FILTER_RESTRICTIONS"
	CauseRequestedServiceNotAuthorized            = "REQUESTED_SERVICE_NOT_AUTHORIZED"
	CauseRequestedServiceTemporarilyNotAuthorized = "REQUESTED_SERVICE_TEMPORARILY_NOT_AUTHORIZED"
	CauseTemporaryNetworkFailure                  = "TEMPORARY_NETWORK_FAILURE"
	CauseApplicationSessionContextNotFound        = "APPLICATION_SESSION_CONTEXT_NOT_FOUND"
	CausePDUSessionNotAvailable                   = "PDU_SESSION_NOT_AVAILABLE"

	CauseInvalidMsgFormat    = sbi.CauseInvalidMsgFormat
	CauseMandatoryIEMissing  = sbi.CauseMandatoryIEMissing
	CauseMaxJSONSizeExceeded = sbi.CauseMaxJSONSizeExceeded
	CauseSystemFailure       = sbi.CauseSystemFailure
)

// api is Npcf_PolicyAuthorization as package sbi sees it. Its causes
// have the status of TS 29.514 Table 5.7.3-1; package sbi knows those
// of TS 29.500 Table 5.2.7.2-1. A Create answers 303 when a context
// with the same service information exists already (TS 29.514
// 4.2.2.2).
var api = sbi.API[ProblemDetails, *ProblemDetails]{
	Name: "n5",
	CauseStatus: map[string]int{
		CauseInvalidServiceInformation:                http.StatusBadRequest,
		CauseFilterRestrictions:                       http.StatusBadRequest,
		CauseRequestedServiceNotAuthorized:            http.StatusForbidden,
		CauseRequestedServiceTemporarilyNotAuthorized: http.StatusForbidden,
		CauseTemporaryNetworkFailure:                  http.StatusForbidden,
		CauseApplicationSessionContextNotFound:        http.StatusNotFound,
		CausePDUSessionNotAvailable:                   http.StatusInternalServerError,
	},
	AnswersSeeOther: true,
}

// NewProblem returns the error for an application error of the API,
// with the status TS 29.514 Table 5.7.3-1 or TS 29.500 Table
// 5.2.7.2-1 gives it. A cause neither table has gets 500.
func NewProblem(cause, detail string) *ProblemError {
	return api.NewProblem(cause, detail)
}
