package b2bua_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is about the offer with which the Call moves the caller's
// media to the early dialog that answered: when it gives way to the two
// ends, how far it goes when the caller's own offers cross it, and what
// happens when the caller refuses it.

// The caller puts the call on hold as soon as it has acknowledged the
// 2xx of a forked call, and the Call is about to offer it the session
// of the dialog that answered. The two ends settle the session between
// them: the hold reaches that dialog and its answer the caller, and the
// Call offers nothing of its own, which would point the caller's media
// back at what that dialog sent before the hold (RFC 3261 14.1, RFC
// 3264 8).
func TestHoldRightAfterTheAnswerOfAForkedCall(t *testing.T) {
	callerWire := newWire()
	f := newForkedCallee(t, false)
	var held atomic.Bool
	var prepared atomic.Int32
	h := &hooks{}
	h.request = func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal {
		if req.Method == sip.INVITE && d == b2bua.ToCallee {
			held.Store(true)
		}
		return nil
	}
	h.response = func(resp *sip.Message, d b2bua.Direction) {
		// The role takes its time over the session of the dialog that
		// answered, the second time it prepares that dialog's 183 for the
		// caller: the first time was the relay of the 183 itself, this one
		// is the offer that moves the caller's media (OfferCaller). The
		// caller's hold crosses it meanwhile.
		if d == b2bua.ToCaller && resp.StatusCode == 183 && tagOf(t, resp) == "branch-2" && prepared.Add(1) == 2 {
			until(overtake, held.Load)
		}
	}
	b2b := newB2BUA(t, h, f.uri())
	offers := make(chan *sip.Message, 2)
	caller := startUA(t, "caller", timers, callerWire, func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, answerOffers(offers))
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}

	resp, err := out.ReInvite(hold)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the hold got %v: %v", resp, err)
	}
	if !strings.Contains(string(resp.Body), "a=recvonly") {
		t.Errorf("the hold got the answer\n%s\nwant the recvonly answer of the dialog that answered", resp.Body)
	}
	if got := within(t, "the hold at the callee", f.inDialog); !strings.Contains(string(got.Body), "a=sendonly") {
		t.Errorf("the dialog that answered got the hold with\n%s\nwant the caller's sendonly offer", got.Body)
	}
	// An offer of the Call's own would come once the role has prepared it
	// (overtake), or after the delay of an offer that crossed one of the
	// caller (sip.GlareDelay, at most 2 s).
	if until(3*time.Second, func() bool { return callerWire.has(sip.UPDATE) || len(offers) > 0 }) {
		t.Errorf("the caller got %v after its hold, want no offer of the B2BUA's own", callerWire.requests())
	}
	out.Bye()
}

// The session descriptions that the Call sends the caller go on in one
// session, whose version goes up with each of them (RFC 3264 8). An
// offer of the first dialog that the Call relayed to the caller is one
// of them, so the offer of the session of the dialog that answered
// comes after it.
func TestTheOfferAfterARelayedOfferOfTheFirstDialog(t *testing.T) {
	f := newForkedCallee(t, false)
	b2b := newB2BUA(t, &hooks{}, f.uri())
	offers := make(chan *sip.Message, 2)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, answerOffers(offers))
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	// The first dialog sends an offer of its own in its early dialog,
	// such as the one that tells the caller of its preconditions (RFC
	// 3311 5.1), and the Call relays it to the caller.
	d := within(t, "the early dialog of the first dialog", f.dialogs)
	if resp := f.send(d, sip.UPDATE, branchVersion(1, 2, "")); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the UPDATE of the first dialog got %v", resp)
	}
	relayed := within(t, "the UPDATE at the caller", offers)
	if relayed.Method != sip.UPDATE {
		t.Fatalf("the caller got a %s, want the UPDATE of the first dialog", relayed.Method)
	}
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}

	req := within(t, "the offer at the caller", offers)
	had, err := sdp.Parse(relayed.Body)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := sdp.Parse(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := had.Origin
	want.Version++
	if offer.Origin != want {
		t.Errorf("the %s has the o= line %+v, want %+v, one above that of the last session description that went to the caller",
			req.Method, offer.Origin, want)
	}
	out.Bye()
}

// The caller refuses the session of the dialog that answered. After an
// INVITE with an offer the call goes on as it was, with the caller's
// media where the first dialog put it. After an INVITE without one, the
// 2xx of that dialog carried its offer, which only the caller could
// answer, so the Call ends that dialog with a BYE, as a UAC that cannot
// answer the offer of a 2xx does (RFC 3261 13.2.2.4).
func TestTheCallerRefusesTheSessionOfTheDialogThatAnswered(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("the 2xx offers %t", offered), func(t *testing.T) {
			f := newForkedCallee(t, offered)
			b2b := newB2BUA(t, &hooks{}, f.uri())
			offers := make(chan *sip.Message, 2)
			caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
			inv := invitation(t, b2b, func(r *sip.InDialogRequest) {
				offers <- r.Request
				r.Reject(488)
			})
			inv.Body = callerSession(1)
			if offered {
				inv.Body = sip.Body{}
			}
			out, err := caller.Invite(inv)
			if err != nil {
				t.Fatal(err)
			}
			first := within(t, "183 of the first dialog", out.Responses())
			if offered {
				if resp, err := out.PRACK(first, callerSession(1)); err != nil || resp.StatusCode != 200 {
					t.Fatalf("the PRACK with the answer got %v: %v", resp, err)
				}
			}
			f.next <- struct{}{}
			within(t, "183 of the second dialog", out.Responses())
			f.next <- struct{}{}
			if _, final := finalOf(t, out); final.StatusCode != 200 {
				t.Fatalf("the call got %d", final.StatusCode)
			}
			within(t, "the offer at the caller", offers)

			if !offered {
				select {
				case bye := <-f.byes:
					t.Errorf("the dialog that answered got a %s, want the call to go on", bye.Method)
				case <-time.After(10 * timers.T1):
				}
				out.Bye()
				return
			}
			within(t, "the BYE at the dialog that answered", f.byes)
		})
	}
}

// Every offer of the Call's own may cross one of the caller, which
// refuses it with 491 (Request Pending): the Call sends three in all and
// then leaves the session as it is (RFC 3261 14.1).
func TestTheOffersOfTheDialogThatAnsweredGiveUp(t *testing.T) {
	f := newForkedCallee(t, false)
	h := &hooks{}
	b2b := newB2BUA(t, h, f.uri())
	offers := make(chan *sip.Message, 8)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, func(r *sip.InDialogRequest) {
		offers <- r.Request
		r.Reject(491)
	})
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	// The two offers after the first wait for a delay of at most 2 s
	// each (sip.GlareDelay), and a fourth would come within another.
	if !until(10*time.Second, func() bool { return len(offers) >= 3 }) {
		t.Fatalf("the caller refused %d offers with 491, want the 3 that the Call sends in all", len(offers))
	}
	if until(3*time.Second, func() bool { return len(offers) > 3 }) {
		t.Errorf("the caller got %d offers, want no more than the 3 that the Call sends in all", len(offers))
	}
	out.Bye()
}

// The caller hangs up while an offer of the Call's own waits to go again
// after the caller refused it with 491: no further offer goes, and the
// dialog that answered gets the BYE (RFC 3326 2).
func TestTheOffersOfTheDialogThatAnsweredStopWithTheCall(t *testing.T) {
	f := newForkedCallee(t, false)
	b2b := newB2BUA(t, &hooks{}, f.uri())
	offers := make(chan *sip.Message, 4)
	hungUp := make(chan struct{})
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, func(r *sip.InDialogRequest) {
		offers <- r.Request
		// The caller hangs up before it refuses the offer, so that the
		// Call comes to its next try with the dialog already gone.
		<-hungUp
		r.Reject(491)
	})
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	if !until(5*time.Second, func() bool { return len(offers) == 1 }) {
		t.Fatalf("the caller got %d offers, want the first one", len(offers))
	}
	if err := out.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	close(hungUp)
	within(t, "the BYE at the dialog that answered", f.byes)
	if until(3*time.Second, func() bool { return len(offers) > 1 }) {
		t.Errorf("the caller got %d offers, want only the one it refused before it hung up", len(offers))
	}
}

// The caller offers a session of its own as soon as it has acknowledged
// the 2xx of a forked call, and the dialog that answered refuses it with
// 488 (Not Acceptable Here): the session stays as it was, so the Call
// still offers the caller the session of that dialog (RFC 3261 13.2.1,
// RFC 3264 8).
func TestTheOfferAfterARefusedOfferOfTheCaller(t *testing.T) {
	f := newForkedCallee(t, false)
	f.refuses = 488
	var tried atomic.Bool
	var prepared atomic.Int32
	h := &hooks{}
	h.request = func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal {
		if req.Method == sip.INVITE && d == b2bua.ToCallee {
			tried.Store(true)
		}
		return nil
	}
	h.response = func(resp *sip.Message, d b2bua.Direction) {
		// As in TestHoldRightAfterTheAnswerOfAForkedCall, the role takes
		// its time over the session of the dialog that answered, so that
		// the caller's own offer comes first.
		if d == b2bua.ToCaller && resp.StatusCode == 183 && tagOf(t, resp) == "branch-2" && prepared.Add(1) == 2 {
			until(overtake, tried.Load)
		}
	}
	b2b := newB2BUA(t, h, f.uri())
	offers := make(chan *sip.Message, 2)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, answerOffers(offers))
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}

	if resp, err := out.ReInvite(hold); err != nil || resp.StatusCode != 488 {
		t.Fatalf("the re-INVITE of the caller got %v: %v, want the 488 of the dialog that answered", resp, err)
	}
	within(t, "the re-INVITE at the dialog that answered", f.inDialog)
	req := within(t, "the offer at the caller", offers)
	if got := mediaOf(t, req.Body); got != 41002 {
		t.Errorf("the %s points the caller's media at port %d, want 41002 of the dialog that answered", req.Method, got)
	}
	out.Bye()
}

// A session description that the Call relayed to the caller and that
// the caller refused is none of the caller's: the offer of the session
// of the dialog that answered goes on in the session the caller has,
// with the version one above the last description it took (RFC 3264 8).
func TestTheOfferAfterARefusedOfferOfTheFirstDialog(t *testing.T) {
	f := newForkedCallee(t, false)
	b2b := newB2BUA(t, &hooks{}, f.uri())
	offers := make(chan *sip.Message, 2)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	var took atomic.Bool
	inv := invitation(t, b2b, func(r *sip.InDialogRequest) {
		if took.CompareAndSwap(false, true) {
			// The caller cannot take the offer of the first dialog, such
			// as one that asks for a codec it does not have (RFC 3261
			// 14.2).
			offers <- r.Request
			r.Reject(488)
			return
		}
		answerOffers(offers)(r)
	})
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	first := within(t, "183 of the first dialog", out.Responses())
	d := within(t, "the early dialog of the first dialog", f.dialogs)
	if resp := f.send(d, sip.UPDATE, branchVersion(1, 2, "")); resp == nil || resp.StatusCode != 488 {
		t.Fatalf("the UPDATE of the first dialog got %v, want the 488 of the caller", resp)
	}
	within(t, "the UPDATE at the caller", offers)
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}

	req := within(t, "the offer at the caller", offers)
	had, err := sdp.Parse(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := sdp.Parse(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := had.Origin
	want.Version++
	if offer.Origin != want {
		t.Errorf("the %s has the o= line %+v, want %+v, one above that of the last session description that the caller took",
			req.Method, offer.Origin, want)
	}
	out.Bye()
}
