package interconnect

import (
	"errors"
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Errors that a Rejection wraps, so that a caller can tell them apart
// with errors.Is.
var (
	// ErrMethodNotAllowed: the method may not cross this II-NNI (TS
	// 29.165 Table 6.1).
	ErrMethodNotAllowed = errors.New("interconnect: the method is not allowed at this II-NNI")
	// ErrExtensionNotAllowed: the request requires an option tag that
	// the profile rejects.
	ErrExtensionNotAllowed = errors.New("interconnect: the request requires an extension the agreement does not allow")
	// ErrOrigFromUntrusted: a request from outside the trust domain
	// asks for originating services (TS 24.229 5.10.3.2).
	ErrOrigFromUntrusted = errors.New("interconnect: an untrusted peer asks for originating services")
)

// Rejection is a request that the IBCF answers itself instead of
// forwarding it (TS 24.229 5.10.1: the IBCF may reject a request by
// local policy with a 4xx).
type Rejection struct {
	StatusCode int
	// Headers go into the response, such as Unsupported in a 420 (RFC
	// 3261 8.2.2.3) or Allow in a 405 (RFC 3261 21.4.6).
	Headers []sip.HeaderField
	// Err tells why.
	Err error
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("%d %s: %v", r.StatusCode, sip.ReasonPhrase(r.StatusCode), r.Err)
}

func (r *Rejection) Unwrap() error { return r.Err }

// Response builds the response to req that carries the rejection.
func (r *Rejection) Response(req *sip.Message, toTag string) *sip.Message {
	resp := sip.NewResponse(req, r.StatusCode, toTag)
	resp.Headers = append(resp.Headers, r.Headers...)
	return resp
}

func reject(statusCode int, err error, headers ...sip.HeaderField) *Rejection {
	return &Rejection{StatusCode: statusCode, Headers: headers, Err: err}
}
