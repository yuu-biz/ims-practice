package sip

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// readFinal reads from the raw peer until the final response to the
// request with CSeq cseq comes, and returns it. Provisional responses
// and the responses to other requests are skipped.
func readFinal(t *testing.T, p *rawCallee, cseq string) *Message {
	t.Helper()
	for {
		if m := readDatagram(t, p.peer); !m.IsRequest() && m.StatusCode >= 200 && m.Get("CSeq") == cseq {
			return m
		}
	}
}

// RFC 3262 3: a PRACK that matches no reliable provisional response
// waiting for one gets 481. The UAC of a call takes PRACKs too, as its
// Allow says: the callee may send it a re-INVITE whose provisional
// responses go reliably.
func TestPRACKToTheCallerMatchingNoResponseGets481(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	_, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { r.Reject(488) }})
	prack := p.request(PRACK, Body{}, HeaderField{Name: "RAck", Value: "1 1 INVITE"})
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 481 {
		t.Errorf("PRACK got %s, want 481", startLine(resp))
	}
}

// rawCall has a raw caller call a UA whose user hands each request
// within the dialog to handle and accepts the call, and returns the
// call and the raw peer with the dialog as the caller has it. The
// rawCallee type sends and answers within a dialog from either side.
func rawCall(t *testing.T, timers Timers, handle func(*InDialogRequest)) (*IncomingCall, *rawCallee) {
	t.Helper()
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, timers, callee, func(c *IncomingCall) {
		c.Handle(handle)
		calls <- c
		c.Accept(answer)
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	offer.addTo(invite)
	send(t, peer, invite, calleeUA.Stack())
	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	d, err := NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	p := &rawCallee{t: t, peer: peer, ua: calleeUA, target: "sip:001010000000001@" + addrOf(peer).String(), invite: invite, dialog: d}
	return <-calls, p
}

// uasSide is a call of the UA in which a raw peer sends the re-INVITE:
// the UA answered the call, or it made the call.
type uasSide struct {
	name  string
	start func(t *testing.T, timers Timers, handle func(*InDialogRequest)) *rawCallee
}

var uasSides = []uasSide{
	{"IncomingCall", func(t *testing.T, timers Timers, handle func(*InDialogRequest)) *rawCallee {
		_, p := rawCall(t, timers, handle)
		return p
	}},
	{"OutgoingCall", func(t *testing.T, timers Timers, handle func(*InDialogRequest)) *rawCallee {
		_, p := rawAnswer(t, newUA(t, timers, "001010000000001", noCalls), Invitation{OnRequest: handle})
		return p
	}},
}

var supports100rel = HeaderField{Name: "Supported", Value: Option100rel}

// reInviteFrom sends a re-INVITE with body from the raw peer, which
// gives its own Contact as a target refresh request does.
func reInviteFrom(p *rawCallee, body Body, headers ...HeaderField) *Message {
	p.t.Helper()
	return p.request(INVITE, body, append([]HeaderField{{Name: "Contact", Value: "<" + p.target + ">"}}, headers...)...)
}

// prackFrom sends the PRACK for prov from the raw peer.
func prackFrom(p *rawCallee, prov *Message, body Body) *Message {
	p.t.Helper()
	return p.request(PRACK, body, HeaderField{Name: "RAck", Value: rackFor(prov).String()})
}

// ackFrom sends the ACK for the 2xx to reinvite from the raw peer.
func ackFrom(p *rawCallee, reinvite *Message) {
	p.t.Helper()
	cseq, _ := ParseCSeq(reinvite.Get("CSeq"))
	ack := p.dialog.newACK(cseq.Number)
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(p.peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	ack.Prepend("Via", via.String())
	send(p.t, p.peer, ack, p.ua.Stack())
}

// readProvisional reads the next provisional response other than 100
// from the raw peer.
func readProvisional(t *testing.T, p *rawCallee) *Message {
	t.Helper()
	m := readSkippingTrying(t, p.peer)
	if m.IsRequest() || m.StatusCode >= 200 {
		t.Fatalf("got %s, want a provisional response", startLine(m))
	}
	return m
}

// returned waits for an error that the user of the call sent on errs.
func returned(t *testing.T, errs <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-errs:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5s", what)
		return nil
	}
}

// RFC 3262 3 and 5 for a re-INVITE: it lists 100rel, so the 183 with
// the answer goes reliably and comes again until its PRACK. The PRACK
// gets 200, and only then does the 2xx go, which needs no answer of its
// own. That holds whichever side of the call the UA is.
func TestReliable183ToAReINVITE(t *testing.T) {
	for _, side := range uasSides {
		t.Run(side.name, func(t *testing.T) {
			progressed := make(chan error, 1)
			accept := make(chan struct{})
			acks := make(chan reinviteResult, 1)
			p := side.start(t, slowTimers, func(r *InDialogRequest) {
				go func() { progressed <- r.Progress(183, holdAnswer) }()
				<-accept
				ack, err := r.Accept(Body{})
				acks <- reinviteResult{ack, err}
			})
			reinvite := reInviteFrom(p, holdOffer, supports100rel)

			prov := readProvisional(t, p)
			rseq, err := ParseRSeq(prov.Get("RSeq"))
			if prov.StatusCode != 183 || !prov.Requires(Option100rel) || err != nil || rseq < 1 || rseq >= 1<<31 {
				t.Fatalf("got %s with Require %q and RSeq %q, want a reliable 183", startLine(prov), prov.Get("Require"), prov.Get("RSeq"))
			}
			// RFC 6141 4.5: the reliable response to a target refresh
			// request carries the local target.
			if got, want := prov.Get("Contact"), "<"+p.ua.cfg.Contact+">"; got != want || string(prov.Body) != string(holdAnswer.Content) {
				t.Errorf("183 has Contact %q and body %q, want %s and the answer", got, prov.Body, want)
			}
			close(accept)
			// RFC 3262 3: no 2xx while the 183 with the answer waits for
			// its PRACK; the 183 itself comes again.
			again := readSkippingTrying(t, p.peer)
			if again.StatusCode != 183 || again.Get("RSeq") != prov.Get("RSeq") {
				t.Fatalf("got %s with RSeq %q before the PRACK, want the 183 again", startLine(again), again.Get("RSeq"))
			}

			prack := prackFrom(p, prov, Body{})
			for {
				m := readDatagram(t, p.peer)
				if m.Get("CSeq") == reinvite.Get("CSeq") && m.StatusCode >= 200 {
					t.Fatalf("the re-INVITE got %s before the PRACK had its 200", startLine(m))
				}
				if m.Get("CSeq") == prack.Get("CSeq") {
					if m.StatusCode != 200 {
						t.Fatalf("PRACK got %s, want 200", startLine(m))
					}
					break
				}
			}
			if err := returned(t, progressed, "Progress"); err != nil {
				t.Errorf("Progress: %v", err)
			}
			ok := readFinal(t, p, reinvite.Get("CSeq"))
			if ok.StatusCode != 200 || len(ok.Body) > 0 {
				t.Errorf("re-INVITE got %s with body %q, want 200 without one", startLine(ok), ok.Body)
			}
			ackFrom(p, reinvite)
			select {
			case a := <-acks:
				if a.err != nil || a.ack == nil {
					t.Errorf("Accept returned %v, %v; want the ACK", a.ack, a.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Accept did not return after the ACK")
			}
		})
	}
}

// RFC 3262 3: a reliable provisional response that has no PRACK after
// 64*T1 gets the re-INVITE rejected with a 5xx. The call goes on.
func TestReINVITEWithoutAPRACKGets500(t *testing.T) {
	results := make(chan error, 2)
	in, p := rawCall(t, fastTimers, func(r *InDialogRequest) {
		results <- r.Progress(183, holdAnswer)
		_, err := r.Accept(Body{})
		results <- err
	})
	start := time.Now()
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 500 {
		t.Fatalf("re-INVITE got %s, want 500", startLine(final))
	}
	if elapsed := time.Since(start); elapsed < 64*fastTimers.T1 {
		t.Errorf("500 after %v, before 64*T1", elapsed)
	}
	if err := returned(t, results, "Progress"); !errors.Is(err, ErrNoPRACK) {
		t.Errorf("Progress: %v, want ErrNoPRACK", err)
	}
	if err := returned(t, results, "Accept"); !errors.Is(err, ErrRequestAnswered) {
		t.Errorf("Accept after the 500: %v, want ErrRequestAnswered", err)
	}
	ackNonOK(t, p, reinvite)
	select {
	case <-in.Done():
		t.Fatalf("the call ended with %v", in.Err())
	default:
	}
}

// RFC 3262 3: the provisional responses to a re-INVITE without 100rel
// go unreliably, and their session description is no answer, so the 2xx
// carries it. With 100rel in Require they go reliably.
func TestProvisionalResponsesToAReINVITEWithout100rel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  []HeaderField
		reliable bool
	}{
		{"neither", nil, false},
		{"Require", []HeaderField{{Name: "Require", Value: Option100rel}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := make(chan error, 2)
			_, p := rawCall(t, testTimers, func(r *InDialogRequest) {
				results <- r.Progress(183, holdAnswer)
				_, err := r.Accept(holdAnswer)
				results <- err
			})
			reinvite := reInviteFrom(p, holdOffer, tc.headers...)
			prov := readProvisional(t, p)
			if got := prov.Requires(Option100rel) || prov.Get("RSeq") != ""; got != tc.reliable {
				t.Fatalf("183 has Require %q and RSeq %q, want it reliable: %t", prov.Get("Require"), prov.Get("RSeq"), tc.reliable)
			}
			if tc.reliable {
				prackFrom(p, prov, Body{})
			}
			if err := returned(t, results, "Progress"); err != nil {
				t.Errorf("Progress: %v", err)
			}
			if ok := readFinal(t, p, reinvite.Get("CSeq")); ok.StatusCode != 200 {
				t.Errorf("re-INVITE got %s, want 200", startLine(ok))
			}
			ackFrom(p, reinvite)
			if err := returned(t, results, "Accept"); err != nil {
				t.Errorf("Accept: %v", err)
			}
		})
	}
}

// RFC 3262 3: a PRACK matches a reliable provisional response by its
// RSeq and by the CSeq of its INVITE, so a PRACK with another RSeq, or
// one for the INVITE of the call, gets 481.
func TestPRACKForAReINVITEMatchesItsResponse(t *testing.T) {
	progressed := make(chan error, 1)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		progressed <- r.Progress(180, Body{})
		r.Accept(holdAnswer)
	})
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	prov := readProvisional(t, p)
	rack := rackFor(prov)
	for _, wrong := range []RAck{
		{RSeq: rack.RSeq + 1, CSeq: rack.CSeq},
		{RSeq: rack.RSeq, CSeq: CSeq{Number: 1, Method: INVITE}},
	} {
		prack := p.request(PRACK, Body{}, HeaderField{Name: "RAck", Value: wrong.String()})
		if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 481 {
			t.Errorf("PRACK with RAck %s got %s, want 481", wrong, startLine(resp))
		}
	}
	prack := prackFrom(p, prov, Body{})
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 {
		t.Fatalf("PRACK got %s, want 200", startLine(resp))
	}
	if err := returned(t, progressed, "Progress"); err != nil {
		t.Errorf("Progress: %v", err)
	}
	readFinal(t, p, reinvite.Get("CSeq"))
	ackFrom(p, reinvite)
}

// RFC 3261 9.2 and RFC 3262 3: before any reliable provisional response
// has carried a session description, a CANCEL gets the re-INVITE its
// 487 at once, though a reliable 180 still waits for its PRACK. That
// PRACK, when it comes, still matches the response. The user learns of
// the CANCEL all the same, as a B2BUA has to cancel its other leg.
func TestCancelledReINVITEDoesNotWaitForThePRACK(t *testing.T) {
	progressed := make(chan error, 2)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		progressed <- r.Progress(180, Body{})
		select {
		case <-r.Cancelled():
			progressed <- nil
		default:
			progressed <- errors.New("Cancelled is not closed")
		}
	})
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	prov := readProvisional(t, p)
	start := time.Now()
	send(t, p.peer, newMatchingRequest(reinvite, CANCEL, reinvite.Get("To")), p.ua.Stack())
	if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 487 {
		t.Fatalf("re-INVITE got %s, want 487", startLine(final))
	}
	if elapsed := time.Since(start); elapsed > 16*slowTimers.T1 {
		t.Errorf("487 after %v, want it at once", elapsed)
	}
	ackNonOK(t, p, reinvite)
	if err := returned(t, progressed, "Progress"); !errors.Is(err, ErrRequestAnswered) {
		t.Errorf("Progress: %v, want ErrRequestAnswered", err)
	}
	if err := returned(t, progressed, "the check of Cancelled"); err != nil {
		t.Error(err)
	}
	prack := prackFrom(p, prov, Body{})
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 {
		t.Errorf("PRACK after the 487 got %s, want 200", startLine(resp))
	}
}

// RFC 6141 3.3 and 3.8: once a reliable 183 has carried the answer, a
// CANCEL leaves the re-INVITE to the user, who knows whether the change
// has been made. The user here does what a B2BUA does: on Cancelled it
// passes the CANCEL on to its other leg, and ends the re-INVITE with the
// final response that comes from there, a 2xx, which the UAC
// acknowledges, or a 487.
func TestCancelledReINVITEAfterAReliableAnswerIsLeftToTheUser(t *testing.T) {
	for _, farEnd := range []int{200, 487} {
		t.Run(strconv.Itoa(farEnd), func(t *testing.T) {
			relayed := make(chan struct{})
			final := make(chan int)
			results := make(chan reinviteResult, 1)
			_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
				if err := r.Progress(183, holdAnswer); err != nil {
					results <- reinviteResult{nil, err}
					return
				}
				select {
				case <-r.Cancelled():
				case <-time.After(5 * time.Second):
					results <- reinviteResult{nil, errors.New("Cancelled was not closed within 5s of the CANCEL")}
					return
				}
				close(relayed)
				if code := <-final; code == 487 {
					results <- reinviteResult{nil, r.Reject(487)}
					return
				}
				ack, err := r.Accept(Body{})
				results <- reinviteResult{ack, err}
			})
			reinvite := reInviteFrom(p, holdOffer, supports100rel)
			prack := prackFrom(p, readProvisional(t, p), Body{})
			readFinal(t, p, prack.Get("CSeq"))
			cancel := newMatchingRequest(reinvite, CANCEL, reinvite.Get("To"))
			send(t, p.peer, cancel, p.ua.Stack())
			if resp := readFinal(t, p, cancel.Get("CSeq")); resp.StatusCode != 200 {
				t.Fatalf("CANCEL got %s, want 200", startLine(resp))
			}
			select {
			case <-relayed:
			case r := <-results:
				t.Fatalf("the user: %v", r.err)
			}
			for until := time.Now().Add(3 * slowTimers.T1); time.Now().Before(until); {
				if m, ok := readWithin(p.peer, time.Until(until)); ok && m.Get("CSeq") == reinvite.Get("CSeq") && m.StatusCode >= 200 {
					t.Fatalf("the re-INVITE got %s before the user ended it", startLine(m))
				}
			}

			final <- farEnd
			if got := readFinal(t, p, reinvite.Get("CSeq")); got.StatusCode != farEnd {
				t.Fatalf("re-INVITE got %s, want %d", startLine(got), farEnd)
			}
			if farEnd == 200 {
				ackFrom(p, reinvite)
			} else {
				ackNonOK(t, p, reinvite)
			}
			select {
			case r := <-results:
				if r.err != nil || (farEnd == 200) != (r.ack != nil) {
					t.Errorf("the user's answer returned %v, %v", r.ack, r.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the user's answer did not return")
			}
		})
	}
}

// RFC 3262 5: the first reliable provisional response to a re-INVITE
// without an offer carries the offer.
func TestFirstReliableResponseToAReINVITEWithoutAnOfferOffers(t *testing.T) {
	results := make(chan error, 1)
	_, p := rawCall(t, testTimers, func(r *InDialogRequest) {
		results <- r.Progress(180, Body{})
		r.Reject(500)
	})
	reinvite := reInviteFrom(p, Body{}, supports100rel)
	if err := returned(t, results, "Progress"); !errors.Is(err, errNoOfferInReliable) {
		t.Errorf("Progress without the offer: %v, want errNoOfferInReliable", err)
	}
	if m := readSkippingTrying(t, p.peer); m.StatusCode != 500 || m.Get("CSeq") != reinvite.Get("CSeq") {
		t.Errorf("re-INVITE got %s, want only the 500 of the user", startLine(m))
	}
}

// The UAC of a re-INVITE and its UAS, both of this package, whichever
// side of the call sends the re-INVITE: the answer comes in a reliable
// 183, the UAC acknowledges it with a PRACK of its own, and the 2xx
// carries no session description (RFC 3262 5).
func TestReliable183ToAReINVITEBetweenTwoUAs(t *testing.T) {
	results := make(chan reinviteResult, 1)
	user := func(r *InDialogRequest) {
		if err := r.Progress(183, holdAnswer); err != nil {
			results <- reinviteResult{nil, err}
			return
		}
		ack, err := r.Accept(Body{})
		results <- reinviteResult{ack, err}
	}
	e := establishWith(t, user, user)
	for _, side := range []struct {
		name string
		leg  interface {
			ReInviteWith(ReInvitation) (*Message, error)
		}
	}{{"caller", e.out}, {"callee", e.in}} {
		got := make(chan *Message, 2)
		done := make(chan reInviteDone, 1)
		go func() {
			resp, err := side.leg.ReInviteWith(ReInvitation{Body: holdOffer, OnProvisional: func(m *Message) { got <- m }})
			done <- reInviteDone{resp, err}
		}()
		if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 || len(r.resp.Body) > 0 {
			t.Fatalf("re-INVITE of the %s got %v, %v; want 200 without a body", side.name, r.resp, r.err)
		}
		if m := handed(t, got, 1)[0]; m.StatusCode != 183 || !m.Requires(Option100rel) || string(m.Body) != string(holdAnswer.Content) {
			t.Errorf("the %s got %s with Require %q and body %q, want the reliable 183 with the answer",
				side.name, startLine(m), m.Get("Require"), m.Body)
		}
		select {
		case r := <-results:
			if r.err != nil || r.ack == nil {
				t.Fatalf("the peer of the %s: Accept returned %v, %v; want the ACK", side.name, r.ack, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the peer of the %s did not get its ACK", side.name)
		}
	}
}

// RFC 3262 5 between two UAs of this package: a re-INVITE without an
// offer gets it in a reliable 183, the user of the UAC answers in the
// PRACK, and the user of the UAS reads the answer there.
func TestReINVITEWithoutAnOfferAnsweredInThePRACKBetweenTwoUAs(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	results := make(chan reinviteResult, 1)
	e := establishWith(t, nil, func(r *InDialogRequest) {
		if r.Request.Method == PRACK {
			pracks <- r
			r.Accept(Body{})
			return
		}
		if err := r.Progress(183, offer2); err != nil {
			results <- reinviteResult{nil, err}
			return
		}
		ack, err := r.Accept(Body{})
		results <- reinviteResult{ack, err}
	})
	got := make(chan *Message, 1)
	done := reInviteWith(e.out, ReInvitation{OnProvisional: func(m *Message) { got <- m }})
	m := handed(t, got, 1)[0]
	if resp, err := e.out.PRACK(m, answer2); err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRACK got %v, %v; want 200", resp, err)
	}
	if r := <-pracks; r.Offer || string(r.Request.Body) != string(answer2.Content) {
		t.Errorf("the callee's user got Offer %t and body %q, want the PRACK with the answer", r.Offer, r.Request.Body)
	}
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 || len(r.resp.Body) > 0 {
		t.Fatalf("ReInviteWith returned %v, %v; want 200 without a body", r.resp, r.err)
	}
	if r := <-results; r.err != nil || r.ack == nil || len(r.ack.Body) > 0 {
		t.Errorf("Accept returned %v, %v; want the ACK without a body", r.ack, r.err)
	}
}

// RFC 6141 4.6: once a reliable provisional response to a re-INVITE has
// gone, its target refresh holds, so the remote target stays the
// Contact of the re-INVITE even when the re-INVITE then fails. After a
// provisional response that is not reliable, the failure leaves the
// target as it was (RFC 3261 12.2.2).
func TestReliableResponseToAReINVITEKeepsItsTargetRefresh(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  []HeaderField
		reliable bool
	}{
		{"reliable", []HeaderField{supports100rel}, true},
		{"unreliable", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := make(chan error, 2)
			in, p := rawCall(t, testTimers, func(r *InDialogRequest) {
				results <- r.Progress(180, Body{})
				results <- r.Reject(488)
			})
			moved := "sip:moved@" + addrOf(p.peer).String()
			reinvite := p.request(INVITE, holdOffer, append([]HeaderField{{Name: "Contact", Value: "<" + moved + ">"}}, tc.headers...)...)
			prov := readProvisional(t, p)
			if tc.reliable {
				prackFrom(p, prov, Body{})
			}
			if err := returned(t, results, "Progress"); err != nil {
				t.Fatalf("Progress: %v", err)
			}
			if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 488 {
				t.Fatalf("re-INVITE got %s, want 488", startLine(final))
			}
			ackNonOK(t, p, reinvite)
			want := p.target
			if tc.reliable {
				want = moved
			}
			if d, _ := in.Dialog(); d.RemoteTarget != want {
				t.Errorf("remote target is %s after the 488, want %s", d.RemoteTarget, want)
			}
		})
	}
}

// A B2BUA passes the reliable provisional responses of its other leg on
// to a re-INVITE, and the PRACKs back (RFC 3262 4). After RelayPRACK the
// user of the call gets each PRACK of the re-INVITE, with or without a
// body, and the 200 to it is the user's; otherwise the UA answers a
// PRACK without a body itself.
func TestRelayPRACKOfAReINVITE(t *testing.T) {
	for _, relay := range []bool{false, true} {
		name := "PRACK of the UA"
		if relay {
			name = "PRACK of the user"
		}
		t.Run(name, func(t *testing.T) {
			pracks := make(chan *InDialogRequest, 1)
			progressed := make(chan error, 1)
			_, p := rawCall(t, testTimers, func(r *InDialogRequest) {
				if r.Request.Method == PRACK {
					pracks <- r
					return
				}
				if relay {
					r.RelayPRACK()
				}
				progressed <- r.Progress(180, Body{})
				r.Accept(holdAnswer)
			})
			reInviteFrom(p, holdOffer, supports100rel)
			prack := prackFrom(p, readProvisional(t, p), Body{})
			if relay {
				var r *InDialogRequest
				select {
				case r = <-pracks:
				case <-time.After(2 * time.Second):
					t.Fatal("the user did not get the PRACK")
				}
				if r.Request.Get("CSeq") != prack.Get("CSeq") || r.Arrival != 2 {
					t.Fatalf("the user got CSeq %q with Arrival %d, want the PRACK after the re-INVITE", r.Request.Get("CSeq"), r.Arrival)
				}
				// Only a retransmission of the 180 that crossed the PRACK
				// may come before the user accepts it.
				for until := time.Now().Add(3 * testTimers.T1); time.Now().Before(until); {
					if m, ok := readWithin(p.peer, time.Until(until)); ok && m.Get("CSeq") == prack.Get("CSeq") {
						t.Fatalf("the PRACK got %s before the user accepted it", startLine(m))
					}
				}
				if _, err := r.Accept(Body{}); err != nil {
					t.Fatalf("Accept of the PRACK: %v", err)
				}
			}
			if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 {
				t.Fatalf("PRACK got %s, want 200", startLine(resp))
			}
			if err := returned(t, progressed, "Progress"); err != nil {
				t.Errorf("Progress: %v", err)
			}
			if !relay && len(pracks) > 0 {
				t.Error("the user got the PRACK without a body, which the UA answers")
			}
		})
	}
}
