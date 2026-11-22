package sip

import "errors"

// This file holds what the stack does when a request fails at its
// target. DNS gives a client an ordered list of targets for the next
// hop of a request, best first (RFC 3263 4), and 4.3 tells what a
// failure at one of them is: a 503 (Service Unavailable), a transport
// error, or Timer B or Timer F firing before any response, provisional
// or final. The client then sends the same request to the next target,
// with a Via of a new branch, and so in a new client transaction. A
// stateful proxy does the same (RFC 3261 16.6 step 7). The stack does it
// for every TU, a UA as well as a proxy: SendRequest takes the targets,
// and the TU follows one ClientTransaction to its end.
//
// Everything else of a client transaction goes to the target that it
// was sent to: its retransmissions, the ACK for a 300-699 response, and
// a CANCEL (RFC 3263 4). Only the ACK for a 2xx, a transaction of its
// own, finds its target afresh (SendACK).

var errNoTarget = errors.New("sip: no target for the request")

// follow passes the responses of each attempt of ct on to the TU, and
// sends req on to the next of next whenever it fails at a target. The
// TU sees the provisional responses of every target, and then the final
// response or the error of the last target tried. A 503 does not reach
// the TU when the request goes on, and a cancelled request goes no
// further.
//
// SIMPLIFIED: RFC 3261 21.5.4. A server that answers 503 with
// Retry-After asks the client not to send it other requests for that
// time. The stack remembers no server from one request to the next:
// each request starts again at the first target. Only an S-CSCF that
// the operator has shut down or locked answers 503 with Retry-After,
// and a request that goes to it first all the same gets its 503 and
// goes on to the next target, one round trip later.
func (s *Stack) follow(ct *ClientTransaction, req *Message, next []Destination) {
	defer s.wg.Done()
	a := ct.attempt
	for {
		unavailable, responded := ct.pass(a, len(next) > 0)
		failed, why := failedAt(unavailable, a.err, responded)
		if !failed || len(next) == 0 {
			ct.end(unavailable, a.err)
			return
		}
		s.logNextTarget(a.request, a.destination, why, next)
		following, rest, err := s.reach(req, next)
		if err == nil {
			var sent bool
			if sent, err = s.sendOn(ct, following); sent {
				a, next = following, rest
				continue
			}
		}
		switch {
		case err == nil:
			// The TU has cancelled the request.
			ct.end(unavailable, a.err)
		case unavailable != nil:
			// No other target took the request. A 503 is a response
			// from a server, which tells the TU more than a target that
			// could not be reached.
			ct.end(unavailable, nil)
		default:
			// No other target took the request; the last one tells why.
			ct.end(nil, err)
		}
		return
	}
}

// end closes Responses with the outcome at the last target: the 503
// that pass held back, or err.
func (ct *ClientTransaction) end(unavailable *Message, err error) {
	if unavailable != nil {
		ct.deliver(unavailable)
	}
	ct.err = err
	close(ct.responses)
}

// pass passes the responses of a on to the TU until a ends. With hold
// set, a 503 stays back, since the request may yet go on to another
// target; pass returns it. responded tells whether any response came.
func (ct *ClientTransaction) pass(a *attempt, hold bool) (unavailable *Message, responded bool) {
	for resp := range a.responses {
		responded = true
		if resp.StatusCode == 503 && hold {
			unavailable = resp
			continue
		}
		ct.deliver(resp)
	}
	return unavailable, responded
}

// deliver passes resp to the TU. Like the loop, it never waits for the
// TU (responseBuffer).
func (ct *ClientTransaction) deliver(resp *Message) {
	if !queue(ct.responses, resp) {
		ct.stack.log.Warn("dropped response the TU has not read", "status", resp.StatusCode, "cseq", resp.Get("CSeq"))
	}
}

// failedAt tells whether an attempt that ended with unavailable, a 503
// that pass held back, or with err has failed at its target as RFC 3263
// 4.3 counts it, and why. A timeout is a failure only before any
// response: after one, the target has the request.
func failedAt(unavailable *Message, err error, responded bool) (failed bool, why string) {
	switch {
	case unavailable != nil:
		return true, "503 " + unavailable.Reason
	case err == nil, errors.Is(err, ErrStackClosed):
		return false, ""
	case errors.Is(err, ErrTimeout):
		return !responded, err.Error()
	}
	return true, err.Error()
}

// reach prepares req for the first of targets that the stack can send
// it to, and returns its attempt and the targets after it. A target that
// prepare cannot reach, such as one that refuses the TCP connection,
// has failed with a transport error (RFC 3263 4.3), and the next one is
// tried. When none is left, the error is that of the last one.
func (s *Stack) reach(req *Message, targets []Destination) (*attempt, []Destination, error) {
	err := errNoTarget
	for i, target := range targets {
		var out *Message
		var dst Destination
		if out, dst, err = s.prepare(req, target); err == nil {
			return newAttempt(out, dst), targets[i+1:], nil
		}
		if errors.Is(err, ErrStackClosed) {
			break
		}
		s.logNextTarget(req, target, err.Error(), targets[i+1:])
	}
	return nil, nil, err
}

// sendOn starts the client transaction of a, the next attempt of ct,
// unless the TU has cancelled ct. Cancel and sendOn take ct.mu, so that
// a CANCEL goes to the attempt under way. sent tells whether a started.
func (s *Stack) sendOn(ct *ClientTransaction, a *attempt) (sent bool, err error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.cancelled {
		return false, nil
	}
	if err := s.post(startClient{a: a}); err != nil {
		return false, err
	}
	ct.attempt = a
	return true, nil
}

// logNextTarget tells that req failed at target, and goes on to the
// first of next.
func (s *Stack) logNextTarget(req *Message, target Destination, why string, next []Destination) {
	if len(next) == 0 {
		return
	}
	s.log.Info("request sent on to the next target", "method", string(req.Method), "call_id", req.Get("Call-ID"),
		"failed", target.String(), "reason", why, "next", next[0].String())
}
