package mrfc

import (
	"log/slog"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/netann"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the MRFC tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// The MRFC is the terminating UA of each INVITE it gets, and that
// dialog lives in the instance that took it: the Contact names this
// instance, and its termination on the MRFP, its timers and its wait
// for the Notify of the end of an announcement are in this process
// (mrfc.go). An instance that the operator takes out of service
// therefore goes on with the dialogs it holds, and takes no new one.
//
// While it is not UNLOCKED it goes on with:
//
//   - every request within a dialog it holds, so that an announcement
//     plays to its end and a party stays in its conference: the
//     re-INVITE of a hold or of a party that leaves and rejoins the mix
//     (TS 24.229 5.8.4.1.1.1), the BYE that stops a play (5.8.3.1.1),
//     and the BYE that the MRFC itself sends when the MRFP reports that
//     the play has ended (5.8.3.2.1);
//   - an OPTIONS outside a dialog, which the UA answers with 200 of its
//     own (RFC 3261 11.2). It asks what the node can do, and the node is
//     alive.
//
// It refuses, with 503 (Service Unavailable) and Retry-After, every
// INVITE outside a dialog: the announcement of RFC 4240 3, and the
// party of a conference of RFC 4240 5, whether or not this instance
// already holds a party of that conference. Unlike the conference of
// the MMTel AS, which is the focus, a conference of the MRFC is tied to
// no instance of it, as long as the instances share one Store: the
// Store names the MRFP and the context that hold the mix, and every
// instance knows every MRFP, so a party that another instance takes
// joins the same mix (mrfc.go). A refusal here then loses nothing that
// another instance cannot give, which is what lets the MRFC refuse what
// the AS would call a dial-in.
//
// Instances that each keep a Store of their own, as MemoryStore is,
// share none of that: the instance that takes the refused party finds
// no media for the conference and has an MRFP make a second context,
// which splits the mix in two (conference.go, attach). A deployment of
// more than one instance therefore keeps the Store outside the
// processes (deploy/redundancy/mrfc.yaml), and where one instance
// serves alone there is no other to take the refused party.
//
// TS 24.229 5.8.1 has the MRFC handle a request that arrives outside a
// dialog addressed to a GRUU of a dialog it holds, such as an INVITE
// with Replaces that takes that dialog over. That request would carry
// on with something begun here, and a drained MRFC would have to take
// it. This MRFC puts the SIP URI of the instance in its Contact and no
// GRUU, and nothing in this network sends it a request outside a dialog
// it has with it, which is the simplification that Listen records. So
// there is no such request to take.
//
// The AS reaches the MRFC by the name in its configuration, which DNS
// resolves, so the 503 goes to the AS: its stack sends the INVITE on to
// the next target that DNS gave for that name and keeps the 503 from
// the AS itself (RFC 3263 4.3, internal/sip/nexttarget.go). With no
// target left the AS sees the 503 and goes on without the MRF: a call
// that was to hear an announcement or a customized alerting tone is
// served without it (internal/mmtel/announcement.go, cat.go), and a
// user that was to join a conference is refused, since a conference has
// no media without a mixer (internal/mmtel/conference.go).

// role is the name of the MRFC in the configuration file, and the path
// segment of the maintenance API.
const role = "mrfc"

// retryAfter is the Retry-After, in seconds, of the 503 that the MRFC
// answers while it is not UNLOCKED: how long the client should send it
// no other request (RFC 3261 21.5.4). No specification gives the value.
// It is that of the other roles that refuse a request outside a dialog.
const retryAfter = 30

// Function is what the MRFC tells the maintenance API of its node. It
// is ENABLED while the node runs: the MRFC holds no association with an
// MRFP that it could report the state of, since every operation of Mp
// is a request of its own to the control API of a media node
// (internal/agw), and an MRFP that does not answer one shows in the
// refusal of that INVITE.
//
// Its users are the dialogs that this instance holds, one for each
// INVITE outside a dialog that it has taken and not yet finished with:
// an announcement until it has played or the AS has ended it, and a
// party of a conference for as long as that party is in the mix. An
// INVITE that the MRFC refuses for the service its Request-URI names,
// or for its SDP, is one for the moment it takes to answer it; one
// whose Request-URI does not parse at all is answered 400 (Bad Request)
// before it is counted (onCall). A shut down waits for all of them, so
// that the operator learns from the state of the node when nothing of
// the MRFC would be lost by stopping it: no announcement is playing,
// and no party is in a mix through this instance. The mixes themselves
// are in the MRFPs and outlive this node.
func (m *MRFC) Function() maintenance.Function {
	return maintenance.Function{
		Role:  role,
		Users: func() int { return int(m.users.Load()) },
	}
}

// Manage gives the MRFC the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the MRFC is UNLOCKED.
func (m *MRFC) Manage(f *maintenance.ManagedFunction) { m.managed.Store(f) }

// unlocked reports whether the operator lets the MRFC take new dialogs.
func (m *MRFC) unlocked() bool {
	f := m.managed.Load()
	return f == nil || f.AdministrativeState() == maintenance.Unlocked
}

// onCall is UAConfig.OnCall: an INVITE outside a dialog, which asks this
// instance for an announcement or for a party of a conference. The
// Request-URI is read first, so that the refusal of an instance out of
// service carries the P-Asserted-Identity of the service that was asked
// for, as every response of the MRFC to an initial INVITE does (TS
// 24.229 5.8.2.1.1.1).
func (m *MRFC) onCall(c *sip.IncomingCall) {
	log := m.log.With("call_id", c.Request.Get("Call-ID"), "request_uri", c.Request.RequestURI)
	u, err := sip.ParseURI(c.Request.RequestURI)
	if err != nil {
		m.refuse(c, sip.URI{}, &netann.Error{Status: 400, Reason: "Bad Request-URI"}, log)
		return
	}
	if !m.unlocked() {
		m.refuseOutOfService(c, u, log)
		return
	}
	m.userArrived()
	defer m.userQuit()
	m.serve(c, u, log)
}

// refuseOutOfService answers an INVITE that arrives while the MRFC is
// not UNLOCKED: 503 (Service Unavailable) with Retry-After (RFC 3261
// 21.5.4) and the Warning that says why.
func (m *MRFC) refuseOutOfService(c *sip.IncomingCall, u sip.URI, log *slog.Logger) {
	log.Info("INVITE refused while not unlocked")
	headers := []sip.HeaderField{
		{Name: "Retry-After", Value: strconv.Itoa(retryAfter)},
		m.warning(399, "Out of service"),
		assertedIdentity(u),
	}
	if err := c.Reject(503, headers...); err != nil {
		log.Warn("refusal not sent", "error", err.Error())
	}
}

// userArrived counts a dialog that the MRFC has taken on.
func (m *MRFC) userArrived() { m.users.Add(1) }

// userQuit counts the end of one, which may be the last that a shut
// down waits for: the user quit event of X.731 7.1.3.4.
func (m *MRFC) userQuit() {
	m.users.Add(-1)
	if f := m.managed.Load(); f != nil {
		f.UserQuit()
	}
}
