package scscf

import (
	"context"
	"crypto/rand"
	"log/slog"
	"slices"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds how the S-CSCF invokes application servers for an
// initial request: step 5 of TS 24.229 5.4.3.2 for the originating
// case and step 4 of 5.4.3.3 for the terminating case. The criteria are
// tried in the order of their priority. The first one that matches
// sends the request to its server, with the S-CSCF's own URI after the
// server's in Route. That URI carries an original dialog identifier
// (5.4.3.4), so that when the server sends the request back, the
// S-CSCF goes on with the next criterion. When no criterion is left,
// the request goes on to its destination.

// odiParam is the URI parameter in which the S-CSCF puts the original
// dialog identifier. TS 24.229 5.4.3.4 leaves the encoding to the
// S-CSCF.
const odiParam = "odi"

// originalDialogLifetime is how long the Store keeps an original dialog
// identifier. It covers the longest wait for the final response of the
// server: Timer F, 32 s, for a MESSAGE, and Timer C, more than 3
// minutes (RFC 3261 16.6 step 11), for an INVITE that rings. A server
// that sends back the request with the same identifier again later
// (5.4.3.4 NOTE) is served within this time too.
const originalDialogLifetime = 4 * time.Minute

// invokeApplicationServers takes the criteria of d after d.Invoked in
// order. The first that matches out sends it to its server. When none is
// left, the request goes on as for a user without services.
func (s *SCSCF) invokeApplicationServers(st *sip.ServerTransaction, out *sip.Message, d OriginalDialog, log *slog.Logger) {
	for i := d.Invoked + 1; i < len(d.Criteria); i++ {
		if !d.Criteria[i].Matches(out, d.SessionCase) {
			continue
		}
		if s.invoke(st, out, d, i, log) {
			return
		}
		// The server failed and its criterion says SESSION_CONTINUED: the
		// evaluation goes on from step 5 (5.4.3.2) or step 4 (5.4.3.3)
		// with the criteria after it.
	}
	switch d.SessionCase {
	case ifc.TerminatingRegistered:
		s.routeTerminating(st, out, d, log)
	case ifc.TerminatingUnregistered:
		s.routeUnregistered(st, out, d, log)
	default:
		s.routeOriginating(st, out, log)
	}
}

// invoke sends out to the server of criterion i (5.4.3.2 step 5 a,
// 5.4.3.3 step 4 b), and passes the server's final answer to st. It
// reports false when the server failed and its criterion lets the
// evaluation go on; out is then as it was.
//
// The default handling applies when no response comes within Timer F
// (Timer B for an INVITE), or when the server answers 408 or 5xx before
// any 1xx and before it sent the request back. SESSION_TERMINATED then
// passes the failure to the user, or 504 (Server Time-out) when no
// response came.
//
// SIMPLIFIED: TS 24.229 5.4.3.2 and 5.4.3.3. A 100 (Trying) does not
// count as the 1xx that turns the default handling off: Proxy keeps it
// to the hop it came over (RFC 3261 16.7 step 5).
func (s *SCSCF) invoke(st *sip.ServerTransaction, out *sip.Message, d OriginalDialog, i int, log *slog.Logger) bool {
	as := d.Criteria[i].ApplicationServer
	d.ID, d.Invoked, d.Returned, d.Expires = rand.Text(), i, false, time.Now().Add(originalDialogLifetime)
	log = log.With("application_server", as.ServerName, "original_dialog", d.ID)
	if err := s.store.PutOriginalDialog(context.Background(), d); err != nil {
		s.storeFailed(st, err, log)
		return true
	}

	toAS := out.Clone()
	server, err := ims.LooseRoute(as.ServerName)
	if err != nil {
		log.Warn("application server URI is not a SIP URI", "error", err.Error())
		s.respond(st, 500, nil)
		return true
	}
	own := s.ownURI()
	own.Params.Set(odiParam, d.ID)
	toAS.Prepend("Route", sip.NameAddr{URI: own.String()}.String())
	toAS.Prepend("Route", sip.NameAddr{URI: server.String()}.String())
	// 5.4.3.2 step 5 b and 5.4.3.3 step 4 c: whom the server serves, and
	// in which session case, so that one server can tell its originating
	// services from its terminating ones (5.7.1.3A). Those steps leave
	// sescase and regstate to operator policy; this S-CSCF always adds
	// them.
	// There is one value, never two (RFC 8498 5).
	toAS.Set("P-Served-User", d.pServedUser().String())
	// Step 13 leaves it to the operator whether a request to an
	// application server in the trust domain is record-routed. This
	// S-CSCF stays on the path of every dialog it creates.
	if ims.CreatesDialog(toAS.Method) {
		sip.AddRecordRoute(toAS, s.ownURI())
	}
	log.Info("invoking the application server", "priority", d.Criteria[i].Priority, "session_case", d.SessionCase.String(), "served_user", d.ServedUser)

	provisional := false
	relay := s.onResponses(st, d.direction())
	targets, err := s.nextHop(toAS)
	var final *sip.Message
	if err == nil {
		final, err = st.ProxyWith(toAS, func(r *sip.Message) {
			provisional = provisional || r.StatusCode < 200
			relay(r)
		}, targets...)
	}
	if unreachable(final, err) && !provisional {
		returned, rerr := s.returned(d.ID)
		if rerr != nil {
			// Without the identifier, the S-CSCF cannot tell the failure
			// of the server from the answer of the session.
			s.storeFailed(st, rerr, log)
			return true
		}
		if !returned {
			log.Info("application server failed", "status", statusOf(final), "error", errorText(err), "default_handling", as.DefaultHandling.String())
			if as.DefaultHandling == ifc.SessionContinued {
				return false
			}
		}
	}
	// Any final response from the server goes to the user, without the
	// criteria of lower priority (5.4.3.2, 5.4.3.3).
	if final != nil {
		relay(final)
	}
	ims.Finish(st, final, err)
	return true
}

// terminating tells the terminating case of d from the originating one,
// that of a call diversion included.
func (d OriginalDialog) terminating() bool {
	return d.SessionCase == ifc.TerminatingRegistered || d.SessionCase == ifc.TerminatingUnregistered
}

// servedUserSessionCases is how P-Served-User writes the session case
// of each kind of filter criteria (5.4.3.2 step 5 b, 5.4.3.3 step 4 c).
var servedUserSessionCases = map[ifc.SessionCase]sip.SessionCase{
	ifc.OriginatingRegistered:   sip.SessionCaseOrig,
	ifc.OriginatingUnregistered: sip.SessionCaseOrig,
	ifc.TerminatingRegistered:   sip.SessionCaseTerm,
	ifc.TerminatingUnregistered: sip.SessionCaseTerm,
	ifc.OriginatingCDIV:         sip.SessionCaseOrigCDIV,
}

// pServedUser is the P-Served-User of the requests that d sends to
// application servers (RFC 5502 6, RFC 8498 6.2).
func (d OriginalDialog) pServedUser() sip.PServedUser {
	return sip.PServedUser{URI: d.ServedUser, SessionCase: servedUserSessionCases[d.SessionCase], RegState: d.RegState}
}

// returned reports whether the server has sent back the request it got
// with the original dialog identifier id.
func (s *SCSCF) returned(id string) (bool, error) {
	d, ok, err := s.store.OriginalDialog(context.Background(), id)
	return ok && d.Returned, err
}

// fromApplicationServer is a request that a server sent back with the
// original dialog identifier that the S-CSCF gave it (5.4.3.2 step 3,
// 5.4.3.3 step 1): the evaluation goes on after the criterion that sent
// the request there, with the list of criteria as it was then.
func (s *SCSCF) fromApplicationServer(st *sip.ServerTransaction, out *sip.Message, id string, log *slog.Logger) {
	log = log.With("original_dialog", id)
	ctx := context.Background()
	d, ok, err := s.store.OriginalDialog(ctx, id)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if !ok {
		// The identifier has expired, or was never given out. Where the
		// evaluation stood is lost.
		log.Info("request with an unknown original dialog identifier")
		s.respond(st, 500, nil)
		return
	}
	d.Returned = true
	if err := s.store.PutOriginalDialog(ctx, d); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	log.Info("request back from the application server", "session_case", d.SessionCase.String(), "served_user", d.ServedUser, "icid", d.ICID)
	if d.terminating() && !sameTarget(out.RequestURI, d) {
		s.retargeted(st, out, d, log)
		return
	}
	s.invokeApplicationServers(st, out, d, log)
}

// retargeted is 5.4.3.3 step 3 when a server has sent the request of the
// terminating case d to someone else, such as the target of a call
// diversion. The S-CSCF stops evaluating d's criteria. NOTE 6 there has
// the criteria decide what follows: when the served user has criteria
// of ORIGINATING_CDIV, the S-CSCF evaluates those for the served user,
// now the diverting user, from step 4B of 5.4.3.2 (option b); with
// none, it routes the request as the originating case does (option a).
// Either way the request then goes to its new target.
func (s *SCSCF) retargeted(st *sip.ServerTransaction, out *sip.Message, d OriginalDialog, log *slog.Logger) {
	if !ifc.HasSessionCase(d.Criteria, ifc.OriginatingCDIV) {
		log.Info("the application server retargeted the request", "request_uri", out.RequestURI)
		s.routeOriginating(st, out, log)
		return
	}
	log.Info("the application server retargeted the request; the originating services after retargeting follow",
		"request_uri", out.RequestURI, "diverting_user", d.ServedUser)
	// The ordered list of 5.4.3.2 step 3 is that of the served user as it
	// was, and the regstate of the terminating case goes on (RFC 8498 5
	// leaves the choice to the S-CSCF). Steps 6 and 9 do not apply: the
	// request came back with an original dialog identifier.
	s.invokeApplicationServers(st, out, OriginalDialog{
		ServedUser:  d.ServedUser,
		SessionCase: ifc.OriginatingCDIV,
		RegState:    d.RegState,
		Criteria:    d.Criteria,
		Aliases:     d.Aliases,
		Invoked:     -1,
		ICID:        d.ICID,
	}, log)
}

// sameTarget is the match of 5.4.3.3 step 3 between the Request-URI of
// a request that comes back and the one that d saved: the same
// canonical form (a), or an alias of the served user (c). This project
// has no GRUUs, so b does not arise.
func sameTarget(uri string, d OriginalDialog) bool {
	if ims.SamePublicIdentity(uri, d.RequestURI) {
		return true
	}
	return slices.ContainsFunc(d.Aliases, func(alias string) bool { return ims.SamePublicIdentity(alias, uri) })
}
