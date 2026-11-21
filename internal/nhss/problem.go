package nhss

import (
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// ProblemDetails is the body of an error answer (TS 29.571 5.2.4.1),
// sent as application/problem+json. Cause names the application error,
// such as USER_NOT_FOUND.
type ProblemDetails = sbi.ProblemDetails

// InvalidParam names one attribute that made a request fail (TS 29.571
// 5.2.4.6).
type InvalidParam = sbi.InvalidParam

// ExtendedProblemDetails is the body of the 403 to an S-CSCF
// registration (TS 29.562 6.1.6.2.11): ProblemDetails and, with
// IDENTITY_ALREADY_REGISTERED, the S-CSCF that serves the user, as the
// Server-Name of a SAA does on Cx. The OpenAPI joins the two with
// allOf, so their attributes sit side by side in one JSON object; the
// table of 6.1.6.2.11 draws them as two nested attributes, problemDetails
// and additionalInfo, but the OpenAPI is what goes on the wire. The
// other services answer with plain ProblemDetails, whose JSON is the
// same with AdditionalInfo empty.
type ExtendedProblemDetails struct {
	ProblemDetails
	AdditionalInfo
}

// AdditionalInfo is the extra information of an ExtendedProblemDetails
// (TS 29.562 6.1.6.2.12).
type AdditionalInfo struct {
	ScscfServerName string `json:"scscfServerName,omitempty"`
}

// The application errors of TS 29.562 that the IMS procedures meet,
// and the common ones of TS 29.500 Table 5.2.7.2-1 that the handlers
// here answer.
const (
	// TS 29.562 Table 6.1.7.3-1 (UECM), 6.2.7.3-1 (SDM), 6.3.7.3-1
	// (UEAU): the identity is unknown. DIAMETER_ERROR_USER_UNKNOWN on Cx.
	CauseUserNotFound = "USER_NOT_FOUND"
	// TS 29.562 Table 6.1.7.3-1: the public and the private identity do
	// not belong together. DIAMETER_ERROR_IDENTITIES_DONT_MATCH on Cx.
	// Table 6.1.4.2.2-2 spells it IDENTITIES_DONT_MATCH for Authorize;
	// this package follows the table of application errors.
	CauseIdentitiesDoNotMatch = "IDENTITIES_DO_NOT_MATCH"
	// TS 29.562 Table 6.1.7.3-1: the registration type is not allowed.
	CauseErrorInRegistrationType = "ERROR_IN_REGISTRATION_TYPE"
	// TS 29.562 Table 6.1.7.3-1: another S-CSCF serves the user.
	CauseIdentityAlreadyRegistered = "IDENTITY_ALREADY_REGISTERED"
	// TS 29.562 Table 6.1.7.3-1: the deregistration callback names no
	// context.
	CauseContextNotFound = "CONTEXT_NOT_FOUND"
	// TS 29.562 Table 6.1.4.2.2-2: Authorize found no registered
	// identity, as DIAMETER_ERROR_IDENTITY_NOT_REGISTERED on Cx.
	CauseIdentityNotRegistered = "IDENTITY_NOT_REGISTERED"
	// TS 29.562 Table 6.1.4.2.2-2: the HSS refuses the authorization,
	// for example because of roaming restrictions or barring.
	CauseAuthorizationRejected = "AUTHORIZATION_REJECTED"
	// TS 29.562 Table 6.2.7.3-1: the user has none of the data, for
	// example no S-CSCF name before any registration (6.2.3.13).
	CauseDataNotFound = "DATA_NOT_FOUND"
	// TS 29.562 Table 6.2.7.3-1: the consumer may not read or write the
	// data.
	CauseOperationNotAllowed = "OPERATION_NOT_ALLOWED"
	// TS 29.562 Table 6.3.7.3-1: the user cannot be authenticated with
	// this scheme.
	CauseAuthenticationRejected = "AUTHENTICATION_REJECTED"
	// TS 29.562 Table 6.3.7.3-1: the HSS does not support the scheme.
	// The table of the operation, 6.3.3.2.4.2.2-2, lists it as
	// UNSUPPORTED_SIP_AUTH_SCHEME under 403; this package follows the
	// table of application errors, which gives 501.
	CauseUnsupportedSIPAuthenticationScheme = "UNSUPPORTED_SIP_AUTHENTICATION_SCHEME"

	CauseInvalidMsgFormat    = sbi.CauseInvalidMsgFormat
	CauseMandatoryIEMissing  = sbi.CauseMandatoryIEMissing
	CauseMaxJSONSizeExceeded = sbi.CauseMaxJSONSizeExceeded
	CauseSystemFailure       = sbi.CauseSystemFailure
)

// ProblemError is an error answer: the HTTP status and the problem
// details of its body. The client returns it for an answer that is
// not a success; an HSS or S-CSCF implementation returns it to make
// the handler answer with that status and body.
type ProblemError = sbi.ProblemError[ExtendedProblemDetails, *ExtendedProblemDetails]

// api is the three services of TS 29.562 as package sbi sees them.
// Their causes have the status of the tables above; package sbi knows
// those of TS 29.500 Table 5.2.7.2-1.
var api = sbi.API[ExtendedProblemDetails, *ExtendedProblemDetails]{
	Name: "nhss",
	CauseStatus: map[string]int{
		CauseUserNotFound:                       http.StatusNotFound,
		CauseIdentitiesDoNotMatch:               http.StatusForbidden,
		CauseErrorInRegistrationType:            http.StatusForbidden,
		CauseIdentityAlreadyRegistered:          http.StatusForbidden,
		CauseContextNotFound:                    http.StatusNotFound,
		CauseIdentityNotRegistered:              http.StatusNotFound,
		CauseAuthorizationRejected:              http.StatusForbidden,
		CauseDataNotFound:                       http.StatusNotFound,
		CauseOperationNotAllowed:                http.StatusForbidden,
		CauseAuthenticationRejected:             http.StatusForbidden,
		CauseUnsupportedSIPAuthenticationScheme: http.StatusNotImplemented,
	},
}

// NewProblem returns the error for an application error, with the
// status its table gives. A cause no table here has gets 500.
func NewProblem(cause, detail string) *ProblemError {
	return api.NewProblem(cause, detail)
}
