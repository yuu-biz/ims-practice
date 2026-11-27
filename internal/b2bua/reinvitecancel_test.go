package b2bua_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the CANCEL of a re-INVITE that the B2BUA relays: it goes
// on to the far leg, and the final response of that leg is what the
// side that cancelled gets (RFC 3261 9.1, 9.2, RFC 6141 3.3, 3.8).

// cancelReason is the Reason with which a side tells why it gives the
// change of the session up (RFC 3326 2): the resources for the new
// session could not be reserved (TS 24.229 6.1.4.2 item b).
var cancelReason = sip.NewReason(sip.ReasonSIP, 487, "resources not reserved")

// cancelledReInvite is a call through the B2BUA b2b in which the callee
// sent a re-INVITE with an offer and the caller answered it in a
// reliable 183, which the callee acknowledges when prack is set. It
// returns the re-INVITE at the caller, the one the callee sent, the
// requests that reached the caller, and where the role hears of a
// request that got no 2xx.
//
// The callee is a stack of the test (rawCallee) so that it can cancel
// the re-INVITE it sent; the UA of package sip cancels only a re-INVITE
// of its own (sip.CallDialog.CancelReInvite).
func cancelledReInvite(t *testing.T, prack bool) (*sip.InDialogRequest, *sip.ClientTransaction, *wire, <-chan *sip.Message) {
	t.Helper()
	rc := newRawCallee(t, timers)
	unanswered := make(chan *sip.Message, 2)
	h := &hooks{crossing: true}
	h.unanswered = func(req *sip.Message, _ b2bua.Direction) { unanswered <- req }
	b2b := newB2BUA(t, h, rc.uri())
	reinvites := make(chan *sip.InDialogRequest, 2)
	callerWire := newWire()
	caller := startUA(t, "caller", timers, callerWire, func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) { reinvites <- r }))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	d := within(t, "dialog at the callee", rc.dialog)

	ct := rc.send(&d, sip.INVITE, reOffer,
		sip.HeaderField{Name: "Contact", Value: "<" + rc.uri() + ">"},
		sip.HeaderField{Name: "Supported", Value: sip.Option100rel})
	r := within(t, "the re-INVITE of the callee at the caller", reinvites)
	// The caller shows its answer in a reliable 183, whose copy reaches
	// the callee, and holds the final response (RFC 3262 3).
	go r.Progress(183, reAnswer)
	p := provisionalTo(t, ct)
	if p.StatusCode != 183 || !p.Requires(sip.Option100rel) || string(p.Body) != string(reAnswer.Content) {
		t.Fatalf("the callee got %d with Require %q and %q, want a reliable 183 with the answer",
			p.StatusCode, p.Get("Require"), p.Body)
	}
	if prack {
		rack := sip.RAck{RSeq: rseqOf(t, p), CSeq: cseqOf(t, ct.Request)}
		if resp := finalTo(rc.send(&d, sip.PRACK, sip.Body{}, sip.HeaderField{Name: "RAck", Value: rack.String()})); resp == nil || resp.StatusCode != 200 {
			t.Fatalf("the callee's PRACK got %v, want 200", resp)
		}
	}
	return r, ct, callerWire, unanswered
}

// TestTheCancelOfARelayedReInviteReachesTheFarLeg: the callee cancels
// the re-INVITE with which it asked for a change of the session. The
// CANCEL goes on to the caller with its Reason, and the 487 of the
// caller is what the callee's re-INVITE gets, so both ends are left on
// the session of the last offer and answer (RFC 3261 14.1).
func TestTheCancelOfARelayedReInviteReachesTheFarLeg(t *testing.T) {
	r, ct, callerWire, unanswered := cancelledReInvite(t, false)
	if err := ct.Cancel(cancelReason.Header()); err != nil {
		t.Fatalf("the callee could not cancel its re-INVITE: %v", err)
	}
	select {
	case <-r.Cancelled():
	case <-time.After(5 * time.Second):
		t.Fatal("the re-INVITE at the caller was not cancelled")
	}
	if got := callerWire.reasons(sip.CANCEL); len(got) != 1 || got[0] != cancelReason.String() {
		t.Errorf("the caller got CANCELs with Reason %v, want one with %q", got, cancelReason.String())
	}
	if err := r.Reject(487); err != nil {
		t.Fatalf("the caller could not answer its cancelled re-INVITE: %v", err)
	}
	if resp := finalTo(ct); resp == nil || resp.StatusCode != 487 {
		t.Fatalf("the callee's re-INVITE got %v, want the 487 of the caller", resp)
	}
	// The role puts back what it changed for the re-INVITE, which got no
	// 2xx on the caller's leg.
	if req := within(t, "the re-INVITE at Hooks.Unanswered", unanswered); req.Method != sip.INVITE {
		t.Errorf("the role was told of an unanswered %s, want the re-INVITE", req.Method)
	}
}

// TestACancelledRelayedReInviteThatTheFarLegAnswersWith2xx: the caller
// has made the change of the session that the callee's re-INVITE asked
// for, its answer having gone in a reliable 183 that got its PRACK, so
// it answers the cancelled re-INVITE with a 2xx and not with an error
// (RFC 6141 3.3, 3.8). The Call passes that 2xx on and invents no 487 of
// its own, so the two ends keep the same session, and the role is not
// told to put anything back.
func TestACancelledRelayedReInviteThatTheFarLegAnswersWith2xx(t *testing.T) {
	r, ct, callerWire, unanswered := cancelledReInvite(t, true)
	if err := ct.Cancel(cancelReason.Header()); err != nil {
		t.Fatalf("the callee could not cancel its re-INVITE: %v", err)
	}
	if !until(2*time.Second, func() bool { return callerWire.has(sip.CANCEL) }) {
		t.Fatalf("the caller received %v, want the CANCEL of the re-INVITE", callerWire.requests())
	}
	select {
	case <-r.Cancelled():
	case <-time.After(5 * time.Second):
		t.Fatal("the re-INVITE at the caller was not cancelled")
	}
	if _, err := r.Accept(sip.Body{}); err != nil {
		t.Fatalf("the caller could not accept its cancelled re-INVITE: %v", err)
	}
	if resp := finalTo(ct); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the callee's re-INVITE got %v, want the 200 of the caller", resp)
	}
	if len(unanswered) > 0 {
		t.Error("the role was told to put back what it changed for a re-INVITE that got a 2xx")
	}
}
