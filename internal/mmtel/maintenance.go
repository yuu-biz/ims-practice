package mmtel

import (
	"net/http"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the MMTel AS tells the maintenance API of its
// node (internal/maintenance), and what it does while the operator has
// it shut down or locked.
//
// The AS is a routeing B2BUA, so the handling of a call lives in the
// instance that took its INVITE: both legs of the call carry that
// instance in their Contact, and a conference answers at a URI that DNS
// leads to the instance that is its focus (mmtel.go). An instance that
// the operator takes out of service therefore goes on with what it has
// begun and takes nothing new.
//
// While it is not UNLOCKED it goes on with:
//
//   - every request within a dialog it holds, so that the calls, their
//     diversions, their holds and the conferences it is the focus of end
//     as they would have, and a subscriber can refresh or end a
//     subscription it has (RFC 6665 4.1.2.3);
//   - an INVITE to the URI of a conference that this instance hosts,
//     from a user that dials in or comes back after leaving (TS 24.147
//     5.3.2.4.1), the INVITE that the filter criteria give the AS for
//     that user's own originating services, which names the same URI,
//     and an initial SUBSCRIBE to the event package of such a
//     conference. None of them is new work: that conference has begun
//     here and lives nowhere else, so an instance that refused them
//     would keep a conference in progress from its own participants;
//   - an OPTIONS outside a dialog, which the UA answers with 200 of its
//     own (RFC 3261 11.2). It asks what the node can do, and the node is
//     alive;
//   - a request of Ut that it has already begun to answer.
//
// It refuses, with 503 (Service Unavailable) and Retry-After:
//
//   - an INVITE outside a dialog that would start a call of its own,
//     which the filter criteria of a served user send it;
//   - an INVITE to the conference factory URI, which would create a
//     conference here (TS 24.147 5.3.2.3.1);
//   - an INVITE or an initial SUBSCRIBE to a conference URI that this
//     instance does not host, which is as new to it as any other
//     session;
//   - a new request of Ut, whose XCAP server runs beside the AS on a
//     listener of its own (Ut).
//
// A MESSAGE, a REFER or a PUBLISH outside a dialog never gets this far:
// the UA answers each with 405 (Method Not Allowed), since the AS takes
// none of them outside a dialog (mmtel.go).
//
// The S-CSCF reaches the AS by the filter criteria of the served user,
// so the 503 goes back to the S-CSCF, which reads it as a failure of
// the server: it sends the INVITE to the next instance that DNS gave it
// for the server name (RFC 3263 4.3), and with none left applies the
// default handling of the criterion (TS 24.229 5.4.3.2, 5.4.3.3).
// SESSION_CONTINUED goes on with the criteria of lower priority, which
// leaves the call without the services of this AS; SESSION_TERMINATED
// ends the call, and the caller gets the 500 that the S-CSCF makes of a
// 503 (RFC 3261 16.7 step 6). TS 24.229 5.4.3.2 NOTE 25A describes this
// case: an S-CSCF that gets no response or a 503 from the AS of a user
// chooses another AS for that user's new sessions, while the AS that
// answered goes on serving the sessions it already holds.
//
// A request that reaches the focus comes by DNS rather than by a filter
// criterion, from a user that dials in or subscribes to the conference
// event package. The refusal of one whose conference this instance does
// not host, because the conference has ended and given its URI up
// (TS 24.147 5.3.2.7), travels back as any other 503: the S-CSCF of
// that user, with no other instance to try, answers 500, as a proxy
// does not pass a 503 on (RFC 3261 16.7 step 6).

// role is the name of the MMTel AS in the configuration file, and the
// path segment of the maintenance API.
const role = "mmtel"

// retryAfter is the Retry-After, in seconds, of the 503 that the AS
// answers while it is not UNLOCKED: how long the client should send it
// no other request (RFC 3261 21.5.4). No specification gives the value.
const retryAfter = 30

// Function is what the MMTel AS tells the maintenance API of its node.
// It cannot work while no instance of the HSS takes its Sh requests,
// which hssAvailable tells: it reads the settings of the served user's
// services from the HSS for every initial INVITE (TS 29.364), and
// without them it serves no call.
//
// Its users are the work it has taken on and not yet finished: the
// INVITEs outside a dialog, one user each, and the requests of Ut it is
// answering. A call that the filter criteria sent it is one, with the
// two legs the B2BUA relays between. A conference counts as the call of
// the user that created it, which lasts as long as the conference does,
// since the conference ends when its creator leaves (TS 24.147 5.3.2.7
// item 3), and the INVITE of each user that dialed into that conference
// counts as one more, for as long as that user is in it. A participant
// that the focus invited is no user of its own: the focus invites it on
// a REFER within the dialog of the participant that asked for it. A
// shut down waits for all of them, so that the operator learns from the
// state of the node when nothing of the AS would be lost by stopping
// it. The requests of Ut are answered out of the HSS and end in a
// moment, so they hold a shut down up no longer than that.
func (as *AS) Function(hssAvailable func() bool) maintenance.Function {
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if hssAvailable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(as.users.Load()) },
	}
}

// Manage gives the AS the administrative state that the maintenance API
// of its node keeps for it. Until then, and in a node without the API,
// the AS is UNLOCKED.
func (as *AS) Manage(m *maintenance.ManagedFunction) { as.managed.Store(m) }

// unlocked reports whether the operator lets the AS take new work.
func (as *AS) unlocked() bool {
	m := as.managed.Load()
	return m == nil || m.AdministrativeState() == maintenance.Unlocked
}

// newSessionRefused reports whether the AS refuses req, a request
// outside any dialog, because the operator has it out of service. Two
// such requests carry on with something that has begun, so the AS
// takes them whatever its administrative state: one to the URI of a
// conference that this instance hosts, and the INVITE of a call that an
// AS diverted (continuesDiversion). Anything else outside a dialog
// would start something new.
func (as *AS) newSessionRefused(req *sip.Message) bool {
	return !as.unlocked() && as.conferenceAt(req.RequestURI) == nil && !continuesDiversion(req)
}

// continuesDiversion reports whether req is the INVITE of a call that
// an AS diverted, which the S-CSCF hands to the AS again for the
// originating services of the diverting user (orig-cdiv, TS 24.229
// 5.4.3.3 step 3). The call began before the diversion: a 503 to it
// would end a call in progress, as the filter criterion of that
// session case may terminate the session when the AS fails.
func continuesDiversion(req *sip.Message) bool {
	served, err := sip.ParsePServedUser(req.Get("P-Served-User"))
	return err == nil && served.SessionCase == sip.SessionCaseOrigCDIV
}

// onCall is UAConfig.OnCall: an INVITE outside a dialog, which starts a
// call of this instance, or joins a conference that it hosts
// (conference.go).
func (as *AS) onCall(in *sip.IncomingCall) {
	if as.newSessionRefused(in.Request) {
		as.refusedCall(in)
		return
	}
	as.userArrived()
	defer as.userQuit()
	as.serve(in)
}

// Ut wraps the XCAP server of Ut, which runs beside the AS on a
// listener of its own, with the drain of the AS: the administrative
// state that the maintenance API keeps for the AS governs Ut too, so
// the role has one state and the operator one request to make.
//
// A request that arrives while the AS is not UNLOCKED gets 503
// (refusedUt), and one that the server has begun to answer is answered:
// it is a user of the AS until then, as a call is, so a shut down waits
// for it.
//
// TS 24.623 gives the XCAP client no handling of a 503 and says nothing
// of an XCAP server that is out of service; of the responses it does
// handle, a 403 is the one that tells a UE to leave the server alone for
// a while (5.3.1.2.2), and that is about a user that may not use XCAP at
// all. So the behaviour here is that of HTTP: the 503 says that this
// server cannot take the request now because of scheduled maintenance,
// and Retry-After says for how long (RFC 9110 15.6.4, 10.2.3).
//
// Draining Ut costs the user nothing that another instance cannot give
// it: the documents live in the HSS, which every instance reads and
// writes, so a user whose request is refused reaches the same documents
// through any other instance of the AS. X.731 has a LOCKED function
// serve no user, and an operator that has locked this one can stop the
// node without cutting a request of Ut short.
func (as *AS) Ut(server http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !as.unlocked() {
			as.refusedUt(w, r)
			return
		}
		as.userArrived()
		defer as.userQuit()
		server.ServeHTTP(w, r)
	})
}

// onSubscribe is UAConfig.OnSubscribe: a SUBSCRIBE to a conference.
// Only an initial one to a conference that this instance does not host
// starts something new; one within the dialog of a subscription
// refreshes or ends it, which the AS serves while it is out of service
// as it serves any request within a dialog.
func (as *AS) onSubscribe(st *sip.ServerTransaction) {
	if !ims.IsInDialog(st.Request) && as.newSessionRefused(st.Request) {
		as.refuse(st)
		return
	}
	as.subscribe(st)
}

// refusedCall answers an INVITE that the AS refuses while it is not
// UNLOCKED (newSessionRefused): 503 (Service Unavailable) with
// Retry-After (RFC 3261 21.5.4).
func (as *AS) refusedCall(in *sip.IncomingCall) {
	as.log.Info("call refused while not unlocked", "call_id", in.Request.Get("Call-ID"), "request_uri", in.Request.RequestURI)
	in.Reject(503, as.refusalFields()...)
}

// refuse answers a request outside a dialog that the AS refuses while
// it is not UNLOCKED, as refusedCall answers an INVITE.
func (as *AS) refuse(st *sip.ServerTransaction) {
	as.log.Info("request refused while not unlocked", "method", string(st.Request.Method), "call_id", st.Request.Get("Call-ID"))
	resp := sip.NewResponse(st.Request, 503, sip.NewTag())
	for _, h := range as.refusalFields() {
		resp.Add(h.Name, h.Value)
	}
	st.Respond(resp)
}

// refusedUt answers a request of Ut that arrives while the AS is not
// UNLOCKED: 503 (Service Unavailable), which says that the server
// cannot take it now because of scheduled maintenance, with Retry-After
// for how long the client should wait (RFC 9110 15.6.4, 10.2.3).
func (as *AS) refusedUt(w http.ResponseWriter, r *http.Request) {
	as.log.Info("Ut request refused while not unlocked", "method", r.Method, "path", r.URL.Path)
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	http.Error(w, "the XCAP server is out of service", http.StatusServiceUnavailable)
}

// refusalFields are the header fields of that 503: Retry-After, the
// time for which the client should send this server no other request,
// and the Warning that says why it was refused.
func (as *AS) refusalFields() []sip.HeaderField {
	return []sip.HeaderField{
		{Name: "Retry-After", Value: strconv.Itoa(retryAfter)},
		as.warning("Out of service"),
	}
}

// userArrived counts a piece of work that the AS has taken on: a call,
// or a request of Ut.
func (as *AS) userArrived() { as.users.Add(1) }

// userQuit counts the end of one, which may be the last that a shut
// down waits for: the user quit event of X.731 7.1.3.4.
func (as *AS) userQuit() {
	as.users.Add(-1)
	if m := as.managed.Load(); m != nil {
		m.UserQuit()
	}
}
