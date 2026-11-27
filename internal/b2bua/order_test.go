package b2bua_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file shows that each side's requests within the dialogs reach
// the other side in the order they came, however long the role takes to
// prepare one of them. A test holds a request in the role's hooks until
// the request after it has reached the other side, or for half a
// second if it does not; a Call that let the second one go first shows
// it then.

const overtake = 500 * time.Millisecond

// rawCallee is a callee that is a SIP stack of the test, so that it can
// send requests within its dialog back to back, without waiting for the
// final response of the one before. It answers the INVITE with a 200
// with the answer, and every other request with 200.
type rawCallee struct {
	t      *testing.T
	stack  *sip.Stack
	dialog chan sip.Dialog
	// handle, when set, takes the requests within the dialog for the
	// test to answer, where the callee otherwise answers each with 200
	// (handleRequests).
	handle chan *sip.ServerTransaction
	// rseq counts the reliable provisional responses that the callee
	// sends within one INVITE (RFC 3262 3).
	rseq int
}

// handleRequests has the test answer the requests that the callee
// receives within its dialog, and returns where they come. The test
// calls it before the first call reaches the callee.
func (rc *rawCallee) handleRequests() <-chan *sip.ServerTransaction {
	rc.handle = make(chan *sip.ServerTransaction, 8)
	return rc.handle
}

func newRawCallee(t *testing.T, tm sip.Timers) *rawCallee {
	t.Helper()
	tl, err := sip.Listen("127.0.0.1:0", discard)
	if err != nil {
		t.Fatal(err)
	}
	rc := &rawCallee{t: t, dialog: make(chan sip.Dialog, 1)}
	stack, err := sip.NewStack(tl, sip.Config{SentBy: tl.LocalAddr().String(), Timers: tm, OnRequest: rc.onRequest, OnACK: func(*sip.Message) {}}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	rc.stack = stack
	return rc
}

func (rc *rawCallee) uri() string { return "sip:callee@" + rc.stack.LocalAddr().String() }

func (rc *rawCallee) onRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.INVITE || ims.IsInDialog(st.Request) {
		if rc.handle != nil {
			rc.handle <- st
			return
		}
		st.Respond(sip.NewResponse(st.Request, 200, ""))
		return
	}
	ok := sip.NewResponse(st.Request, 200, "callee")
	for _, rr := range st.Request.GetAll("Record-Route") {
		ok.Add("Record-Route", rr)
	}
	ok.Add("Contact", "<"+rc.uri()+">")
	ok.Set("Content-Type", answer.Type)
	ok.Body = answer.Content
	d, err := sip.NewUASDialog(st.Request, ok)
	if err != nil {
		st.Respond(sip.NewResponse(st.Request, 500, "callee"))
		return
	}
	st.Respond(ok)
	rc.dialog <- d
}

// send sends a request of method with body and headers within d to the
// B2BUA.
func (rc *rawCallee) send(d *sip.Dialog, method sip.Method, body sip.Body, headers ...sip.HeaderField) *sip.ClientTransaction {
	rc.t.Helper()
	req := d.NewRequest(method)
	for _, h := range headers {
		req.Add(h.Name, h.Value)
	}
	if len(body.Content) > 0 {
		req.Set("Content-Type", body.Type)
		req.Body = body.Content
	}
	hop, err := sip.NextHop(req)
	if err != nil {
		rc.t.Fatal(err)
	}
	addr, err := netip.ParseAddr(hop.Host)
	if err != nil {
		rc.t.Fatal(err)
	}
	ct, err := rc.stack.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: netip.AddrPortFrom(addr, uint16(hop.Port))})
	if err != nil {
		rc.t.Fatal(err)
	}
	return ct
}

// finalTo is the final response of ct, nil when none came.
func finalTo(ct *sip.ClientTransaction) *sip.Message {
	var final *sip.Message
	for resp := range ct.Responses() {
		final = resp
	}
	return final
}

// before reports whether got has a before b, both as "CSeq method".
func before(got []string, a, b string) bool {
	i, j := slices.Index(got, a), slices.Index(got, b)
	return i >= 0 && j > i
}

// The callee's 183 has the answer, and the caller acknowledges it with
// a PRACK without a body and then sends an UPDATE, once the PRACK has
// its 200 (RFC 3262 4, RFC 3311 5.1). The B2BUA answers that PRACK at
// once, as a UA would, and the callee gets the PRACK of its 183 before
// the UPDATE all the same, as the caller sent them.
func TestPRACKWithoutABodyGoesBeforeTheUPDATEAfterIt(t *testing.T) {
	calleeWire := newWire()
	h := &hooks{}
	h.request = func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal {
		if req.Method == sip.PRACK && d == b2bua.ToCallee {
			until(overtake, func() bool { return calleeWire.has(sip.UPDATE) })
		}
		return nil
	}
	calls := make(chan *sip.IncomingCall, 1)
	callee := startUA(t, "callee", timers, calleeWire, func(in *sip.IncomingCall) {
		in.Handle(func(r *sip.InDialogRequest) { r.Accept(answer) })
		calls <- in
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "INVITE at the callee", calls)
	progressed := make(chan error, 1)
	go func() { progressed <- in.Progress(183, answer) }()
	if prov := within(t, "183 at the caller", out.Responses()); prov.StatusCode != 183 {
		t.Fatalf("the caller got %d, want the 183", prov.StatusCode)
	}
	resp, err := out.Update(offer)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the UPDATE got %v: %v", resp, err)
	}
	if err := within(t, "PRACK at the callee", progressed); err != nil {
		t.Fatalf("the callee's 183 got no PRACK: %v", err)
	}
	if got := calleeWire.requests(); !before(got, "2 PRACK", "3 UPDATE") {
		t.Errorf("the callee received %v, want the PRACK and then the UPDATE", got)
	}
	out.Cancel()
}

// dtmf is the body of an INFO that carries the digit of a key press.
func dtmf(digit string) sip.Body {
	return sip.Body{Type: "application/dtmf-relay", Content: []byte("Signal=" + digit + "\r\nDuration=160\r\n")}
}

// The callee sends two INFOs back to back. The B2BUA takes its time to
// prepare the first, and the caller gets the two in the order the
// callee sent them: the callee's requests have a line of their own.
func TestTheCalleesRequestsKeepTheirOrder(t *testing.T) {
	callerWire := newWire()
	h := &hooks{}
	h.request = func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal {
		if string(req.Body) == string(dtmf("1").Content) {
			until(overtake, func() bool { return callerWire.has("INFO") })
		}
		return nil
	}
	rc := newRawCallee(t, timers)
	b2b := newB2BUA(t, h, rc.uri())
	caller := startUA(t, "caller", timers, callerWire, func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) { r.Accept(sip.Body{}) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	d := within(t, "dialog at the callee", rc.dialog)
	one, two := rc.send(&d, "INFO", dtmf("1")), rc.send(&d, "INFO", dtmf("2"))
	for i, ct := range []*sip.ClientTransaction{one, two} {
		if final := finalTo(ct); final == nil || final.StatusCode != 200 {
			t.Errorf("INFO %d got %v: %v", i+1, final, ct.Err())
		}
	}
	if got, want := callerWire.bodies("INFO"), []string{string(dtmf("1").Content), string(dtmf("2").Content)}; !slices.Equal(got, want) {
		t.Errorf("the caller received the INFOs with %q, want %q, in the order the callee sent them", got, want)
	}
	out.Bye()
}

// The caller sends a re-INVITE without an offer: the callee's 2xx has
// the offer, and the caller's ACK the answer (RFC 3261 14.1). Right
// after the ACK the caller sends an UPDATE with a new offer. The B2BUA
// takes its time to put the answer on the callee's side, and the UPDATE
// waits for it: before the ACK, the callee would find its offer still
// open.
func TestTheUPDATEAfterAReInviteWithoutAnOfferWaitsForTheACK(t *testing.T) {
	calleeWire := newWire()
	h := &hooks{}
	h.ack = func(ack *sip.Message, d b2bua.Direction) {
		if d == b2bua.ToCallee && ack.Get("CSeq") != "1 ACK" {
			until(overtake, func() bool { return calleeWire.has(sip.UPDATE) })
		}
	}
	callee := startUA(t, "callee", timers, calleeWire, func(in *sip.IncomingCall) {
		// The answer to the UPDATE's offer, and the offer of the 2xx to
		// the re-INVITE without one.
		in.Handle(func(r *sip.InDialogRequest) { r.Accept(answer) })
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	ok, err := out.ReInvite(sip.Body{})
	if err != nil || ok.StatusCode != 200 || len(ok.Body) == 0 {
		t.Fatalf("the re-INVITE got %v: %v, want a 200 with the callee's offer", ok, err)
	}
	if err := out.Ack(offer); err != nil {
		t.Fatalf("ACK with the answer: %v", err)
	}
	resp, err := out.Update(offer)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("the UPDATE got %v: %v, want the callee's 200", resp, err)
	}
	if got := calleeWire.requests(); !before(got, "2 ACK", "3 UPDATE") {
		t.Errorf("the callee received %v, want the ACK of the re-INVITE and then the UPDATE", got)
	}
	out.Bye()
}

// The callee sends two INFOs back to back, and the caller does not
// answer the first. The second waits for its turn; when the caller
// hangs up, it gets 487 at once, the response that a request pending
// when a BYE ends the dialog gets (RFC 3261 15.1.2), and does not wait
// for the first to time out (RFC 3261 17.1.2.2).
func TestARequestWaitingForItsTurnEndsWithTheLeg(t *testing.T) {
	slow := sip.Timers{T1: 100 * time.Millisecond, T2: 800 * time.Millisecond, T4: 500 * time.Millisecond}
	rc := newRawCallee(t, slow)
	b2b := startB2BUA(t, &hooks{}, rc.uri(), slow)
	held := make(chan *sip.InDialogRequest, 2)
	caller := startUA(t, "caller", slow, nil, func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) { held <- r }))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	d := within(t, "dialog at the callee", rc.dialog)
	rc.send(&d, "INFO", dtmf("1"))
	within(t, "first INFO at the caller", held)
	second := rc.send(&d, "INFO", dtmf("2"))
	select {
	case r := <-held:
		t.Fatalf("the caller got the second INFO with CSeq %s before the first had its answer", r.Request.Get("CSeq"))
	case <-time.After(4 * slow.T1):
	}
	if err := out.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	hungUp := time.Now()
	final := finalTo(second)
	if final == nil || final.StatusCode != 487 {
		t.Fatalf("the second INFO got %v: %v, want 487", final, second.Err())
	}
	// The first INFO would time out after 64*T1.
	if waited := time.Since(hungUp); waited > 16*slow.T1 {
		t.Errorf("the second INFO got its 487 %v after the caller hung up, want it at once", waited)
	}
}
