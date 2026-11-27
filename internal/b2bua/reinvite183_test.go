package b2bua_test

import (
	"strconv"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the relay of a re-INVITE through the B2BUA: the
// provisional responses of the far leg, the PRACK of each reliable one,
// and the UPDATE that follows the answer while the re-INVITE is still
// pending (RFC 3262, RFC 3312). The callee is a stack of the test
// (rawCallee), since the UA of package sip answers a re-INVITE with a
// final response only, and lists 100rel in Supported in every
// re-INVITE it sends.

// The sessions that the two ends offer and answer within the call,
// after the offer of the INVITE and the answer of its 200.
var (
	reOffer    = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 2 IN IP4 127.0.0.1\r\n")}
	reAnswer   = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 2 IN IP4 127.0.0.1\r\n")}
	lateOffer  = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 3 IN IP4 127.0.0.1\r\n")}
	lateAnswer = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 3 IN IP4 127.0.0.1\r\n")}
)

// next is the next request within the dialog that the callee received,
// which has to be of method.
func (rc *rawCallee) next(method sip.Method) *sip.ServerTransaction {
	rc.t.Helper()
	st := within(rc.t, "a "+string(method)+" at the callee", rc.handle)
	if st.Request.Method != method {
		rc.t.Fatalf("the callee got %s, want %s", st.Request.Method, method)
	}
	return st
}

// respond answers the request of st with status, the Contact of the
// callee and body.
func (rc *rawCallee) respond(st *sip.ServerTransaction, status int, body sip.Body) {
	rc.t.Helper()
	resp := sip.NewResponse(st.Request, status, "callee")
	resp.Add("Contact", "<"+rc.uri()+">")
	if len(body.Content) > 0 {
		resp.Set("Content-Type", body.Type)
		resp.Body = body.Content
	}
	if err := st.Respond(resp); err != nil {
		rc.t.Fatalf("the callee could not send its %d: %v", status, err)
	}
}

// reliable answers the request of st with a reliable provisional
// response that carries body (RFC 3262 3).
func (rc *rawCallee) reliable(st *sip.ServerTransaction, status int, body sip.Body) {
	rc.t.Helper()
	rc.rseq++
	resp := sip.NewResponse(st.Request, status, "callee")
	resp.Add("Contact", "<"+rc.uri()+">")
	resp.Add("Require", sip.Option100rel)
	resp.Add("RSeq", strconv.Itoa(rc.rseq))
	if len(body.Content) > 0 {
		resp.Set("Content-Type", body.Type)
		resp.Body = body.Content
	}
	if err := st.Respond(resp); err != nil {
		rc.t.Fatalf("the callee could not send its reliable %d: %v", status, err)
	}
}

// answeredCall is a call of a caller through the B2BUA b2b that the
// callee answered with its SDP answer. onRequest, when set, takes the
// requests within the dialog that reach the caller.
func answeredCall(t *testing.T, rc *rawCallee, b2b *sip.UA, onRequest func(*sip.InDialogRequest)) (*sip.OutgoingCall, sip.Dialog) {
	t.Helper()
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, onRequest))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	return out, within(t, "dialog at the callee", rc.dialog)
}

// TestReliable183ToAReINVITECrossesBothLegs: the caller's re-INVITE
// requires preconditions, which crosses to the callee, and the callee
// answers it in a reliable 183. That 183 reaches the caller as a
// reliable 183 of the B2BUA, the caller's PRACK for it reaches the
// callee as the PRACK of the callee's own 183, and the UPDATE with
// which the caller tells that its resources are reserved crosses while
// the re-INVITE is still pending (RFC 3312 13.1, TS 24.229 6.1.4.2 item
// b). The 200 to the re-INVITE then needs no session description, since
// the answer went in the reliable 183 (RFC 3261 13.2.1, RFC 3262 5).
func TestReliable183ToAReINVITECrossesBothLegs(t *testing.T) {
	rc := newRawCallee(t, timers)
	requests := rc.handleRequests()
	// The role passes the header fields of the one leg on to the other,
	// the Require of the far end among them (CrossingFields).
	b2b := newB2BUA(t, &hooks{crossing: true}, rc.uri())
	out, _ := answeredCall(t, rc, b2b, nil)

	provisional := make(chan *sip.Message, 4)
	done := make(chan result, 1)
	go func() {
		resp, err := out.ReInviteWith(sip.ReInvitation{
			Body:          reOffer,
			Headers:       []sip.HeaderField{{Name: "Require", Value: sip.OptionPrecondition}},
			ManualPRACK:   true,
			OnProvisional: func(p *sip.Message) { provisional <- p },
		})
		done <- result{resp, err}
	}()
	re := rc.next(sip.INVITE)
	switch {
	case string(re.Request.Body) != string(reOffer.Content):
		t.Fatalf("the callee's re-INVITE carries %q, want the caller's offer", re.Request.Body)
	case !re.Request.Requires(sip.OptionPrecondition):
		t.Errorf("the callee's re-INVITE has Require %q, want precondition", re.Request.Get("Require"))
	case !re.Request.Supports(sip.Option100rel):
		t.Errorf("the callee's re-INVITE has Supported %q, want 100rel", re.Request.Get("Supported"))
	}

	// The callee answers in a reliable 183, which reaches the caller.
	rc.reliable(re, 183, reAnswer)
	p := within(t, "the 183 of the callee at the caller", provisional)
	switch {
	case p.StatusCode != 183 || !p.Requires(sip.Option100rel):
		t.Fatalf("the caller got %d with Require %q, want a reliable 183", p.StatusCode, p.Get("Require"))
	case string(p.Body) != string(reAnswer.Content):
		t.Fatalf("the 183 at the caller carries %q, want the answer of the callee", p.Body)
	}

	// The caller acknowledges it, and the PRACK goes on as the PRACK of
	// the callee's own 183.
	pracked := make(chan result, 1)
	go func() {
		resp, err := out.PRACK(p, sip.Body{})
		pracked <- result{resp, err}
	}()
	prack := rc.next(sip.PRACK)
	want := sip.RAck{RSeq: 1, CSeq: cseqOf(t, re.Request)}.String()
	if prack.Request.Get("RAck") != want {
		t.Errorf("the callee's PRACK has RAck %q, want %q of its own 183", prack.Request.Get("RAck"), want)
	}
	rc.respond(prack, 200, sip.Body{})
	if r := within(t, "the 200 to the caller's PRACK", pracked); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("the caller's PRACK got %v %v, want 200", r.resp, r.err)
	}

	// The caller tells that its resources are reserved, in an UPDATE
	// that crosses while the re-INVITE is pending.
	updated := make(chan result, 1)
	go func() {
		resp, err := out.Update(lateOffer)
		updated <- result{resp, err}
	}()
	update := rc.next(sip.UPDATE)
	if string(update.Request.Body) != string(lateOffer.Content) {
		t.Fatalf("the callee's UPDATE carries %q, want the caller's offer", update.Request.Body)
	}
	rc.respond(update, 200, lateAnswer)
	if r := within(t, "the 200 to the caller's UPDATE", updated); r.err != nil ||
		r.resp.StatusCode != 200 || string(r.resp.Body) != string(lateAnswer.Content) {
		t.Fatalf("the caller's UPDATE got %v %v, want 200 with the answer of the callee", r.resp, r.err)
	}

	// The callee answers the re-INVITE at last.
	rc.respond(re, 200, sip.Body{})
	r := within(t, "the final response to the caller's re-INVITE", done)
	switch {
	case r.err != nil:
		t.Fatalf("re-INVITE: %v", r.err)
	case r.resp.StatusCode != 200:
		t.Fatalf("the caller's re-INVITE got %d, want 200", r.resp.StatusCode)
	case len(r.resp.Body) != 0:
		t.Errorf("the 200 to the caller's re-INVITE carries %q, want none after the answer of the 183", r.resp.Body)
	}
	if len(requests) != 0 {
		t.Errorf("the callee got %d more requests than the re-INVITE, the PRACK and the UPDATE", len(requests))
	}
}

// TestSDPOfThe2xxAfterAReliable183DoesNotReachTheCaller: the 200 that
// follows the reliable 183 with the answer carries SDP of its own. The
// answer of the 183 is the one that counts, and the caller has it
// already (RFC 3261 13.2.1), so the 200 that the B2BUA sends the caller
// carries none and both legs have the same session.
func TestSDPOfThe2xxAfterAReliable183DoesNotReachTheCaller(t *testing.T) {
	rc := newRawCallee(t, timers)
	rc.handleRequests()
	b2b := newB2BUA(t, &hooks{}, rc.uri())
	out, _ := answeredCall(t, rc, b2b, nil)

	provisional := make(chan *sip.Message, 4)
	done := make(chan result, 1)
	go func() {
		resp, err := out.ReInviteWith(sip.ReInvitation{Body: reOffer, ManualPRACK: true,
			OnProvisional: func(p *sip.Message) { provisional <- p }})
		done <- result{resp, err}
	}()
	re := rc.next(sip.INVITE)
	rc.reliable(re, 183, reAnswer)
	p := within(t, "the 183 of the callee at the caller", provisional)
	go out.PRACK(p, sip.Body{})
	rc.respond(rc.next(sip.PRACK), 200, sip.Body{})
	rc.respond(re, 200, lateAnswer)
	r := within(t, "the final response to the caller's re-INVITE", done)
	if r.err != nil || r.resp.StatusCode != 200 || len(r.resp.Body) != 0 {
		t.Fatalf("the caller's re-INVITE got %v %v with %q, want 200 without a session description",
			r.resp, r.err, r.resp.Body)
	}
}

// TestAReINVITEWithout100relKeepsTheAnswerForThe2xx: the callee sends a
// re-INVITE that offers no 100rel, so the copies of the provisional
// responses go to it unreliably and it takes the session description of
// the 2xx only (RFC 3261 13.2.1, RFC 3262 3). The caller answers in a
// reliable 183, and its answer reaches the callee twice: in the 183,
// where it changes nothing, as RFC 3261 13.2.1 allows, and in the 200
// that the B2BUA sends in the place of the caller's, which carries
// none.
func TestAReINVITEWithout100relKeepsTheAnswerForThe2xx(t *testing.T) {
	rc := newRawCallee(t, timers)
	b2b := newB2BUA(t, &hooks{}, rc.uri())
	reinvites := make(chan *sip.InDialogRequest, 1)
	_, d := answeredCall(t, rc, b2b, func(r *sip.InDialogRequest) { reinvites <- r })

	ct := rc.send(&d, sip.INVITE, reOffer, sip.HeaderField{Name: "Contact", Value: "<" + rc.uri() + ">"})
	r := within(t, "the re-INVITE of the callee at the caller", reinvites)
	if string(r.Request.Body) != string(reOffer.Content) {
		t.Fatalf("the caller's re-INVITE carries %q, want the offer of the callee", r.Request.Body)
	}
	progressed := make(chan error, 1)
	go func() { progressed <- r.Progress(183, reAnswer) }()
	if resp := provisionalTo(t, ct); resp.StatusCode != 183 || resp.Requires(sip.Option100rel) ||
		string(resp.Body) != string(reAnswer.Content) {
		t.Errorf("the callee got %d with Require %q and %q, want an unreliable 183 with the answer",
			resp.StatusCode, resp.Get("Require"), resp.Body)
	}
	if err := within(t, "the PRACK of the B2BUA for the caller's 183", progressed); err != nil {
		t.Fatalf("183: %v", err)
	}
	if _, err := r.Accept(sip.Body{}); err != nil {
		t.Fatalf("200: %v", err)
	}
	if resp := finalTo(ct); resp == nil || resp.StatusCode != 200 || string(resp.Body) != string(reAnswer.Content) {
		t.Errorf("the callee's re-INVITE got %v, want 200 with the answer of the 183", resp)
	}
}

// result is a response and the error of the call that sent the request.
type result struct {
	resp *sip.Message
	err  error
}

// cseqOf is the CSeq of m.
func cseqOf(t *testing.T, m *sip.Message) sip.CSeq {
	t.Helper()
	cseq, err := sip.ParseCSeq(m.Get("CSeq"))
	if err != nil {
		t.Fatal(err)
	}
	return cseq
}

// provisionalTo is the next provisional response other than 100 of ct.
func provisionalTo(t *testing.T, ct *sip.ClientTransaction) *sip.Message {
	t.Helper()
	for {
		resp := within(t, "a response of the request that the callee sent", ct.Responses())
		if resp.StatusCode >= 200 {
			t.Fatalf("got %d, want a provisional response", resp.StatusCode)
		}
		if resp.StatusCode > 100 {
			return resp
		}
	}
}
