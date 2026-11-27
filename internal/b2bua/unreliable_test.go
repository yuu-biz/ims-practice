package b2bua_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the reliability of the copies of the callee's provisional
// responses to the INVITE of the call: a copy goes to the caller
// reliably only when the callee's response came reliably (RFC 3262 3),
// and the caller takes its answer from the first reliable response that
// carries one (RFC 3261 13.2.1).

// TestAnUnreliable183IsCopiedUnreliably: the callee shows a session in
// a 183 that is not reliable, and answers for real in its 200, at
// another version of its session. The caller's INVITE offered 100rel,
// but the copy of the 183 reaches it unreliably too, so no PRACK goes
// either way, and the caller takes its answer from the 200, the one the
// callee gave: a reliable copy would have the caller take the session
// of the 183 for the answer and leave the one of the 200 unread (RFC
// 3261 13.2.1, RFC 3262 5).
func TestAnUnreliable183IsCopiedUnreliably(t *testing.T) {
	calleeWire := newWire()
	calls := make(chan *sip.IncomingCall, 1)
	callee := startUA(t, "callee", timers, calleeWire, func(in *sip.IncomingCall) { calls <- in })
	b2b := newB2BUA(t, &hooks{}, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "INVITE at the callee", calls)
	if err := in.ProgressUnreliably(183, answer); err != nil {
		t.Fatalf("183: %v", err)
	}
	prov := within(t, "the 183 at the caller", out.Responses())
	if prov.StatusCode != 183 || prov.Requires(sip.Option100rel) || prov.Get("RSeq") != "" ||
		string(prov.Body) != string(answer.Content) {
		t.Fatalf("the caller got %d with Require %q, RSeq %q and %q, want an unreliable 183 with the session of the callee",
			prov.StatusCode, prov.Get("Require"), prov.Get("RSeq"), prov.Body)
	}
	go in.Accept(lateAnswer)
	_, final := finalOf(t, out)
	if final.StatusCode != 200 || string(final.Body) != string(lateAnswer.Content) {
		t.Fatalf("the call got %d with %q, want 200 with the answer of the callee's 200", final.StatusCode, final.Body)
	}
	if calleeWire.has(sip.PRACK) {
		t.Error("the callee got a PRACK for a 183 that it sent unreliably")
	}
	out.Bye()
}

// TestThe2xxCarriesTheAnswerThatTheCallerGotUnreliably: the caller's
// INVITE offers no 100rel, and the callee answers in a reliable 183,
// which the B2BUA acknowledges itself (RFC 3262 4). The copy reaches the
// caller unreliably, so its session description is no answer there,
// and the callee's 200 carries none, since its answer went reliably
// already. The 200 that reaches the caller is then its first reliable
// response, and carries the callee's answer (RFC 3261 13.2.1).
func TestThe2xxCarriesTheAnswerThatTheCallerGotUnreliably(t *testing.T) {
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) { calls <- in })
	b2b := newB2BUA(t, &hooks{}, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, nil)
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Supported", Value: sip.OptionTimer})
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "INVITE at the callee", calls)
	if err := in.Progress(183, answer); err != nil {
		t.Fatalf("the callee's reliable 183 got no PRACK: %v", err)
	}
	prov := within(t, "the 183 at the caller", out.Responses())
	if prov.StatusCode != 183 || prov.Requires(sip.Option100rel) {
		t.Fatalf("the caller got %d with Require %q, want an unreliable 183", prov.StatusCode, prov.Get("Require"))
	}
	go in.Accept(sip.Body{})
	_, final := finalOf(t, out)
	if final.StatusCode != 200 || string(final.Body) != string(answer.Content) {
		t.Fatalf("the call got %d with %q, want 200 with the answer of the callee's 183", final.StatusCode, final.Body)
	}
	out.Bye()
}
