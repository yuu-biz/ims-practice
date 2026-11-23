package icscf

import (
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the I-CSCF tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// The I-CSCF keeps nothing from one request to the next, and it does
// not record-route, so no later request of a dialog comes back to it.
// An instance that the operator takes out of service therefore has
// nothing of its own to carry on with: it refuses every request it
// receives, and the sender tries the next I-CSCF that DNS gave it (RFC
// 3263 4.3). A P-CSCF with no I-CSCF left for a REGISTER answers the UE
// 504 (TS 24.229 5.2.2.1 item 7). An S-CSCF or an IBCF with no I-CSCF
// left for an initial request answers 500, as a proxy does not pass a
// 503 on (RFC 3261 16.7 step 6, ims.Finish).
//
// A CANCEL for an INVITE that the instance is still proxying is not a
// new request: it matches the server transaction of that INVITE, which
// the stack cancels without asking the I-CSCF. Only a CANCEL that
// matches no transaction reaches OnCANCEL, and that one the I-CSCF
// forwards while it is shut down as it does otherwise, since no
// transaction of its own holds it back.

// retryAfter is the Retry-After, in seconds, of the 503 that the
// I-CSCF answers while it is not UNLOCKED: how long the client should
// send it no other request (RFC 3261 21.5.4). No specification gives
// the value.
const retryAfter = 30

// Function is what the I-CSCF tells the maintenance API of its node. It
// cannot work while no instance of the HSS takes its Cx requests, which
// hssAvailable tells: it asks the HSS which S-CSCF serves the user of a
// REGISTER (UAR, TS 29.228 6.1.1) and where the callee of an initial
// request is (LIR, 6.1.4). Its users are the transactions it is
// serving: a shut down waits for them to end.
func (i *ICSCF) Function(hssAvailable func() bool) maintenance.Function {
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if hssAvailable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(i.transactions.Load()) },
	}
}

// Manage gives the I-CSCF the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the I-CSCF is UNLOCKED.
func (i *ICSCF) Manage(m *maintenance.ManagedFunction) { i.managed.Store(m) }

// unlocked reports whether the operator lets the I-CSCF take requests.
func (i *ICSCF) unlocked() bool {
	m := i.managed.Load()
	return m == nil || m.AdministrativeState() == maintenance.Unlocked
}

// refuse answers a request that arrives while the I-CSCF is not
// UNLOCKED: 503 (Service Unavailable) with Retry-After (RFC 3261
// 21.5.4). The sender tries the next I-CSCF (RFC 3263 4.3).
func (i *ICSCF) refuse(st *sip.ServerTransaction) {
	i.log.Info("refused while not unlocked", "method", string(st.Request.Method), "call_id", st.Request.Get("Call-ID"))
	resp := sip.NewResponse(st.Request, 503, sip.NewTag())
	resp.Add("Retry-After", strconv.Itoa(retryAfter))
	st.Respond(resp)
}

// transactionEnded counts the end of a transaction, which may be the
// last that a shut down waits for.
func (i *ICSCF) transactionEnded() {
	i.transactions.Add(-1)
	if m := i.managed.Load(); m != nil {
		m.UserQuit()
	}
}
