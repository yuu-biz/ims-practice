package sip

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func response(code int) *Message { return &Message{StatusCode: code, Reason: ReasonPhrase(code)} }

func wantActions(t *testing.T, step string, got, want []TransactionAction) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: actions = %#v, want %#v", step, got, want)
	}
}

func wantState(t *testing.T, step string, got, want TransactionState) {
	t.Helper()
	if got != want {
		t.Errorf("%s: state = %v, want %v", step, got, want)
	}
}

func TestNonInviteClientRetransmitsWithBackoff(t *testing.T) {
	s, actions := StartNonInviteClient(DefaultTimers(), false)
	wantState(t, "start", s.State, Trying)
	wantActions(t, "start", actions, []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerF, Duration: 32 * time.Second},
		StartTimer{Timer: TimerE, Duration: 500 * time.Millisecond},
	})
	// RFC 3261 17.1.2.2: 500 ms, 1 s, 2 s, 4 s, 4 s, ...
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		s, actions = NonInviteClientTransition(s, TimerFired{Timer: TimerE})
		wantActions(t, "Timer E", actions, []TransactionAction{SendRequest{}, StartTimer{Timer: TimerE, Duration: want}})
	}
	wantState(t, "after Timer E", s.State, Trying)
}

func TestNonInviteClientReceivesResponses(t *testing.T) {
	s, _ := StartNonInviteClient(DefaultTimers(), false)
	trying, ok, retransmittedOK := response(100), response(200), response(200)

	s, actions := NonInviteClientTransition(s, ResponseReceived{Response: trying})
	wantState(t, "1xx", s.State, Proceeding)
	wantActions(t, "1xx", actions, []TransactionAction{PassToTU{Response: trying}})

	s, actions = NonInviteClientTransition(s, TimerFired{Timer: TimerE})
	wantActions(t, "Timer E in Proceeding", actions, []TransactionAction{SendRequest{}, StartTimer{Timer: TimerE, Duration: 4 * time.Second}})

	s, actions = NonInviteClientTransition(s, ResponseReceived{Response: ok})
	wantState(t, "final", s.State, Completed)
	wantActions(t, "final", actions, []TransactionAction{PassToTU{Response: ok}, StartTimer{Timer: TimerK, Duration: 5 * time.Second}})

	for _, e := range []TransactionEvent{ResponseReceived{Response: retransmittedOK}, TimerFired{Timer: TimerE}, TimerFired{Timer: TimerF}} {
		s, actions = NonInviteClientTransition(s, e)
		wantState(t, e.eventName()+" in Completed", s.State, Completed)
		wantActions(t, e.eventName()+" in Completed", actions, nil)
	}

	s, actions = NonInviteClientTransition(s, TimerFired{Timer: TimerK})
	wantState(t, "Timer K", s.State, Terminated)
	wantActions(t, "Timer K", actions, nil)
}

func TestNonInviteClientFinalResponseInTrying(t *testing.T) {
	s, _ := StartNonInviteClient(DefaultTimers(), false)
	unauthorized := response(401)
	s, actions := NonInviteClientTransition(s, ResponseReceived{Response: unauthorized})
	wantState(t, "401", s.State, Completed)
	wantActions(t, "401", actions, []TransactionAction{PassToTU{Response: unauthorized}, StartTimer{Timer: TimerK, Duration: 5 * time.Second}})
}

func TestNonInviteClientTimesOut(t *testing.T) {
	for _, first := range []TransactionEvent{nil, ResponseReceived{Response: response(100)}} {
		s, _ := StartNonInviteClient(DefaultTimers(), false)
		if first != nil {
			s, _ = NonInviteClientTransition(s, first)
		}
		s, actions := NonInviteClientTransition(s, TimerFired{Timer: TimerF})
		wantState(t, "Timer F", s.State, Terminated)
		wantActions(t, "Timer F", actions, []TransactionAction{InformTU{Err: ErrTimeout}})
	}
}

func TestNonInviteClientTransportError(t *testing.T) {
	s, _ := StartNonInviteClient(DefaultTimers(), false)
	failure := errors.New("network unreachable")
	s, actions := NonInviteClientTransition(s, TransportError{Err: failure})
	wantState(t, "transport error", s.State, Terminated)
	wantActions(t, "transport error", actions, []TransactionAction{InformTU{Err: failure}})
}

func TestInviteClientRetransmitsUntilTimerB(t *testing.T) {
	s, actions := StartInviteClient(DefaultTimers(), false)
	wantState(t, "start", s.State, Calling)
	wantActions(t, "start", actions, []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerB, Duration: 32 * time.Second},
		StartTimer{Timer: TimerA, Duration: 500 * time.Millisecond},
	})
	// RFC 3261 17.1.1.2: the interval doubles with no T2 cap, so seven
	// requests go out before Timer B.
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second} {
		s, actions = InviteClientTransition(s, TimerFired{Timer: TimerA})
		wantActions(t, "Timer A", actions, []TransactionAction{SendRequest{}, StartTimer{Timer: TimerA, Duration: want}})
	}
	s, actions = InviteClientTransition(s, TimerFired{Timer: TimerB})
	wantState(t, "Timer B", s.State, Terminated)
	wantActions(t, "Timer B", actions, []TransactionAction{InformTU{Err: ErrTimeout}})
}

func TestInviteClientProceedingStopsRetransmissions(t *testing.T) {
	s, _ := StartInviteClient(DefaultTimers(), false)
	trying, ringing := response(100), response(180)

	s, actions := InviteClientTransition(s, ResponseReceived{Response: trying})
	wantState(t, "100", s.State, Proceeding)
	wantActions(t, "100", actions, []TransactionAction{PassToTU{Response: trying}})

	for _, e := range []TransactionEvent{TimerFired{Timer: TimerA}, TimerFired{Timer: TimerB}} {
		s, actions = InviteClientTransition(s, e)
		wantState(t, e.eventName()+" in Proceeding", s.State, Proceeding)
		wantActions(t, e.eventName()+" in Proceeding", actions, nil)
	}

	s, actions = InviteClientTransition(s, ResponseReceived{Response: ringing})
	wantActions(t, "180", actions, []TransactionAction{PassToTU{Response: ringing}})
}

func TestInviteClientACKsAFailureResponse(t *testing.T) {
	for _, first := range []TransactionEvent{nil, ResponseReceived{Response: response(180)}} {
		s, _ := StartInviteClient(DefaultTimers(), false)
		if first != nil {
			s, _ = InviteClientTransition(s, first)
		}
		busy, retransmitted := response(486), response(486)
		s, actions := InviteClientTransition(s, ResponseReceived{Response: busy})
		wantState(t, "486", s.State, Completed)
		wantActions(t, "486", actions, []TransactionAction{
			PassToTU{Response: busy},
			SendACK{Response: busy},
			StartTimer{Timer: TimerD, Duration: 32 * time.Second},
		})

		s, actions = InviteClientTransition(s, ResponseReceived{Response: retransmitted})
		wantState(t, "retransmitted 486", s.State, Completed)
		wantActions(t, "retransmitted 486", actions, []TransactionAction{SendACK{Response: retransmitted}})

		s, actions = InviteClientTransition(s, TimerFired{Timer: TimerD})
		wantState(t, "Timer D", s.State, Terminated)
		wantActions(t, "Timer D", actions, nil)
	}
}

func TestInviteClientTimerDOutlastsTheServer(t *testing.T) {
	// With a T1 of 1 s, the server's Timer H is 64 s, longer than the 32 s
	// minimum.
	s, _ := StartInviteClient(Timers{T1: time.Second, T2: 4 * time.Second, T4: 5 * time.Second}, false)
	_, actions := InviteClientTransition(s, ResponseReceived{Response: response(404)})
	if got := actions[len(actions)-1]; got != (StartTimer{Timer: TimerD, Duration: 64 * time.Second}) {
		t.Errorf("last action = %#v, want Timer D at 64 s", got)
	}
}

func TestInviteClientAcceptedPassesEvery2xx(t *testing.T) {
	s, _ := StartInviteClient(DefaultTimers(), false)
	ok, retransmitted, forked := response(200), response(200), response(202)

	s, actions := InviteClientTransition(s, ResponseReceived{Response: ok})
	wantState(t, "200", s.State, Accepted)
	wantActions(t, "200", actions, []TransactionAction{
		PassToTU{Response: ok},
		StartTimer{Timer: TimerM, Duration: 32 * time.Second},
	})

	// RFC 6026 7.2: no ACK from the transaction, and every 2xx to the TU.
	for _, resp := range []*Message{retransmitted, forked} {
		s, actions = InviteClientTransition(s, ResponseReceived{Response: resp})
		wantState(t, "2xx in Accepted", s.State, Accepted)
		wantActions(t, "2xx in Accepted", actions, []TransactionAction{PassToTU{Response: resp}})
	}
	for _, e := range []TransactionEvent{ResponseReceived{Response: response(486)}, ResponseReceived{Response: response(180)}, TimerFired{Timer: TimerA}, TimerFired{Timer: TimerB}} {
		s, actions = InviteClientTransition(s, e)
		wantState(t, e.eventName()+" in Accepted", s.State, Accepted)
		wantActions(t, e.eventName()+" in Accepted", actions, nil)
	}

	s, actions = InviteClientTransition(s, TimerFired{Timer: TimerM})
	wantState(t, "Timer M", s.State, Terminated)
	wantActions(t, "Timer M", actions, nil)
}

func TestInviteClientCancelWaitsForAProvisionalResponse(t *testing.T) {
	s, _ := StartInviteClient(DefaultTimers(), false)
	s, actions := InviteClientTransition(s, CancelFromTU{})
	wantState(t, "CANCEL in Calling", s.State, Calling)
	wantActions(t, "CANCEL in Calling", actions, nil)

	// RFC 3261 9.1: the CANCEL goes out with the first provisional
	// response, and only once.
	trying := response(100)
	s, actions = InviteClientTransition(s, ResponseReceived{Response: trying})
	wantActions(t, "100 after CANCEL", actions, []TransactionAction{
		PassToTU{Response: trying},
		SendCancel{},
		StartTimer{Timer: TimerAfterCancel, Duration: 32 * time.Second},
	})
	ringing := response(180)
	s, actions = InviteClientTransition(s, ResponseReceived{Response: ringing})
	wantActions(t, "180 after CANCEL", actions, []TransactionAction{PassToTU{Response: ringing}})
	s, actions = InviteClientTransition(s, CancelFromTU{})
	wantActions(t, "second CANCEL", actions, nil)

	terminated := response(487)
	s, actions = InviteClientTransition(s, ResponseReceived{Response: terminated})
	wantState(t, "487", s.State, Completed)
	wantActions(t, "487", actions, []TransactionAction{
		PassToTU{Response: terminated},
		SendACK{Response: terminated},
		StartTimer{Timer: TimerD, Duration: 32 * time.Second},
	})
}

func TestInviteClientCancelInProceeding(t *testing.T) {
	s, _ := StartInviteClient(DefaultTimers(), false)
	s, _ = InviteClientTransition(s, ResponseReceived{Response: response(180)})
	s, actions := InviteClientTransition(s, CancelFromTU{})
	wantActions(t, "CANCEL in Proceeding", actions, []TransactionAction{
		SendCancel{},
		StartTimer{Timer: TimerAfterCancel, Duration: 32 * time.Second},
	})

	// RFC 3261 9.1: no final response within 64*T1 of the CANCEL.
	s, actions = InviteClientTransition(s, TimerFired{Timer: TimerAfterCancel})
	wantState(t, "64*T1 after CANCEL", s.State, Terminated)
	wantActions(t, "64*T1 after CANCEL", actions, []TransactionAction{InformTU{Err: ErrTimeout}})
}

func TestInviteClientIgnoresTheCancelTimerAfterTheFinalResponse(t *testing.T) {
	s, _ := StartInviteClient(DefaultTimers(), false)
	s, _ = InviteClientTransition(s, ResponseReceived{Response: response(180)})
	s, _ = InviteClientTransition(s, CancelFromTU{})
	s, _ = InviteClientTransition(s, ResponseReceived{Response: response(487)})
	s, actions := InviteClientTransition(s, TimerFired{Timer: TimerAfterCancel})
	wantState(t, "cancel timer in Completed", s.State, Completed)
	wantActions(t, "cancel timer in Completed", actions, nil)
}

func TestInviteClientCancelAfterAFinalResponse(t *testing.T) {
	for _, final := range []int{200, 486} {
		s, _ := StartInviteClient(DefaultTimers(), false)
		s, _ = InviteClientTransition(s, ResponseReceived{Response: response(final)})
		s, actions := InviteClientTransition(s, CancelFromTU{})
		wantActions(t, "CANCEL in "+s.State.String(), actions, nil)
	}
	// A CANCEL still waiting in Calling is dropped by a final response.
	s, _ := StartInviteClient(DefaultTimers(), false)
	s, _ = InviteClientTransition(s, CancelFromTU{})
	busy := response(486)
	_, actions := InviteClientTransition(s, ResponseReceived{Response: busy})
	wantActions(t, "486 after CANCEL in Calling", actions, []TransactionAction{
		PassToTU{Response: busy},
		SendACK{Response: busy},
		StartTimer{Timer: TimerD, Duration: 32 * time.Second},
	})
}

func TestInviteClientTransportError(t *testing.T) {
	failure := errors.New("network unreachable")
	s, _ := StartInviteClient(DefaultTimers(), false)
	s, actions := InviteClientTransition(s, TransportError{Err: failure})
	wantState(t, "transport error in Calling", s.State, Terminated)
	wantActions(t, "transport error in Calling", actions, []TransactionAction{InformTU{Err: failure}})

	s, _ = StartInviteClient(DefaultTimers(), false)
	s, _ = InviteClientTransition(s, ResponseReceived{Response: response(603)})
	s, actions = InviteClientTransition(s, TransportError{Err: failure})
	wantState(t, "transport error in Completed", s.State, Terminated)
	wantActions(t, "transport error in Completed", actions, []TransactionAction{InformTU{Err: failure}})
}

func TestInviteServerSendsTryingWhenTheTUIsSilent(t *testing.T) {
	trying, ringing := response(100), response(180)
	s, actions := StartInviteServer(DefaultTimers(), false, trying)
	wantState(t, "start", s.State, Proceeding)
	wantActions(t, "start", actions, []TransactionAction{StartTimer{Timer: TimerTrying, Duration: 200 * time.Millisecond}})

	s, actions = InviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission before any response", actions, nil)

	s, actions = InviteServerTransition(s, TimerFired{Timer: TimerTrying})
	wantState(t, "200 ms", s.State, Proceeding)
	wantActions(t, "200 ms", actions, []TransactionAction{SendResponse{Response: trying}})
	s, actions = InviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission after 100", actions, []TransactionAction{SendResponse{Response: trying}})

	s, actions = InviteServerTransition(s, ResponseFromTU{Response: ringing})
	wantState(t, "180 from TU", s.State, Proceeding)
	wantActions(t, "180 from TU", actions, []TransactionAction{SendResponse{Response: ringing}})
	_, actions = InviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission after 180", actions, []TransactionAction{SendResponse{Response: ringing}})
}

func TestInviteServerSendsNoTryingAfterTheTUAnswered(t *testing.T) {
	ringing := response(180)
	s, _ := StartInviteServer(DefaultTimers(), false, response(100))
	s, _ = InviteServerTransition(s, ResponseFromTU{Response: ringing})
	_, actions := InviteServerTransition(s, TimerFired{Timer: TimerTrying})
	wantActions(t, "200 ms after a 180", actions, nil)
}

func TestInviteServerRetransmitsAFailureUntilTheACK(t *testing.T) {
	s, _ := StartInviteServer(DefaultTimers(), false, response(100))
	busy := response(486)

	s, actions := InviteServerTransition(s, ResponseFromTU{Response: busy})
	wantState(t, "486 from TU", s.State, Completed)
	wantActions(t, "486 from TU", actions, []TransactionAction{
		SendResponse{Response: busy},
		StartTimer{Timer: TimerG, Duration: 500 * time.Millisecond},
		StartTimer{Timer: TimerH, Duration: 32 * time.Second},
	})
	// RFC 3261 17.2.1: 500 ms, 1 s, 2 s, 4 s, 4 s, like Timer E.
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		s, actions = InviteServerTransition(s, TimerFired{Timer: TimerG})
		wantActions(t, "Timer G", actions, []TransactionAction{SendResponse{Response: busy}, StartTimer{Timer: TimerG, Duration: want}})
	}
	s, actions = InviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission in Completed", actions, []TransactionAction{SendResponse{Response: busy}})
	s, actions = InviteServerTransition(s, ResponseFromTU{Response: response(200)})
	wantActions(t, "200 from TU in Completed", actions, nil)

	s, actions = InviteServerTransition(s, ACKReceived{ACK: &Message{Method: ACK}})
	wantState(t, "ACK", s.State, Confirmed)
	wantActions(t, "ACK", actions, []TransactionAction{StartTimer{Timer: TimerI, Duration: 5 * time.Second}})

	for _, e := range []TransactionEvent{ACKReceived{ACK: &Message{Method: ACK}}, RequestRetransmitted{}, TimerFired{Timer: TimerG}, TimerFired{Timer: TimerH}} {
		s, actions = InviteServerTransition(s, e)
		wantState(t, e.eventName()+" in Confirmed", s.State, Confirmed)
		wantActions(t, e.eventName()+" in Confirmed", actions, nil)
	}

	s, actions = InviteServerTransition(s, TimerFired{Timer: TimerI})
	wantState(t, "Timer I", s.State, Terminated)
	wantActions(t, "Timer I", actions, nil)
}

func TestInviteServerGivesUpWithoutTheACK(t *testing.T) {
	s, _ := StartInviteServer(DefaultTimers(), false, response(100))
	s, _ = InviteServerTransition(s, ResponseFromTU{Response: response(404)})
	s, actions := InviteServerTransition(s, TimerFired{Timer: TimerH})
	wantState(t, "Timer H", s.State, Terminated)
	wantActions(t, "Timer H", actions, []TransactionAction{InformTU{Err: ErrNoACK}})
}

func TestInviteServerAcceptedLeaves2xxRetransmissionToTheTU(t *testing.T) {
	s, _ := StartInviteServer(DefaultTimers(), false, response(100))
	ok, retransmitted := response(200), response(200)
	ack := &Message{Method: ACK}

	s, actions := InviteServerTransition(s, ResponseFromTU{Response: ok})
	wantState(t, "200 from TU", s.State, Accepted)
	wantActions(t, "200 from TU", actions, []TransactionAction{
		SendResponse{Response: ok},
		StartTimer{Timer: TimerL, Duration: 32 * time.Second},
	})

	// RFC 6026 7.1: retransmitted INVITEs are absorbed, the TU's 2xx
	// retransmissions go out, and the ACK goes to the TU.
	s, actions = InviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission in Accepted", actions, nil)
	s, actions = InviteServerTransition(s, ResponseFromTU{Response: retransmitted})
	wantActions(t, "2xx from TU in Accepted", actions, []TransactionAction{SendResponse{Response: retransmitted}})
	s, actions = InviteServerTransition(s, ResponseFromTU{Response: response(486)})
	wantActions(t, "486 from TU in Accepted", actions, nil)
	s, actions = InviteServerTransition(s, ACKReceived{ACK: ack})
	wantState(t, "ACK in Accepted", s.State, Accepted)
	wantActions(t, "ACK in Accepted", actions, []TransactionAction{PassACKToTU{ACK: ack}})

	s, actions = InviteServerTransition(s, TimerFired{Timer: TimerL})
	wantState(t, "Timer L", s.State, Terminated)
	wantActions(t, "Timer L", actions, nil)
}

func TestInviteServerSurvivesTransportErrors(t *testing.T) {
	failure := errors.New("network unreachable")
	for _, final := range []int{0, 200, 486} {
		s, _ := StartInviteServer(DefaultTimers(), false, response(100))
		if final != 0 {
			s, _ = InviteServerTransition(s, ResponseFromTU{Response: response(final)})
		}
		before := s.State
		s, actions := InviteServerTransition(s, TransportError{Err: failure})
		wantState(t, "transport error in "+before.String(), s.State, before)
		wantActions(t, "transport error in "+before.String(), actions, []TransactionAction{InformTU{Err: failure}})
	}
}

func TestNonInviteServerAnswersRetransmissions(t *testing.T) {
	s := StartNonInviteServer(DefaultTimers(), false)
	ok := response(200)

	s, actions := NonInviteServerTransition(s, RequestRetransmitted{})
	wantState(t, "retransmission in Trying", s.State, Trying)
	wantActions(t, "retransmission in Trying", actions, nil)

	s, actions = NonInviteServerTransition(s, ResponseFromTU{Response: ok})
	wantState(t, "final from TU", s.State, Completed)
	wantActions(t, "final from TU", actions, []TransactionAction{SendResponse{Response: ok}, StartTimer{Timer: TimerJ, Duration: 32 * time.Second}})

	s, actions = NonInviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission in Completed", actions, []TransactionAction{SendResponse{Response: ok}})

	s, actions = NonInviteServerTransition(s, ResponseFromTU{Response: response(500)})
	wantActions(t, "second final from TU", actions, nil)
	s, actions = NonInviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission after a discarded final", actions, []TransactionAction{SendResponse{Response: ok}})

	s, actions = NonInviteServerTransition(s, TimerFired{Timer: TimerJ})
	wantState(t, "Timer J", s.State, Terminated)
	wantActions(t, "Timer J", actions, nil)
}

func TestNonInviteServerProceeding(t *testing.T) {
	s := StartNonInviteServer(DefaultTimers(), false)
	trying, ok := response(100), response(200)

	s, actions := NonInviteServerTransition(s, ResponseFromTU{Response: trying})
	wantState(t, "1xx from TU", s.State, Proceeding)
	wantActions(t, "1xx from TU", actions, []TransactionAction{SendResponse{Response: trying}})

	s, actions = NonInviteServerTransition(s, RequestRetransmitted{})
	wantActions(t, "retransmission in Proceeding", actions, []TransactionAction{SendResponse{Response: trying}})

	s, actions = NonInviteServerTransition(s, ResponseFromTU{Response: ok})
	wantState(t, "final from TU", s.State, Completed)
	wantActions(t, "final from TU", actions, []TransactionAction{SendResponse{Response: ok}, StartTimer{Timer: TimerJ, Duration: 32 * time.Second}})

	failure := errors.New("network unreachable")
	s, actions = NonInviteServerTransition(s, TransportError{Err: failure})
	wantState(t, "transport error", s.State, Terminated)
	wantActions(t, "transport error", actions, []TransactionAction{InformTU{Err: failure}})
}

// RFC 3261 17.1.1.2, 17.1.2.2, 17.2.1 and 17.2.2: over a reliable
// transport no timer retransmits, and the timers that wait for
// retransmissions to die out are zero.
func TestReliableTransportNeitherRetransmitsNorWaits(t *testing.T) {
	timers := DefaultTimers()
	busy, ok := response(486), response(200)

	nic, actions := StartNonInviteClient(timers, true)
	wantActions(t, "non-INVITE client start", actions, []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerF, Duration: 32 * time.Second},
	})
	nic, actions = NonInviteClientTransition(nic, ResponseReceived{Response: ok})
	wantState(t, "non-INVITE client final", nic.State, Completed)
	wantActions(t, "non-INVITE client final", actions, []TransactionAction{PassToTU{Response: ok}, StartTimer{Timer: TimerK, Duration: 0}})

	ic, actions := StartInviteClient(timers, true)
	wantActions(t, "INVITE client start", actions, []TransactionAction{
		SendRequest{},
		StartTimer{Timer: TimerB, Duration: 32 * time.Second},
	})
	ic, actions = InviteClientTransition(ic, ResponseReceived{Response: busy})
	wantState(t, "INVITE client 486", ic.State, Completed)
	wantActions(t, "INVITE client 486", actions, []TransactionAction{
		PassToTU{Response: busy},
		SendACK{Response: busy},
		StartTimer{Timer: TimerD, Duration: 0},
	})

	is, _ := StartInviteServer(timers, true, response(100))
	is, actions = InviteServerTransition(is, ResponseFromTU{Response: busy})
	wantState(t, "INVITE server 486", is.State, Completed)
	wantActions(t, "INVITE server 486", actions, []TransactionAction{
		SendResponse{Response: busy},
		StartTimer{Timer: TimerH, Duration: 32 * time.Second},
	})
	is, actions = InviteServerTransition(is, ACKReceived{ACK: &Message{Method: ACK}})
	wantState(t, "INVITE server ACK", is.State, Confirmed)
	wantActions(t, "INVITE server ACK", actions, []TransactionAction{StartTimer{Timer: TimerI, Duration: 0}})

	nis := StartNonInviteServer(timers, true)
	nis, actions = NonInviteServerTransition(nis, ResponseFromTU{Response: ok})
	wantState(t, "non-INVITE server final", nis.State, Completed)
	wantActions(t, "non-INVITE server final", actions, []TransactionAction{SendResponse{Response: ok}, StartTimer{Timer: TimerJ, Duration: 0}})
}
