package sip

import (
	"errors"
	"time"
)

// This file holds the four transaction state machines of RFC 3261 17:
// INVITE client (17.1.1, Figure 5), non-INVITE client (17.1.2, Figure
// 6), INVITE server (17.2.1, Figure 7), and non-INVITE server (17.2.2,
// Figure 8). The INVITE machines follow RFC 6026, which redraws Figures
// 5 and 7 with the Accepted state. Like internal/diameter/fsm.go, each Transition is a
// pure function: it never touches the network or a clock. The stack in
// stack.go feeds it events and carries out the actions it returns.
//
// Each machine knows whether its transport is reliable, such as TCP,
// or not, such as UDP. Over a reliable transport the transaction
// retransmits nothing: Timers A, E and G are not started, and Timers D, I, J
// and K, which wait for retransmissions to die out, are zero (RFC 3261
// 17.1.1.2, 17.1.2.2, 17.2.1, 17.2.2; RFC 6026 8.4). Timers B, F, H, L
// and M are the same for every transport.

// TransactionState is a state of a transaction state machine.
type TransactionState int

const (
	Trying TransactionState = iota
	Calling
	Proceeding
	Completed
	Confirmed // an INVITE server transaction after the ACK
	Accepted  // RFC 6026: an INVITE transaction after a 2xx
	Terminated
)

func (s TransactionState) String() string {
	switch s {
	case Trying:
		return "Trying"
	case Calling:
		return "Calling"
	case Proceeding:
		return "Proceeding"
	case Completed:
		return "Completed"
	case Confirmed:
		return "Confirmed"
	case Accepted:
		return "Accepted"
	case Terminated:
		return "Terminated"
	}
	return "unknown"
}

// Timers are the base values that the transaction timers derive from
// (RFC 3261 17.1.1.1, 17.1.2.2, Appendix A).
type Timers struct {
	T1 time.Duration // RTT estimate
	T2 time.Duration // maximum retransmit interval for non-INVITE requests
	T4 time.Duration // maximum time a message stays in the network
	// TransactionTimeout, when set, is Timer B and Timer F in place of
	// 64*T1 (RFC 3261 17.1.1.2, 17.1.2.2). An operator shortens it so
	// that a request to a server that has stopped goes on to the next
	// server sooner than 32 s (RFC 3263 4.3). TS 24.229 too lets a node
	// wait less than Timer B and Timer F before it turns to another
	// server: the I-CSCF before it selects another S-CSCF (5.3.1.3
	// NOTE 2), and the S-CSCF before it takes a P-CSCF for gone
	// (5.4.3.3 NOTE 24).
	TransactionTimeout time.Duration
}

// transactionTimeout is Timer B and Timer F.
func (t Timers) transactionTimeout() time.Duration {
	if t.TransactionTimeout > 0 {
		return t.TransactionTimeout
	}
	return 64 * t.T1
}

// DefaultTimers are the defaults of RFC 3261 Appendix A.
func DefaultTimers() Timers {
	return Timers{T1: 500 * time.Millisecond, T2: 4 * time.Second, T4: 5 * time.Second}
}

// Timer names a transaction timer (RFC 3261 Appendix A; RFC 6026 8.11
// adds L and M).
type Timer int

const (
	TimerA Timer = iota // INVITE client: request retransmit interval
	TimerB              // INVITE client: transaction timeout, 64*T1
	TimerD              // INVITE client: wait for response retransmissions
	TimerE              // non-INVITE client: request retransmit interval
	TimerF              // non-INVITE client: transaction timeout, 64*T1
	TimerG              // INVITE server: response retransmit interval
	TimerH              // INVITE server: wait for the ACK, 64*T1
	TimerI              // INVITE server: wait for ACK retransmissions, T4
	TimerJ              // non-INVITE server: wait for request retransmissions, 64*T1
	TimerK              // non-INVITE client: wait for response retransmissions, T4
	TimerL              // INVITE server: wait for INVITE retransmissions after a 2xx, 64*T1
	TimerM              // INVITE client: wait for more 2xx responses, 64*T1
	// TimerTrying is not in Appendix A. It is the 200 ms after which an
	// INVITE server transaction sends 100 (Trying) itself, because the
	// TU has not answered (RFC 3261 17.2.1).
	TimerTrying
	// TimerAfterCancel is not in Appendix A either. It is the 64*T1
	// after a CANCEL within which the INVITE's final response should
	// come; then the client gives the INVITE up (RFC 3261 9.1).
	TimerAfterCancel
)

func (t Timer) String() string {
	return [...]string{"A", "B", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "Trying", "after CANCEL"}[t]
}

// tryingDelay is the 200 ms of RFC 3261 17.2.1. It does not scale with
// T1.
const tryingDelay = 200 * time.Millisecond

// timerD is at least 32 s over an unreliable transport: the client
// cannot know the T1 of the server whose Timer H it has to outlast (RFC
// 6026 8.4, replacing the end of RFC 3261 17.1.1.2).
func timerD(t Timers, reliable bool) time.Duration {
	if reliable {
		return 0
	}
	return max(32*time.Second, 64*t.T1)
}

// waitT4 is Timers I and K: T4 over an unreliable transport, zero over
// a reliable one (RFC 3261 17.1.2.2, 17.2.1).
func waitT4(t Timers, reliable bool) time.Duration {
	if reliable {
		return 0
	}
	return t.T4
}

// timerJ is 64*T1 over an unreliable transport and zero over a reliable
// one (RFC 3261 17.2.2).
func timerJ(t Timers, reliable bool) time.Duration {
	if reliable {
		return 0
	}
	return 64 * t.T1
}

// ErrTimeout is what a client transaction reports when Timer B, Timer F
// or TimerAfterCancel fires before a final response arrives. RFC 3261
// 8.1.3.1: the TU treats it as a 408 (Request Timeout).
var ErrTimeout = errors.New("sip: transaction timed out")

// ErrNoACK is what an INVITE server transaction reports when Timer H
// fires: the ACK for its 300-699 response never came (RFC 3261 17.2.1).
var ErrNoACK = errors.New("sip: no ACK for the final response")

// TransactionEvent is an input to a transaction state machine.
type TransactionEvent interface{ eventName() string }

type (
	// ResponseReceived is a response that matched a client transaction
	// (RFC 3261 17.1.3).
	ResponseReceived struct{ Response *Message }
	// RequestRetransmitted is a request that matched an existing server
	// transaction (RFC 3261 17.2.3).
	RequestRetransmitted struct{}
	// ACKReceived is an ACK that matched an INVITE server transaction
	// (RFC 3261 17.2.3).
	ACKReceived struct{ ACK *Message }
	// ResponseFromTU is a response that the TU passes to a server
	// transaction.
	ResponseFromTU struct{ Response *Message }
	// TimerFired reports that a timer set by StartTimer expired.
	TimerFired struct{ Timer Timer }
	// TransportError reports that the transport failed to send (RFC
	// 3261 17.1.4, 17.2.4).
	TransportError struct{ Err error }
	// CancelFromTU is the TU asking for its INVITE to be cancelled. It is
	// not in Figure 5, but RFC 3261 9.1 ties the CANCEL to the INVITE
	// client transaction's state: it may only go out once a provisional
	// response has arrived, it is pointless after a final one, and the
	// transaction gives up if no final response follows it within 64*T1.
	CancelFromTU struct{}
)

func (ResponseReceived) eventName() string     { return "response" }
func (RequestRetransmitted) eventName() string { return "request retransmission" }
func (ACKReceived) eventName() string          { return "ACK" }
func (ResponseFromTU) eventName() string       { return "response from TU" }
func (e TimerFired) eventName() string         { return "Timer " + e.Timer.String() }
func (TransportError) eventName() string       { return "transport error" }
func (CancelFromTU) eventName() string         { return "CANCEL from TU" }

// TransactionAction is an output of a transaction state machine.
type TransactionAction interface{ isTransactionAction() }

type (
	// SendRequest passes the client transaction's request to the
	// transport, the first time or as a retransmission.
	SendRequest struct{}
	// SendACK passes the ACK for a 300-699 Response to the transport.
	// The stack builds it from the INVITE and the response (RFC 3261
	// 17.1.1.3).
	SendACK struct{ Response *Message }
	// SendCancel starts a CANCEL for the INVITE, in a non-INVITE client
	// transaction of its own (RFC 3261 9.1).
	SendCancel struct{}
	// SendResponse passes a response to the transport.
	SendResponse struct{ Response *Message }
	// PassToTU hands a response to the TU of a client transaction.
	PassToTU struct{ Response *Message }
	// PassACKToTU hands an ACK to the TU of an INVITE server transaction
	// in Accepted (RFC 6026 7.1).
	PassACKToTU struct{ ACK *Message }
	// InformTU tells the TU that the transaction failed: ErrTimeout, or
	// the transport error.
	InformTU struct{ Err error }
	// StartTimer starts the named timer. A transaction runs several
	// timers at once; an expired timer that no longer matters in the
	// current state is ignored by Transition.
	StartTimer struct {
		Timer    Timer
		Duration time.Duration
	}
)

func (SendRequest) isTransactionAction()  {}
func (SendACK) isTransactionAction()      {}
func (SendCancel) isTransactionAction()   {}
func (SendResponse) isTransactionAction() {}
func (PassToTU) isTransactionAction()     {}
func (PassACKToTU) isTransactionAction()  {}
func (InformTU) isTransactionAction()     {}
func (StartTimer) isTransactionAction()   {}

// InviteClientState is everything an INVITE client transaction
// remembers.
type InviteClientState struct {
	State    TransactionState
	Timers   Timers
	Reliable bool
	// IntervalA is what Timer A was last set to, so that the next
	// interval can double from it.
	IntervalA time.Duration
	// CancelRequested is set by CancelFromTU. In Calling the CANCEL is
	// still waiting for a provisional response; from Proceeding on it
	// has been sent.
	CancelRequested bool
}

// StartInviteClient enters Calling: the INVITE goes to the transport,
// Timer B starts at 64*T1 unless TransactionTimeout says otherwise, and
// over an unreliable transport Timer A starts at T1 (RFC 3261
// 17.1.1.2).
func StartInviteClient(t Timers, reliable bool) (InviteClientState, []TransactionAction) {
	s := InviteClientState{State: Calling, Timers: t, Reliable: reliable, IntervalA: t.T1}
	actions := []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerB, Duration: t.transactionTimeout()},
	}
	if !reliable {
		actions = append(actions, StartTimer{Timer: TimerA, Duration: t.T1})
	}
	return s, actions
}

// InviteClientTransition is Figure 5 as RFC 6026 8.4 redraws it. An
// event with no arrow out of the current state is ignored.
func InviteClientTransition(s InviteClientState, e TransactionEvent) (InviteClientState, []TransactionAction) {
	switch s.State {
	case Calling, Proceeding:
		switch e := e.(type) {
		case ResponseReceived:
			switch code := e.Response.StatusCode; {
			case code < 200:
				actions := []TransactionAction{PassToTU{Response: e.Response}}
				if s.State == Calling && s.CancelRequested {
					actions = append(actions, s.sendCancel()...)
				}
				s.State = Proceeding
				return s, actions
			case code < 300:
				// The TU acknowledges a 2xx itself, with a request that has
				// no transaction (RFC 3261 13.2.2.4).
				s.State = Accepted
				return s, []TransactionAction{
					PassToTU{Response: e.Response},
					StartTimer{Timer: TimerM, Duration: 64 * s.Timers.T1},
				}
			default:
				s.State = Completed
				return s, []TransactionAction{
					PassToTU{Response: e.Response},
					SendACK{Response: e.Response},
					StartTimer{Timer: TimerD, Duration: timerD(s.Timers, s.Reliable)},
				}
			}
		case TimerFired:
			if e.Timer == TimerAfterCancel {
				s.State = Terminated
				return s, []TransactionAction{InformTU{Err: ErrTimeout}}
			}
			// Retransmission and timeout stop once a provisional response
			// has arrived (RFC 3261 17.1.1.2).
			if s.State != Calling {
				break
			}
			switch e.Timer {
			case TimerA:
				// Unlike Timer E, Timer A keeps doubling without a cap.
				s.IntervalA *= 2
				return s, []TransactionAction{
					SendRequest{},
					StartTimer{Timer: TimerA, Duration: s.IntervalA},
				}
			case TimerB:
				s.State = Terminated
				return s, []TransactionAction{InformTU{Err: ErrTimeout}}
			}
		case CancelFromTU:
			if s.CancelRequested {
				break
			}
			s.CancelRequested = true
			if s.State == Proceeding {
				return s, s.sendCancel()
			}
		case TransportError:
			s.State = Terminated
			return s, []TransactionAction{InformTU{Err: e.Err}}
		}
	case Completed:
		switch e := e.(type) {
		case ResponseReceived:
			// A retransmitted final response gets the ACK again, and the
			// TU does not see it.
			if e.Response.StatusCode >= 300 {
				return s, []TransactionAction{SendACK{Response: e.Response}}
			}
		case TimerFired:
			if e.Timer == TimerD {
				s.State = Terminated
			}
		case TransportError:
			s.State = Terminated
			return s, []TransactionAction{InformTU{Err: e.Err}}
		}
	case Accepted:
		switch e := e.(type) {
		case ResponseReceived:
			// A retransmitted 2xx, or a 2xx from another branch of a fork:
			// the TU acknowledges each one.
			if code := e.Response.StatusCode; code >= 200 && code < 300 {
				return s, []TransactionAction{PassToTU{Response: e.Response}}
			}
		case TimerFired:
			if e.Timer == TimerM {
				s.State = Terminated
			}
		}
	}
	return s, nil
}

// sendCancel sends the CANCEL and starts waiting for the INVITE's final
// response. A server that answers the CANCEL also answers the INVITE
// with 487 (RFC 3261 9.2), but an RFC 2543 server does not, and the
// CANCEL itself may be lost on the way to a server that is gone.
func (s InviteClientState) sendCancel() []TransactionAction {
	return []TransactionAction{
		SendCancel{},
		StartTimer{Timer: TimerAfterCancel, Duration: 64 * s.Timers.T1},
	}
}

// NonInviteClientState is everything a non-INVITE client transaction
// remembers. Its request lives with the stack, since the state machine
// only ever asks for it to be sent.
type NonInviteClientState struct {
	State    TransactionState
	Timers   Timers
	Reliable bool
	// IntervalE is what Timer E was last set to, so that the next
	// interval can double from it.
	IntervalE time.Duration
}

// StartNonInviteClient enters Trying: the request goes to the
// transport, Timer F starts at 64*T1 unless TransactionTimeout says
// otherwise, and over an unreliable transport Timer E starts at T1 (RFC
// 3261 17.1.2.2).
func StartNonInviteClient(t Timers, reliable bool) (NonInviteClientState, []TransactionAction) {
	s := NonInviteClientState{State: Trying, Timers: t, Reliable: reliable, IntervalE: t.T1}
	actions := []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerF, Duration: t.transactionTimeout()},
	}
	if !reliable {
		actions = append(actions, StartTimer{Timer: TimerE, Duration: t.T1})
	}
	return s, actions
}

// NonInviteClientTransition is Figure 6 of RFC 3261. An event with no
// arrow out of the current state is ignored.
func NonInviteClientTransition(s NonInviteClientState, e TransactionEvent) (NonInviteClientState, []TransactionAction) {
	switch s.State {
	case Trying, Proceeding:
		switch e := e.(type) {
		case ResponseReceived:
			if e.Response.StatusCode < 200 {
				s.State = Proceeding
				return s, []TransactionAction{PassToTU{Response: e.Response}}
			}
			s.State = Completed
			return s, []TransactionAction{
				PassToTU{Response: e.Response},
				StartTimer{Timer: TimerK, Duration: waitT4(s.Timers, s.Reliable)},
			}
		case TimerFired:
			switch e.Timer {
			case TimerE:
				// In Trying the interval doubles up to T2; in
				// Proceeding it is T2 (RFC 3261 17.1.2.2).
				if s.State == Trying {
					s.IntervalE = min(2*s.IntervalE, s.Timers.T2)
				} else {
					s.IntervalE = s.Timers.T2
				}
				return s, []TransactionAction{
					SendRequest{},
					StartTimer{Timer: TimerE, Duration: s.IntervalE},
				}
			case TimerF:
				s.State = Terminated
				return s, []TransactionAction{InformTU{Err: ErrTimeout}}
			}
		case TransportError:
			s.State = Terminated
			return s, []TransactionAction{InformTU{Err: e.Err}}
		}
	case Completed:
		// Retransmissions of the final response are absorbed here.
		if e, ok := e.(TimerFired); ok && e.Timer == TimerK {
			s.State = Terminated
		}
	}
	return s, nil
}

// NonInviteServerState is everything a non-INVITE server transaction
// remembers.
type NonInviteServerState struct {
	State    TransactionState
	Timers   Timers
	Reliable bool
	// LastResponse is the most recent response sent, which a
	// retransmitted request gets again.
	LastResponse *Message
}

// StartNonInviteServer enters Trying. The stack passes the request to
// the TU (RFC 3261 17.2.2).
func StartNonInviteServer(t Timers, reliable bool) NonInviteServerState {
	return NonInviteServerState{State: Trying, Timers: t, Reliable: reliable}
}

// NonInviteServerTransition is Figure 8 of RFC 3261. An event with no
// arrow out of the current state is ignored.
func NonInviteServerTransition(s NonInviteServerState, e TransactionEvent) (NonInviteServerState, []TransactionAction) {
	switch e := e.(type) {
	case RequestRetransmitted:
		// In Trying there is nothing to resend yet, so the
		// retransmission is discarded.
		if s.State == Proceeding || s.State == Completed {
			return s, []TransactionAction{SendResponse{Response: s.LastResponse}}
		}
	case ResponseFromTU:
		if s.State != Trying && s.State != Proceeding {
			// Completed discards any further response from the TU.
			return s, nil
		}
		s.LastResponse = e.Response
		if e.Response.StatusCode < 200 {
			s.State = Proceeding
			return s, []TransactionAction{SendResponse{Response: e.Response}}
		}
		s.State = Completed
		return s, []TransactionAction{
			SendResponse{Response: e.Response},
			StartTimer{Timer: TimerJ, Duration: timerJ(s.Timers, s.Reliable)},
		}
	case TimerFired:
		if s.State == Completed && e.Timer == TimerJ {
			s.State = Terminated
		}
	case TransportError:
		if s.State == Proceeding || s.State == Completed {
			s.State = Terminated
			return s, []TransactionAction{InformTU{Err: e.Err}}
		}
	}
	return s, nil
}

// InviteServerState is everything an INVITE server transaction
// remembers.
type InviteServerState struct {
	State    TransactionState
	Timers   Timers
	Reliable bool
	// Trying is the 100 (Trying) to send if the TU stays silent for
	// 200 ms. The stack builds it from the INVITE (RFC 3261 8.2.6.1).
	Trying *Message
	// LastResponse is the most recent response sent, which a
	// retransmitted INVITE gets again.
	LastResponse *Message
	// IntervalG is what Timer G was last set to, so that the next
	// interval can double from it.
	IntervalG time.Duration
}

// StartInviteServer enters Proceeding. The stack passes the INVITE to
// the TU, and the transaction waits 200 ms for an answer before it
// sends trying (RFC 3261 17.2.1).
func StartInviteServer(t Timers, reliable bool, trying *Message) (InviteServerState, []TransactionAction) {
	s := InviteServerState{State: Proceeding, Timers: t, Reliable: reliable, Trying: trying}
	return s, []TransactionAction{StartTimer{Timer: TimerTrying, Duration: tryingDelay}}
}

// InviteServerTransition is Figure 7 as RFC 6026 8.6 redraws it. An
// event with no arrow out of the current state is ignored.
//
// A transport error never ends the transaction (RFC 6026 7.1): it stays
// to absorb retransmitted INVITEs, and its timers end it.
func InviteServerTransition(s InviteServerState, e TransactionEvent) (InviteServerState, []TransactionAction) {
	if e, ok := e.(TransportError); ok {
		if s.State == Terminated {
			return s, nil
		}
		return s, []TransactionAction{InformTU{Err: e.Err}}
	}
	switch s.State {
	case Proceeding:
		switch e := e.(type) {
		case RequestRetransmitted:
			// Before any response there is nothing to resend.
			if s.LastResponse != nil {
				return s, []TransactionAction{SendResponse{Response: s.LastResponse}}
			}
		case TimerFired:
			if e.Timer == TimerTrying && s.LastResponse == nil {
				s.LastResponse = s.Trying
				return s, []TransactionAction{SendResponse{Response: s.Trying}}
			}
		case ResponseFromTU:
			s.LastResponse = e.Response
			switch code := e.Response.StatusCode; {
			case code < 200:
				return s, []TransactionAction{SendResponse{Response: e.Response}}
			case code < 300:
				// The TU retransmits its 2xx until the ACK comes (RFC 6026
				// 8.1); the transaction only stays to absorb retransmitted
				// INVITEs.
				s.State = Accepted
				return s, []TransactionAction{
					SendResponse{Response: e.Response},
					StartTimer{Timer: TimerL, Duration: 64 * s.Timers.T1},
				}
			default:
				s.State = Completed
				actions := []TransactionAction{SendResponse{Response: e.Response}}
				if !s.Reliable {
					s.IntervalG = s.Timers.T1
					actions = append(actions, StartTimer{Timer: TimerG, Duration: s.IntervalG})
				}
				return s, append(actions, StartTimer{Timer: TimerH, Duration: 64 * s.Timers.T1})
			}
		}
	case Completed:
		switch e := e.(type) {
		case RequestRetransmitted:
			return s, []TransactionAction{SendResponse{Response: s.LastResponse}}
		case ACKReceived:
			s.State = Confirmed
			return s, []TransactionAction{StartTimer{Timer: TimerI, Duration: waitT4(s.Timers, s.Reliable)}}
		case TimerFired:
			switch e.Timer {
			case TimerG:
				s.IntervalG = min(2*s.IntervalG, s.Timers.T2)
				return s, []TransactionAction{
					SendResponse{Response: s.LastResponse},
					StartTimer{Timer: TimerG, Duration: s.IntervalG},
				}
			case TimerH:
				s.State = Terminated
				return s, []TransactionAction{InformTU{Err: ErrNoACK}}
			}
		}
	case Confirmed:
		// Further ACKs, sent for retransmissions of the final response
		// that crossed the first ACK, are absorbed here.
		if e, ok := e.(TimerFired); ok && e.Timer == TimerI {
			s.State = Terminated
		}
	case Accepted:
		switch e := e.(type) {
		case ResponseFromTU:
			if code := e.Response.StatusCode; code >= 200 && code < 300 {
				s.LastResponse = e.Response
				return s, []TransactionAction{SendResponse{Response: e.Response}}
			}
		case ACKReceived:
			return s, []TransactionAction{PassACKToTU{ACK: e.ACK}}
		case TimerFired:
			if e.Timer == TimerL {
				s.State = Terminated
			}
		}
	}
	return s, nil
}
