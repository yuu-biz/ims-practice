package scscf

import (
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the S-CSCF counts of the session establishments
// (TS 32.409 4.1.2): the initial INVITEs it receives, and the responses
// that tell that the session got through. The registration procedures
// (4.1.1) are counted in scscf.go, where the S-CSCF reads what it holds
// for the user.
//
// An initial INVITE reaches the S-CSCF once more for every application
// server of the criteria, each time on a server transaction of its own
// (TS 24.229 5.4.3.4), and the responses of those transactions go back
// to the server that sent the request, not to the node that started the
// session. Only the transaction that brought the INVITE first is
// counted, so one session establishment gives one count however many
// servers it visited.

// startedSession counts an initial INVITE of a session establishment in
// direction d, which the S-CSCF has just received (TS 32.409 4.1.2.1).
// Any other initial request sets up no session.
func (s *SCSCF) startedSession(st *sip.ServerTransaction, d ims.SessionDirection) {
	if st.Request.Method == sip.INVITE {
		s.sessions.Started(role, d)
	}
}

// onResponses is what the S-CSCF does with each response that it passes
// back for the request of st, an initial request of the session
// establishment in direction d: the charging of chargeResponses, and
// the counting of the successful session establishment.
//
// The successful establishment is counted on the response that tells of
// it, the first 180 (Ringing) or the 2xx when the session was answered
// without ringing, as that response goes out (TS 32.409 4.1.2.2,
// sip.ServerTransaction.ClaimFirstSuccess). Summed over the
// directions, the count is that measurement, which names no direction.
func (s *SCSCF) onResponses(st *sip.ServerTransaction, d ims.SessionDirection) func(*sip.Message) {
	charge := s.chargeResponses(st.Request, d == ims.OriginatingSession)
	if st.Request.Method != sip.INVITE || s.sentBack(st.Request) {
		return charge
	}
	return func(resp *sip.Message) {
		charge(resp)
		if st.ClaimFirstSuccess(resp) {
			s.sessions.Succeeded(role, d, resp.StatusCode)
		}
	}
}

// direction is the direction of the session establishment that d is:
// the session case tells it, where the Route of a request that an
// application server sent back does not.
func (d OriginalDialog) direction() ims.SessionDirection {
	if d.terminating() {
		return ims.TerminatingSession
	}
	return ims.OriginatingSession
}
