package sip

import (
	"strings"
	"time"
)

// RemoveFirst deletes the first value of the named header field and
// keeps the rest. A proxy pops its own Via from a response this way
// (RFC 3261 16.7 step 3), and the Route that named it from a request
// (16.4).
func (m *Message) RemoveFirst(name string) {
	name = expandCompactForm(name)
	i := m.index(name)
	if i < 0 {
		return
	}
	if isListHeader(name) {
		if elements := splitList(m.Headers[i].Value); len(elements) > 1 {
			m.Headers[i].Value = strings.Join(elements[1:], ", ")
			return
		}
	}
	m.Headers = append(m.Headers[:i], m.Headers[i+1:]...)
}

// Proxy forwards out, which the TU built from st.Request, to the first
// of targets in a new client transaction on the same stack, the way a
// stateful proxy does (RFC 3261 16.6 and 16.7). targets are those that
// RFC 3263 gives for the next hop, best first, and the stack sends out
// on to the next of them when it fails at one (16.6 step 7, RFC 3263
// 4.3; Stack.SendRequest). Every response loses this proxy's Via.
// Provisional responses other than 100 go to st right away, and so does
// every 2xx to an INVITE (16.7 step 5), until the client transaction
// ends. While both transactions wait in Accepted, that is how
// retransmissions of the 2xx, and the 2xx of other branches of a fork,
// get through (RFC 6026 7.1, 7.2).
//
// Proxy returns the final response that the TU still has to send, so
// that the TU can inspect or change it before it calls st.Respond: the
// final response to a non-INVITE request, or the 300-699 response to an
// INVITE. With only one branch, that response is the best one of 16.7
// step 6. After a 2xx there is nothing left to send, and Proxy returns
// nil and a nil error.
//
// When no final response arrives from the last target tried, Proxy
// returns ErrTimeout after Timer B or Timer F, or the error that kept
// the request from being sent there. RFC 3261 would answer 408 for the
// first (16.7 step 6) and, counting the error as a 503 (16.9), 500 for
// the second; the IMS roles answer as TS 24.229 says instead, which is
// why the choice is left to the TU.
//
// While it waits for the final response, Proxy cancels the branch when
// a CANCEL for st arrives (16.10), with the Reason of that CANCEL (RFC
// 3326 2). For a request other than INVITE, the CANCEL goes on only
// while the branch is pending, with a provisional response and no final
// one (16.7 step 10). For an INVITE, Proxy also runs Timer C
// (16.6 step 11, 16.8): when Timer C fires, the branch is cancelled if
// a provisional response has arrived, and otherwise Proxy returns a 408
// as if the branch had sent one.
func (st *ServerTransaction) Proxy(out *Message, targets ...Destination) (*Message, error) {
	return st.ProxyWith(out, nil, targets...)
}

// ProxyWith is Proxy with a hook: relay sees, and may change, every
// response that Proxy relays upstream by itself, the provisional ones
// and the 2xx to an INVITE. A P-CSCF rewrites the SDP of those answers
// there, before they leave.
func (st *ServerTransaction) ProxyWith(out *Message, relay func(*Message), targets ...Destination) (*Message, error) {
	if relay == nil {
		relay = func(*Message) {}
	}
	ct, err := st.stack.SendRequest(out, targets...)
	if err != nil {
		return nil, err
	}
	invite := out.Method == INVITE
	var timerC *time.Timer
	var timerCFired <-chan time.Time
	if invite {
		timerC = time.NewTimer(st.stack.cfg.TimerC)
		defer timerC.Stop()
		timerCFired = timerC.C
	}
	cancelled := st.Cancelled()
	var final *Message
	answered, provisional := false, false
	for {
		select {
		case resp, ok := <-ct.Responses():
			if !ok {
				switch {
				case answered:
					return nil, nil
				case final != nil:
					return final, nil
				}
				return nil, ct.Err()
			}
			resp = resp.Clone()
			resp.RemoveFirst("Via")
			switch code := resp.StatusCode; {
			case code == 100:
				// 16.7 step 5: a 100 (Trying) is hop by hop.
				provisional = true
			case code < 200:
				provisional = true
				if invite {
					// 16.7 step 2.
					timerC.Reset(st.stack.cfg.TimerC)
				}
				relay(resp)
				st.Respond(resp)
			case code < 300 && invite:
				answered = true
				timerC.Stop()
				timerCFired = nil
				relay(resp)
				st.Respond(resp)
			default:
				final = resp
			}
		case <-cancelled:
			cancelled = nil
			ct.Cancel(ReasonFields(st.CancelRequest())...)
		case <-timerCFired:
			timerCFired = nil
			if !provisional {
				// The branch ends here, so it goes to no other target; a
				// provisional response that still comes gets the CANCEL.
				ct.Cancel()
				return NewResponse(st.Request, 408, NewTag()), nil
			}
			ct.Cancel()
		}
	}
}
