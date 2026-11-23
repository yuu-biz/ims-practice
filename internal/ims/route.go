package ims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Locator finds where to send a request for a SIP URI (RFC 3263).
// *sipdns.Resolver is one.
type Locator interface {
	Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error)
}

// Targets is where to send a request for u (RFC 3263 4): every
// transport and address that DNS gives, best first, as
// sip.Stack.SendRequest takes them. The stack sends the request to the
// first and on to the next whenever it fails at one (4.3).
func Targets(ctx context.Context, loc Locator, u sip.URI) ([]sip.Destination, error) {
	transport, _ := u.Params.Get("transport")
	targets, err := loc.Locate(ctx, u.Host, u.Port, sip.Transport(strings.ToUpper(transport)))
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("ims: no address for %s", u)
	}
	return targets, nil
}

// IsInDialog tells a request within a dialog from an initial one: only
// the former has a tag in To (RFC 3261 12.2).
func IsInDialog(req *sip.Message) bool {
	to, err := sip.ParseNameAddr(req.Get("To"))
	if err != nil {
		return false
	}
	_, ok := to.Params.Get("tag")
	return ok
}

// CreatesDialog tells an initial request for a dialog from a request
// for a standalone transaction, two cases that TS 24.229 handles apart,
// such as 5.2.6.3.3 and 5.2.6.3.7 for the P-CSCF. Only the former is
// record-routed: an INVITE creates a dialog (RFC 3261 12), and so does a
// SUBSCRIBE (RFC 6665 3.1.3). A MESSAGE does not (RFC 3428 4), and its
// table of header fields has no Record-Route (RFC 3428 9).
func CreatesDialog(m sip.Method) bool { return m == sip.INVITE || m == sip.SUBSCRIBE }

// Finish sends what Proxy left for the TU to send. After a 2xx to an
// INVITE there is nothing; when the next hop never answered, TS 24.229
// has the CSCFs answer 504 rather than the 408 of RFC 3261 16.8.
//
// A 503 (Service Unavailable) is not passed on: the proxy answers 500
// (Server Internal Error) of its own instead (RFC 3261 16.7 step 6). A
// 503 tells that the server that sent it takes no request now, and the
// stack has already tried every other target of the next hop. Passed
// on, it would tell the same of this proxy, which still serves other
// requests. A UE reads it as said of its P-CSCF: a 503 without
// Retry-After, or with one longer than Timer F, to a request after its
// registration has the UE mark its P-CSCF unavailable and register
// again (TS 24.229 5.1.2A.1.6). The Retry-After of the 503 is about the
// server that sent it, so the 500 does not carry it.
func Finish(st *sip.ServerTransaction, final *sip.Message, err error) {
	switch {
	case err != nil:
		st.Respond(sip.NewResponse(st.Request, 504, sip.NewTag()))
	case final != nil && final.StatusCode == 503:
		st.Respond(sip.NewResponse(st.Request, 500, sip.NewTag()))
	case final != nil:
		st.Respond(final)
	}
}

// StackRef holds the stack of a role. The stack calls the role, and the
// role needs the stack to forward ACKs, so the role is built first and
// its stack set right after; the atomic keeps that hand-over race-free.
type StackRef struct{ p atomic.Pointer[sip.Stack] }

func (r *StackRef) Set(s *sip.Stack) { r.p.Store(s) }

func (r *StackRef) Get() (*sip.Stack, error) {
	if s := r.p.Load(); s != nil {
		return s, nil
	}
	return nil, errors.New("ims: the role's stack is not set yet")
}

// LooseRoute adds lr to a URI, the form every Route and Record-Route of
// this project takes (RFC 3261 19.1.1).
func LooseRoute(uri string) (sip.URI, error) {
	u, err := sip.ParseURI(uri)
	if err != nil {
		return sip.URI{}, err
	}
	u.Params.Set("lr", "")
	return u, nil
}
