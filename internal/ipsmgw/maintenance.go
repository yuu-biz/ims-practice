package ipsmgw

import (
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the IP-SM-GW tells the maintenance API of its
// node (internal/maintenance), and what it does while the operator has
// it shut down or locked.
//
// Everything the IP-SM-GW keeps past one SIP transaction is in the
// Store (store.go): the deliveries that wait for a delivery report,
// each with its TR1N, and the reg event subscriptions. No piece of that
// is tied to one instance, the way a call is tied to the MMTel AS that
// took its INVITE: a delivery report reaches whichever IP-SM-GW DNS
// leads it to, and so does a NOTIFY, since the Contact of the SUBSCRIBE
// is the name of the IP-SM-GW in DNS and not one node (regevent.go). So
// an instance that the operator takes out of service has to finish only
// what is in its hands at that moment, and takes on nothing new.
//
// While it is not UNLOCKED it goes on with:
//
//   - the MESSAGE that carries a delivery report, which In-Reply-To
//     tells from every other MESSAGE (TS 24.341 5.3.2.4 item d): the
//     RP-ACK or RP-ERROR for an RP-DATA that this network has already
//     sent a UE. It ends a delivery that has begun. A 503 to it would
//     leave the delivery waiting, until TR1N ran out and the SC heard
//     System Failure for a short message that the UE did have;
//   - the NOTIFY of a reg event subscription, which is within the dialog
//     of a SUBSCRIBE that this network sent and carries that SUBSCRIBE
//     on: the first one completes the dialog, and each one says whether
//     the user still has a contact that takes short messages (TS 24.341
//     5.3.3.2). A 503 to it would lose what it says until the next one
//     comes, and leave the HSS with an SMSRegistrationInfo that no
//     longer holds;
//   - every delivery that the SC hands it (Deliver). The SC in this node
//     asks this instance only for what this instance began: the
//     SMS-DELIVER of a short message it took from a UE, the
//     SMS-STATUS-REPORT of a delivery it ended, and the short message of
//     another IMS network that it took in a MESSAGE;
//   - the expiries of TR1N that its sweep claims (claimDue). The SC
//     hears the outcome of every delivery, and a delivery whose report
//     never came has none until a sweep ends it; the claim is this
//     instance's already.
//
// It refuses, with 503 (Service Unavailable) and Retry-After:
//
//   - the third-party REGISTER of a registration or a reregistration
//     (TS 24.341 5.3.3.2). Each would have this instance subscribe to a
//     user's registration state or refresh the subscription, and that
//     subscription is in the Store, where every instance finds it. The
//     third-party REGISTER of a deregistration is refused with the rest,
//     and that refusal loses nothing at all: register answers it with
//     the 200 and does no more, because the S-CSCF ends the reg event
//     subscription with a NOTIFY of its own (TS 24.229 5.4.2.1.2). That
//     NOTIFY finds the subscription in the Store whichever instance it
//     reaches, and is within a dialog, so even this instance serves it
//     while it drains: receiveRegEvent removes the subscription and
//     tells the HSS that the user takes short messages no longer
//     (regevent.go);
//   - the MESSAGE of a short message from a UE, and of a notification
//     that the UE has memory again (TS 24.341 5.3.3.4.1). Each starts
//     the transfer of a short message;
//   - the MESSAGE of a short message that the IP-SM-GW of another IMS
//     network sends to a user of this one (TS 23.204 6.4a step 2). It
//     has begun in that network, but nothing of it has reached this
//     instance's hands, so another instance can take it whole.
//
// A method that the IP-SM-GW does not take at all keeps its 405 (Method
// Not Allowed) whatever the administrative state (OnRequest): the
// method is no more supported out of service than in it, and the next
// instance would answer the same.
//
// The S-CSCF brings the IP-SM-GW its requests by the initial filter
// criteria of the served user, so each 503 goes back to the S-CSCF. It
// sends the request on to the next instance that DNS gave for the
// server name (RFC 3263 4.3), and with none left applies the default
// handling of the criterion: TS 24.229 5.4.1.7 for the third-party
// REGISTER, 5.4.3.2 and 5.4.3.3 for the rest. Every criterion of the
// IP-SM-GW in deploy/compose/hss.yaml says SESSION_CONTINUED, so:
//
//   - a third-party REGISTER that no instance answers leaves the
//     registration as it is (5.4.1.7). For a registration or a
//     reregistration, the user is registered and no IP-SM-GW holds a
//     reg event subscription for it, so the HSS is never told that the
//     user takes short messages; the third-party REGISTER of the next
//     reregistration subscribes. For a deregistration nothing is left
//     undone, since the NOTIFY that ends the subscription does that
//     work;
//   - a MESSAGE of the originating case goes on with the criteria of
//     lower priority, which none of this profile matches, and then to
//     its Request-URI: the PSI of the SC that the UE addressed (TS
//     24.341 5.3.1.2 item a). Nothing in this network answers at that
//     PSI, because the SC sits inside the node of the IP-SM-GW rather
//     than being a node with a name of its own (servicecentre.go), so
//     the S-CSCF translates the number with ENUM, finds no record, and
//     answers 404 (Not Found) (TS 24.229 5.4.3.2 step 10). That 404 is
//     what the UE sees of its short message. TS 24.341 5.3.1.2 gives
//     the SM-over-IP sender no handling of a failure response to that
//     MESSAGE: it takes the submit report that comes in a MESSAGE of
//     its own, and nothing else;
//   - a MESSAGE of the terminating case, the short message of another
//     IMS network, goes on to the recipient's registered contact
//     (5.4.3.3, then 5.4.3.2 step 10 onwards), so the UE gets the
//     RP-DATA of that network with no IP-SM-GW of this one in between.

// role is the name of the IP-SM-GW in the configuration file, and the
// path segment of the maintenance API.
const role = "ipsmgw"

// retryAfter is the Retry-After, in seconds, of the 503 that the
// IP-SM-GW answers while it is not UNLOCKED: how long the client should
// send it no other request (RFC 3261 21.5.4). No specification gives
// the value. It is that of the other roles that refuse a request
// outside a dialog.
const retryAfter = 30

// Function is what the IP-SM-GW tells the maintenance API of its node.
// It cannot work while no instance of the HSS takes its Sh requests,
// which hssAvailable tells: a NOTIFY of a reg event subscription is of
// no use unless the IP-SM-GW can write SMSRegistrationInfo (TS 24.341
// 5.3.3.2), and the SC beside it asks the HSS over Sh which user has
// the number a short message is for, so without the HSS no short
// message is delivered either.
//
// The SC is no part of the operational state here: it runs in this
// process, behind ServiceCentre, so there is no association with it
// whose state could be reported. A node that reached a real SC over SGd
// or MAP would report DISABLED while that link was down, since the
// IP-SM-GW passes every short message either way through it.
//
// Its users are what this instance has in its hands and has not
// finished:
//
//   - each request it is answering, counted from the moment it takes the
//     request on, which is after the refusal of one it will not take
//     (take): the third-party REGISTER until the 200 is written and the
//     Store read, each MESSAGE until the submit report or the delivery
//     report that answers it is done with, and each NOTIFY until the HSS
//     has been told what it said. A request that the IP-SM-GW answers
//     415, 403, 400, 481, 488, 489 or 500 is one user for the moment
//     that answer takes; a method it does not take at all is answered
//     405 before it is counted;
//   - each SUBSCRIBE of its own, and each refresh of one, until it has
//     its final response (regevent.go), so that a subscription is not
//     left without a dialog by a node that stopped while its SUBSCRIBE
//     was out;
//   - each delivery that the SC handed it, from the hand-over itself
//     until the MESSAGE that carries the RP-DATA has its final response
//     (Deliver);
//   - each expiry of TR1N that its sweep has claimed, from the claim
//     itself until the delivery is ended and the SC told (claimDue).
//
// The wait for a delivery report is no user: the delivery and its TR1N
// are in the Store, so the report is served by whichever instance it
// reaches and any instance's sweep ends the delivery when TR1N runs
// out. Stopping this instance loses none of it, and a shut down that
// waited for the reports would hold for TR1N, 64 seconds, over work
// that no longer needs this node.
//
// Everything that is counted ends in the time a few SIP transactions
// take, so a shut down reaches LOCKED quickly. What keeps it from
// reaching it is a steady stream of new work: the sweep goes on
// claiming the expiries of deliveries that other instances sent, as the
// S-CSCF goes on with its own expiries while it drains, and each claim
// is a user for as long as it takes. The timeout_seconds of the shut
// down bounds that, as it does for the HSS.
func (g *IPSMGW) Function(hssAvailable func() bool) maintenance.Function {
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if hssAvailable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(g.inHand.Load()) },
	}
}

// Manage gives the IP-SM-GW the administrative state that the
// maintenance API of its node keeps for it. Until then, and in a node
// without the API, the IP-SM-GW is UNLOCKED.
func (g *IPSMGW) Manage(f *maintenance.ManagedFunction) { g.managed.Store(f) }

// unlocked reports whether the operator lets the IP-SM-GW take new
// work.
func (g *IPSMGW) unlocked() bool {
	f := g.managed.Load()
	return f == nil || f.AdministrativeState() == maintenance.Unlocked
}

// take gives st to handle, unless the operator has the IP-SM-GW out of
// service and st would start something new: then it answers 503
// instead. A request that handle gets is a user of the IP-SM-GW until
// handle returns, so that a shut down waits for it.
func (g *IPSMGW) take(st *sip.ServerTransaction, handle func(*sip.ServerTransaction)) {
	if g.newWorkRefused(st.Request) {
		g.refuseOutOfService(st)
		return
	}
	g.userArrived()
	defer g.userQuit()
	handle(st)
}

// newWorkRefused reports whether the IP-SM-GW refuses req because the
// operator has it out of service and req would start something new. Two
// requests carry earlier work on: a MESSAGE with In-Reply-To, which is
// the delivery report of an RP-DATA that this network sent, and a NOTIFY
// within the dialog of a reg event subscription of this network.
func (g *IPSMGW) newWorkRefused(req *sip.Message) bool {
	if g.unlocked() {
		return false
	}
	switch req.Method {
	case sip.MESSAGE:
		return req.Get("In-Reply-To") == ""
	case sip.NOTIFY:
		return !ims.IsInDialog(req)
	}
	return true
}

// refuseOutOfService answers a request that arrives while the IP-SM-GW
// is not UNLOCKED and would start something new: 503 (Service
// Unavailable) with Retry-After, the time for which the client should
// send this server no other request (RFC 3261 21.5.4), and the Warning
// that says why. The P-Charging-Vector of the request comes back in it
// as in every other response of the IP-SM-GW (TS 24.229 5.7.2).
func (g *IPSMGW) refuseOutOfService(st *sip.ServerTransaction) {
	g.log.Info("request refused while not unlocked", "method", string(st.Request.Method),
		"call_id", st.Request.Get("Call-ID"), "request_uri", st.Request.RequestURI)
	g.respond(st, 503, func(resp *sip.Message) {
		resp.Add("Retry-After", strconv.Itoa(retryAfter))
		resp.Add("Warning", "399 "+g.warningAgent()+" "+strconv.Quote("Out of service"))
	})
}

// warningAgent is the warn-agent of the Warning header field (RFC 3261
// 20.43): the host of the IP-SM-GW's own URI, or the home domain when
// that URI does not parse.
func (g *IPSMGW) warningAgent() string {
	u, err := sip.ParseURI(g.cfg.URI)
	if err != nil {
		return g.cfg.HomeDomain
	}
	return u.Host
}

// userArrived counts a piece of work that the IP-SM-GW has taken on.
func (g *IPSMGW) userArrived() { g.inHand.Add(1) }

// userQuit counts the end of one, which may be the last that a shut
// down waits for: the user quit event of X.731 7.1.3.4.
func (g *IPSMGW) userQuit() {
	g.inHand.Add(-1)
	if f := g.managed.Load(); f != nil {
		f.UserQuit()
	}
}
