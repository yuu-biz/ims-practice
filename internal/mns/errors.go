package mns

// ErrorResponseGet is the body of an error answer to a GET (TS 32.158
// 6.6.3.2; the schema ErrorResponseGet of TS28623_ComDefs.yaml).
//
// The producer reports the first problem it finds, as TS 32.158
// 6.6.3.1 allows, so the type has no otherProblems. Nor does it have
// status: 6.6.3.2 writes it only under the conditions of 6.6.3, where
// problems with different status codes are answered with 207, and one
// problem never is.
type ErrorResponseGet struct {
	Type   ErrorType   `json:"type"`
	Reason ErrorReason `json:"reason,omitempty"`
	// Title is a short summary of the problem that does not change from
	// one occurrence to the next (TS 32.158 6.6.3.1).
	Title string `json:"title,omitempty"`
	// BadQueryParams names the query parameters at fault.
	BadQueryParams []string `json:"badQueryParams,omitempty"`
}

// ErrorResponseDefault is the body of an error answer to a PUT, a
// DELETE or a JSON Merge Patch (TS 32.158 6.6.3.3; the schema
// ErrorResponseDefault of TS28623_ComDefs.yaml). It has no
// otherProblems and no status, as ErrorResponseGet has not, and no
// badObjects, which 6.6.3.3 uses only for the 3GPP JSON Merge Patch.
type ErrorResponseDefault struct {
	Type   ErrorType   `json:"type"`
	Reason ErrorReason `json:"reason,omitempty"`
	Title  string      `json:"title,omitempty"`
	// BadAttributes are pointers to the attributes at fault, relative to
	// the resource, such as "#/attributes/alarmRecords/<id>/ackState".
	BadAttributes []string `json:"badAttributes,omitempty"`
}

// ErrorResponse is the body of an error answer from the recipient of
// a notification, the MnS consumer, to the POST that carried it (the
// default answer of TS28111_FaultNotifications.yaml; the schema
// ErrorResponse of TS28623_ComDefs.yaml). TS 32.158 5.5.4 lets the
// consumer add such a body to the error code it answers with.
type ErrorResponse struct {
	Error ErrorResponseError `json:"error"`
}

// ErrorResponseError is the error property of ErrorResponse.
type ErrorResponseError struct {
	// ErrorInfo says in words what was wrong with the notification.
	ErrorInfo string `json:"errorInfo,omitempty"`
}

// ErrorType is the high-level kind of an error (TS 32.158 6.6.4). The
// OpenAPI types it as any string; the values are those of 6.6.4, each
// with the status code it goes with.
type ErrorType string

const (
	ValidationError        ErrorType = "VALIDATION_ERROR"         // 400
	IENotFound             ErrorType = "IE_NOT_FOUND"             // 400
	ModificationNotAllowed ErrorType = "MODIFICATION_NOT_ALLOWED" // 403
	ServerLimitation       ErrorType = "SERVER_LIMITATION"        // 500
)

// ErrorReason details an ErrorType (TS 32.158 6.6.5). The values are
// those of 6.6.5.2 for a GET, of 6.6.5.3.2 for a JSON Merge Patch and a
// PUT, and of 6.6.5.4 for creating and deleting objects. The comment
// names the ErrorType each goes with.
type ErrorReason string

const (
	QueryParamNamesInvalid   ErrorReason = "QUERY_PARAM_NAMES_INVALID"   // VALIDATION_ERROR
	QueryParamValuesInvalid  ErrorReason = "QUERY_PARAM_VALUES_INVALID"  // VALIDATION_ERROR
	QueryParamsTooComplex    ErrorReason = "QUERY_PARAMS_TOO_COMPLEX"    // SERVER_LIMITATION
	NewAttributeValueInvalid ErrorReason = "NEW_ATTRIBUTE_VALUE_INVALID" // VALIDATION_ERROR
	AttributeNotWritable     ErrorReason = "ATTRIBUTE_NOT_WRITABLE"      // MODIFICATION_NOT_ALLOWED
	AttributeInvariant       ErrorReason = "ATTRIBUTE_INVARIANT"         // MODIFICATION_NOT_ALLOWED
	AttributeNotFound        ErrorReason = "ATTRIBUTE_NOT_FOUND"         // IE_NOT_FOUND
	ObjectDeletionNotAllowed ErrorReason = "OBJECT_DELETION_NOT_ALLOWED" // MODIFICATION_NOT_ALLOWED
)
