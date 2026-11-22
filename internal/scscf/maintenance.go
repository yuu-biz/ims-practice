package scscf

import (
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the S-CSCF tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// The instances of an S-CSCF share the state of the users, so an
// instance that the operator takes out of service moves no user: it
// refuses the requests that would start something new, which the
// I-CSCF or the P-CSCF then sends to the next instance (RFC 3263 4.3),
// and goes on with what it has begun.

// retryAfter is the Retry-After, in seconds, of the 503 that the S-CSCF
// answers while it is not UNLOCKED: how long the client should send it
// no other request (RFC 3261 21.5.4). No specification gives the value.
const retryAfter = 30

// Function is what the S-CSCF tells the maintenance API of its node. It
// cannot work while no instance of the HSS takes its Cx requests, which
// hssAvailable tells, and its users are the transactions it is serving:
// a shut down waits for them to end.
func (s *SCSCF) Function(hssAvailable func() bool) maintenance.Function {
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if hssAvailable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(s.transactions.Load()) },
	}
}

// Manage gives the S-CSCF the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the S-CSCF is UNLOCKED.
func (s *SCSCF) Manage(m *maintenance.ManagedFunction) { s.managed.Store(m) }

// unlocked reports whether the operator lets the S-CSCF take new work.
func (s *SCSCF) unlocked() bool {
	m := s.managed.Load()
	return m == nil || m.AdministrativeState() == maintenance.Unlocked
}

// carriesOn reports whether req goes on with what the S-CSCF has begun:
// a request within a dialog, or one that an application server sends
// back with the original dialog identifier. The S-CSCF serves those
// while it is shut down or locked, so that the sessions and the
// services in progress end as they would have.
func (s *SCSCF) carriesOn(req *sip.Message) bool {
	return ims.IsInDialog(req) || s.sentBack(req)
}

// refuse answers a request that would start something new while the
// S-CSCF is not UNLOCKED: 503 (Service Unavailable) with Retry-After
// (RFC 3261 21.5.4). The client sends the request to the next instance
// (RFC 3263 4.3).
func (s *SCSCF) refuse(st *sip.ServerTransaction) {
	s.log.Info("refused while not unlocked", "method", string(st.Request.Method), "call_id", st.Request.Get("Call-ID"))
	s.respond(st, 503, func(r *sip.Message) { r.Add("Retry-After", strconv.Itoa(retryAfter)) })
}

// transactionEnded counts the end of a transaction, which may be the
// last that a shut down waits for.
func (s *SCSCF) transactionEnded() {
	s.transactions.Add(-1)
	if m := s.managed.Load(); m != nil {
		m.UserQuit()
	}
}
